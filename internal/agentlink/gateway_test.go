package agentlink

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"lotsman/internal/agentlink/pb"
)

// tryHello registers a Gateway with the given expected token + insecure setting,
// dials it over an in-memory bufconn, sends a Hello carrying helloToken, and
// reports whether the gateway accepted the agent (fired onConnect) or rejected
// the stream. It never touches Start(), so it exercises the Connect-level
// enrollment check directly.
func tryHello(t *testing.T, gwToken string, allowInsecure bool, helloToken string) (accepted bool, recvErr error) {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	linkCh := make(chan Link, 1)
	gw := NewGateway("bufconn", gwToken, logger, func(l Link) { linkCh <- l }, nil)
	gw.allowInsecure = allowInsecure

	srv := grpc.NewServer()
	pb.RegisterAgentServiceServer(srv, gw)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	stream, err := pb.NewAgentServiceClient(conn).Connect(context.Background())
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	hello := &pb.AgentMessage{Payload: &pb.AgentMessage_Hello{Hello: &pb.Hello{
		Cluster:      "test-cluster",
		AgentVersion: "v-test",
		Token:        helloToken,
	}}}
	if err := stream.Send(hello); err != nil {
		t.Fatalf("send hello: %v", err)
	}

	// A rejected Hello makes Connect return a status error, surfacing as a Recv
	// error. An accepted Hello fires onConnect and keeps the stream open (Recv
	// blocks), so we race the link delivery against the recv error.
	recvDone := make(chan error, 1)
	go func() {
		_, err := stream.Recv()
		recvDone <- err
	}()

	select {
	case <-linkCh:
		return true, nil
	case err := <-recvDone:
		return false, err
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for accept/reject")
		return false, nil
	}
}

// TestGatewayAuth_FailClosedNoToken: no configured token and no insecure opt-in
// must reject any agent Hello (the SEC-1 fail-closed default).
func TestGatewayAuth_FailClosedNoToken(t *testing.T) {
	accepted, err := tryHello(t, "", false, "some-agent-token")
	if accepted {
		t.Fatal("expected agent to be REJECTED with no token + no insecure opt-in, but it was accepted")
	}
	if err == nil {
		t.Fatal("expected a rejection error from the stream")
	}
}

// TestGatewayAuth_InsecureOptInAcceptsAny: with the insecure opt-in and no
// configured token, the legacy accept-any-non-empty-token path still works.
func TestGatewayAuth_InsecureOptInAcceptsAny(t *testing.T) {
	accepted, err := tryHello(t, "", true, "any-token")
	if !accepted {
		t.Fatalf("expected accept-any path to accept the agent, got rejected: %v", err)
	}
}

// TestGatewayAuth_InsecureStillRejectsEmptyToken: even in insecure mode an empty
// Hello token is rejected (missing token is never valid).
func TestGatewayAuth_InsecureStillRejectsEmptyToken(t *testing.T) {
	accepted, _ := tryHello(t, "", true, "")
	if accepted {
		t.Fatal("expected empty Hello token to be rejected even in insecure mode")
	}
}

// TestGatewayAuth_ConfiguredTokenMatch: with a configured token only the exact
// matching token is accepted (constant-time compare path).
func TestGatewayAuth_ConfiguredTokenMatch(t *testing.T) {
	accepted, err := tryHello(t, "secret-token", false, "secret-token")
	if !accepted {
		t.Fatalf("expected matching token to be accepted, got rejected: %v", err)
	}
}

// TestGatewayAuth_ConfiguredTokenMismatch: a configured token rejects any
// non-matching token, regardless of the insecure opt-in.
func TestGatewayAuth_ConfiguredTokenMismatch(t *testing.T) {
	accepted, err := tryHello(t, "secret-token", true, "wrong-token")
	if accepted {
		t.Fatal("expected non-matching token to be REJECTED when a token is configured")
	}
	if err == nil {
		t.Fatal("expected a rejection error from the stream")
	}
}

// TestGatewayStart_RefusesWithoutTokenOrOptIn: Start() itself fails closed when
// no token is configured and the insecure opt-in is off.
func TestGatewayStart_RefusesWithoutTokenOrOptIn(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := NewGateway("127.0.0.1:0", "", logger, func(Link) {}, nil)
	gw.allowInsecure = false
	if err := gw.Start(context.Background()); err == nil {
		t.Fatal("expected Start to refuse to start with no token and no insecure opt-in")
	}
}

// TestAllowInsecureAgentsFromEnv checks the opt-in env parsing.
func TestAllowInsecureAgentsFromEnv(t *testing.T) {
	t.Setenv(envAllowInsecureAgents, "1")
	if !allowInsecureAgentsFromEnv() {
		t.Fatal("expected LOTSMAN_AGENT_ALLOW_INSECURE=1 to enable insecure mode")
	}
	t.Setenv(envAllowInsecureAgents, "")
	if allowInsecureAgentsFromEnv() {
		t.Fatal("expected unset LOTSMAN_AGENT_ALLOW_INSECURE to be fail-closed (false)")
	}
}
