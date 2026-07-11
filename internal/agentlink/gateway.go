package agentlink

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	"lotsman/internal/agentlink/pb"
	"lotsman/internal/model"
)

// queryTimeout bounds how long a control-plane Do() waits for the agent's
// QueryResult before giving up. A caller-supplied ctx deadline takes precedence
// when it is shorter.
const queryTimeout = 30 * time.Second

// maxAgentStreams caps concurrent gRPC streams per connection — a backstop
// against an abusive/unauthenticated peer opening unbounded streams.
const maxAgentStreams = 64

// envAllowInsecureAgents is the explicit local-dev opt-in that permits the
// legacy "accept any non-empty token" behavior when no enrollment token is
// configured. Default (unset) is fail-closed: the gateway refuses to start and
// rejects agent connections rather than trusting any caller that can reach the
// port. See SEC-1.
const envAllowInsecureAgents = "LOTSMAN_AGENT_ALLOW_INSECURE"

// allowInsecureAgentsFromEnv reports whether the operator explicitly opted into
// the insecure accept-any-token dev mode via LOTSMAN_AGENT_ALLOW_INSECURE.
func allowInsecureAgentsFromEnv() bool {
	switch os.Getenv(envAllowInsecureAgents) {
	case "1", "true", "TRUE", "True", "yes", "on":
		return true
	default:
		return false
	}
}

// Gateway is the control-plane side: it accepts inbound agent connections and
// exposes each as a Link via the OnConnect callback. The control plane's cluster
// registry consumes those Links.
type Gateway struct {
	addr  string
	token string // expected agent enrollment token ("" = none configured)
	// allowInsecure permits the legacy accept-any-non-empty-token behavior when
	// token is "". Default false (fail-closed); enabled only via the explicit
	// LOTSMAN_AGENT_ALLOW_INSECURE opt-in for local dev. See SEC-1.
	allowInsecure bool
	logger        *slog.Logger
	onConnect     func(Link)
	onDisconnect  func(Link)

	srv *grpc.Server
	pb.UnimplementedAgentServiceServer
}

// NewGateway constructs the agent gateway. token is the shared enrollment secret
// every agent must present in its Hello. When token is empty the gateway is
// fail-closed by default (SEC-1): it refuses to start and rejects agents, unless
// the operator sets LOTSMAN_AGENT_ALLOW_INSECURE=1 to re-enable the legacy
// local-dev "accept any non-empty token" behavior. onConnect is called once per
// agent that successfully connects and authenticates; onDisconnect once per such
// agent when its stream ends (nil is allowed).
func NewGateway(addr, token string, logger *slog.Logger, onConnect, onDisconnect func(Link)) *Gateway {
	return &Gateway{
		addr:          addr,
		token:         token,
		allowInsecure: allowInsecureAgentsFromEnv(),
		logger:        logger,
		onConnect:     onConnect,
		onDisconnect:  onDisconnect,
	}
}

