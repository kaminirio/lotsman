package agentlink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"

	"lotsman/internal/agentlink/pb"
)

// heartbeatInterval is how often the agent sends a Heartbeat to keep the link
// warm and signal liveness to the control plane.
const heartbeatInterval = 15 * time.Second

// Handler executes a proxied Request locally on the agent (against its concrete
// sources.Provider) and returns the Response to stream back to the control plane.
type Handler func(context.Context, Request) Response

// Dialer is the agent side of the link: it dials OUT to the control plane,
// serves proxied Requests via Handler, and streams watch Events upward.
type Dialer struct {
	addr     string
	cluster  string
	token    string
	version  string
	caps     []string
	logger   *slog.Logger
	pushFeed func(context.Context) <-chan Event // optional watch-event source

	// dialOpts are extra grpc.DialOptions appended to the default insecure
	// transport — used by tests to inject a bufconn context dialer.
	dialOpts []grpc.DialOption
}

// NewDialer constructs the agent-side dialer. cluster/version/caps default to
// empty; use the option setters before Run to populate Hello.
func NewDialer(addr, token string, logger *slog.Logger) *Dialer {
	return &Dialer{addr: addr, token: token, logger: logger}
}

// WithIdentity sets the Hello fields (cluster, agent version, capabilities).
func (d *Dialer) WithIdentity(cluster, version string, caps []string) *Dialer {
	d.cluster, d.version, d.caps = cluster, version, caps
	return d
}

// WithEventFeed registers a source of watch Events to push to the control plane.
// The feed is (re)subscribed per connection and drained until the stream ends.
func (d *Dialer) WithEventFeed(feed func(context.Context) <-chan Event) *Dialer {
	d.pushFeed = feed
	return d
}

// Run connects to the control plane and serves until ctx is cancelled,
// reconnecting with capped backoff on transient stream failures.
func (d *Dialer) Run(ctx context.Context, handler Handler) error {
	// mTLS seam (ADR-0002): swap insecure for credentials.NewTLS(clientTLSConfig)
	// once agent certs are issued; for local dev the link is plaintext.
	opts := append([]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, d.dialOpts...)
	conn, err := grpc.NewClient(d.addr, opts...)
	if err != nil {
		return fmt.Errorf("agentlink: dial %s: %w", d.addr, err)
	}
	defer conn.Close()

	client := pb.NewAgentServiceClient(conn)

	backoff := time.Second
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		serveErr := d.serveOnce(ctx, client, handler)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		d.logger.Warn("agent link dropped, reconnecting", "err", serveErr, "backoff", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// serveOnce opens one Connect stream, sends Hello, and runs the recv + push +
// heartbeat loops until the stream errors or ctx is cancelled.
func (d *Dialer) serveOnce(ctx context.Context, client pb.AgentServiceClient, handler Handler) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := client.Connect(streamCtx)
	if err != nil {
		return fmt.Errorf("agentlink: open connect stream: %w", err)
	}

	hello := &pb.AgentMessage{Payload: &pb.AgentMessage_Hello{Hello: &pb.Hello{
		Cluster:      d.cluster,
		AgentVersion: d.version,
		Token:        d.token,
		Capabilities: d.caps,
	}}}
	if err := stream.Send(hello); err != nil {
		return fmt.Errorf("agentlink: send hello: %w", err)
	}
	d.logger.Info("agent link established", "cluster", d.cluster, "control_plane", d.addr)

	// sendMu serializes Send across the query-reply, heartbeat, and event
	// goroutines (gRPC streams allow only one concurrent Send).
	var sendMu sync.Mutex
	send := func(m *pb.AgentMessage) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(m)
	}

	go d.heartbeatLoop(streamCtx, send)
	if d.pushFeed != nil {
		go d.pushLoop(streamCtx, send)
	}

	// Recv loop: handle queries until the stream ends.
	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		q := msg.GetQuery()
		if q == nil {
			d.logger.Warn("agentlink: control-plane message without query")
			continue
		}
		go d.handleQuery(streamCtx, send, handler, q)
	}
}

func (d *Dialer) handleQuery(ctx context.Context, send func(*pb.AgentMessage) error, handler Handler, q *pb.Query) {
	kind, ok := kindFromProto[q.GetKind()]
	resp := Response{}
	if !ok {
		resp.Err = fmt.Sprintf("agentlink: unknown request kind %v", q.GetKind())
	} else {
		resp = handler(ctx, Request{Kind: kind, Cluster: d.cluster, Payload: q.GetPayload()})
	}
	out := &pb.AgentMessage{Payload: &pb.AgentMessage_Result{Result: &pb.QueryResult{
		RequestId: q.GetRequestId(),
		Payload:   resp.Payload,
		Error:     resp.Err,
	}}}
	if err := send(out); err != nil && !errors.Is(err, context.Canceled) {
		d.logger.Warn("agentlink: send query result", "request_id", q.GetRequestId(), "err", err)
	}
}

func (d *Dialer) heartbeatLoop(ctx context.Context, send func(*pb.AgentMessage) error) {
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			hb := &pb.AgentMessage{Payload: &pb.AgentMessage_Heartbeat{
				Heartbeat: &pb.Heartbeat{At: timestamppb.Now()},
			}}
			if err := send(hb); err != nil {
				return // stream is gone; recv loop will surface the error
			}
		}
	}
}

func (d *Dialer) pushLoop(ctx context.Context, send func(*pb.AgentMessage) error) {
	feed := d.pushFeed(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-feed:
			if !ok {
				return
			}
			signal, err := marshalSignal(ev)
			if err != nil {
				d.logger.Warn("agentlink: marshal event signal", "err", err)
				continue
			}
			msg := &pb.AgentMessage{Payload: &pb.AgentMessage_Event{Event: &pb.Event{
				Cluster: ev.Cluster,
				Signal:  signal,
			}}}
			if err := send(msg); err != nil {
				return
			}
		}
	}
}