// Start listens for agent connections and blocks until ctx is done or Serve
// returns. mTLS seam (ADR-0002): in production swap insecure for
// credentials.NewTLS(...) with a client-CA-verified config; for local dev the
// link is plaintext and Hello.token gates enrollment.
func (g *Gateway) Start(ctx context.Context) error {
	// Fail closed: without a configured enrollment token, any caller that can
	// reach the port could register an arbitrary cluster and receive proxied
	// user queries. Refuse to start unless the operator explicitly opted into the
	// insecure local-dev mode (SEC-1).
	if g.token == "" && !g.allowInsecure {
		return fmt.Errorf("agentlink: refusing to start: no enrollment token configured (set LOTSMAN_AGENT_TOKEN); for local dev only, set %s=1 to accept any non-empty token", envAllowInsecureAgents)
	}

	lis, err := net.Listen("tcp", g.addr)
	if err != nil {
		return fmt.Errorf("agentlink: gateway listen on %s: %w", g.addr, err)
	}

	// TODO(SEC-1): replace with mTLS credentials (per-cluster client certs).
	// grpc.Creds(credentials.NewTLS(serverTLSConfig)) once the agent enrollment
	// CA lands (ADR-0002). Until then the link is plaintext and Hello.token gates
	// enrollment; harden the server against abusive peers.
	g.srv = grpc.NewServer(
		grpc.MaxConcurrentStreams(maxAgentStreams),
		grpc.ConnectionTimeout(15*time.Second),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second,
			Timeout: 10 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	pb.RegisterAgentServiceServer(g.srv, g)

	if g.token == "" {
		g.logger.Warn("agent gateway running in INSECURE mode (LOTSMAN_AGENT_ALLOW_INSECURE set): accepting ANY non-empty enrollment token — local dev only, never in production")
	} else {
		g.logger.Info("agent gateway enrollment: token-gated (constant-time compare)")
	}
	g.logger.Info("agent gateway listening", "addr", g.addr)

	// Stop serving when the parent ctx is cancelled.
	go func() {
		<-ctx.Done()
		g.srv.GracefulStop()
	}()

	if err := g.srv.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("agentlink: gateway serve: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the gateway, draining in-flight streams.
func (g *Gateway) Shutdown(ctx context.Context) error {
	if g.srv == nil {
		return nil
	}
	stopped := make(chan struct{})
	go func() {
		g.srv.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-ctx.Done():
		g.srv.Stop()
	}
	return nil
}

// Connect handles one long-lived agent stream. The first message must be a
// Hello; afterwards a single receive loop dispatches QueryResults to waiting
// Do() callers, Events to the Link's channel, and Heartbeats to liveness.
func (g *Gateway) Connect(stream pb.AgentService_ConnectServer) error {
	first, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.Unavailable, "agentlink: recv hello: %v", err)
	}
	hello := first.GetHello()
	if hello == nil {
		return status.Error(codes.InvalidArgument, "agentlink: first message must be Hello")
	}
	if hello.GetCluster() == "" {
		return status.Error(codes.InvalidArgument, "agentlink: hello missing cluster")
	}
	// Enrollment auth. When a token is configured, require a constant-time match
	// (so the gateway can't be impersonated by any caller that can reach the
	// port). When none is configured, fail closed (reject) unless the operator
	// explicitly opted into the insecure local-dev "any non-empty token" mode
	// (SEC-1). This mirrors the Start-time refusal as defense in depth for callers
	// that register the gateway directly on a grpc.Server. In production mTLS
	// carries identity and this becomes a CA/SPIFFE check (ADR-0002).
	if hello.GetToken() == "" {
		return status.Error(codes.Unauthenticated, "agentlink: hello missing token")
	}
	if g.token == "" {
		if !g.allowInsecure {
			g.logger.Warn("agent rejected: gateway has no enrollment token and insecure mode is disabled", "cluster", hello.GetCluster())
			return status.Error(codes.Unauthenticated, "agentlink: gateway misconfigured (no enrollment token); refusing connection")
		}
		// Insecure dev opt-in: any non-empty token (checked above) is accepted.
	} else if subtle.ConstantTimeCompare([]byte(hello.GetToken()), []byte(g.token)) != 1 {
		g.logger.Warn("agent rejected: invalid enrollment token", "cluster", hello.GetCluster())
		return status.Error(codes.Unauthenticated, "agentlink: invalid enrollment token")
	}

	g.logger.Info("agent connected",
		"cluster", hello.GetCluster(),
		"agent_version", hello.GetAgentVersion(),
		"capabilities", hello.GetCapabilities())

	link := newGatewayLink(stream, hello.GetCluster(), g.logger)
	g.onConnect(link)
	// Always deregister this exact link when the stream ends, so the registry
	// stops handing it out (the deregister is identity-guarded against a newer
	// link that already replaced it).
	if g.onDisconnect != nil {
		defer g.onDisconnect(link)
	}

	err = link.recvLoop()
	g.logger.Info("agent disconnected", "cluster", hello.GetCluster(), "err", err)
	link.Close()
	return err
}

// gatewayLink is the control-plane Link backed by one agent's gRPC stream. Do()
// sends a Query and blocks on a per-request reply channel keyed by request_id;
// recvLoop demultiplexes inbound QueryResults/Events onto those channels.
type gatewayLink struct {
	stream  pb.AgentService_ConnectServer
	cluster string
	logger  *slog.Logger

	sendMu sync.Mutex // serializes stream.Send across concurrent Do() calls

	mu      sync.Mutex
	seq     uint64
	pending map[string]chan Response
	closed  bool

	events chan Event
}

func newGatewayLink(stream pb.AgentService_ConnectServer, cluster string, logger *slog.Logger) *gatewayLink {
	return &gatewayLink{
		stream:  stream,
		cluster: cluster,
		logger:  logger,
		pending: make(map[string]chan Response),
		events:  make(chan Event, 64),
	}
}

func (l *gatewayLink) Cluster() string      { return l.cluster }
func (l *gatewayLink) Events() <-chan Event { return l.events }

// Do sends req down the stream and waits for the matching QueryResult. It is
// safe for concurrent use: each call gets a unique request_id and its own reply
// channel, so concurrent in-flight queries never cross.
func (l *gatewayLink) Do(ctx context.Context, req Request) (Response, error) {
	kind, ok := kindToProto[req.Kind]
	if !ok {
		return Response{}, fmt.Errorf("agentlink: unknown request kind %q", req.Kind)
	}

	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return Response{}, ErrNotConnected
	}
	l.seq++
	id := fmt.Sprintf("%d", l.seq)
	reply := make(chan Response, 1)
	l.pending[id] = reply
	l.mu.Unlock()

	// Always clear the pending entry on the way out (timeout/cancel/success).
	defer func() {
		l.mu.Lock()
		delete(l.pending, id)
		l.mu.Unlock()
	}()

	msg := &pb.ControlPlaneMessage{
		Payload: &pb.ControlPlaneMessage_Query{Query: &pb.Query{
			RequestId: id,
			Kind:      kind,
			Payload:   req.Payload,
		}},
	}
	l.sendMu.Lock()
	err := l.stream.Send(msg)
	l.sendMu.Unlock()
	if err != nil {
		return Response{}, fmt.Errorf("agentlink: send query: %w", err)
	}

	timer := time.NewTimer(queryTimeout)
	defer timer.Stop()
	select {
	case resp := <-reply:
		return resp, nil
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case <-timer.C:
		return Response{}, fmt.Errorf("agentlink: query %s timed out after %s", id, queryTimeout)
	}
}

// recvLoop reads agent messages until the stream ends, routing each to the
// waiting Do() caller (QueryResult) or the events channel (Event).
func (l *gatewayLink) recvLoop() error {
	for {
		msg, err := l.stream.Recv()
		if err != nil {
			return err
		}
		switch p := msg.GetPayload().(type) {
		case *pb.AgentMessage_Result:
			l.deliver(p.Result)
		case *pb.AgentMessage_Event:
			l.dispatchEvent(p.Event)
		case *pb.AgentMessage_Heartbeat:
			// liveness only; nothing to do beyond keeping the stream warm.
		case *pb.AgentMessage_Hello:
			l.logger.Warn("agentlink: unexpected Hello after handshake", "cluster", l.cluster)
		default:
			l.logger.Warn("agentlink: unknown agent message payload", "cluster", l.cluster)
		}
	}
}

func (l *gatewayLink) deliver(r *pb.QueryResult) {
	l.mu.Lock()
	reply, ok := l.pending[r.GetRequestId()]
	l.mu.Unlock()
	if !ok {
		l.logger.Warn("agentlink: result for unknown request", "request_id", r.GetRequestId())
		return
	}
	reply <- Response{Payload: r.GetPayload(), Err: r.GetError()}
}

func (l *gatewayLink) dispatchEvent(e *pb.Event) {
	var sig model.Signal
	if err := json.Unmarshal(e.GetSignal(), &sig); err != nil {
		l.logger.Warn("agentlink: bad event signal json", "err", err)
		return
	}
	select {
	case l.events <- Event{Cluster: e.GetCluster(), Signal: sig}:
	default:
		l.logger.Warn("agentlink: events buffer full, dropping signal", "cluster", e.GetCluster())
	}
}

// Close marks the link dead, fails any in-flight Do() callers, and closes the
// events channel. Idempotent.
func (l *gatewayLink) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	for id, reply := range l.pending {
		reply <- Response{Err: "agentlink: link closed"}
		delete(l.pending, id)
	}
	l.mu.Unlock()
	close(l.events)
	return nil
}

var _ Link = (*gatewayLink)(nil)
