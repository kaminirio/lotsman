// Package agentlink_test contains integration tests that wire the real
// enrollment.Validator (backed by store.NewMemory) into the Gateway, proving the
// store→validator→gateway contract end to end. An external _test package is used
// so the file can import lotsman/internal/enrollment and lotsman/internal/store
// without creating an import cycle with agentlink.
package agentlink_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"lotsman/internal/agentlink"
	"lotsman/internal/agentlink/pb"
	"lotsman/internal/enrollment"
	"lotsman/internal/store"
)

// durableMem wraps the in-memory store but reports Durable()==true. The
// enrollment validator requires a durable store (Postgres in production); these
// tests use this wrapper to satisfy that precondition without a live database.
type durableMem struct{ *store.Memory }

func (durableMem) Durable() bool { return true }

// seedToken generates a fresh enrollment token, persists it in st bound to
// cluster, and returns the plaintext the agent would present in its Hello.
// expiresAt zero means non-expiring; revoked=true pre-revokes the record.
func seedToken(t *testing.T, st *store.Memory, cluster string, expiresAt time.Time, revoked bool) string {
	t.Helper()
	plaintext, hash, id, err := enrollment.Generate()
	if err != nil {
		t.Fatalf("enrollment.Generate: %v", err)
	}
	rec := store.EnrollmentToken{
		ID:        id,
		Cluster:   cluster,
		Hash:      hash,
		CreatedAt: time.Now(),
		ExpiresAt: expiresAt,
		Revoked:   revoked,
	}
	if err := st.SaveEnrollmentToken(context.Background(), rec); err != nil {
		t.Fatalf("SaveEnrollmentToken: %v", err)
	}
	return plaintext
}

// ---------------------------------------------------------------------------
// Part 1: store → enrollment.Validator contract (table-driven direct calls)
// ---------------------------------------------------------------------------

// TestEnrollmentValidatorIntegration exercises every authentication outcome
// through a real enrollment.Validator backed by store.NewMemory, without the
// gRPC layer. Each sub-test gets an independent store instance.
func TestEnrollmentValidatorIntegration(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name    string
		arrange func(t *testing.T, st *store.Memory) (cluster, token string)
		wantErr bool
	}{
		{
			name: "valid token accepted for its cluster",
			arrange: func(t *testing.T, st *store.Memory) (string, string) {
				tok := seedToken(t, st, "prod-eu", time.Time{}, false)
				return "prod-eu", tok
			},
			wantErr: false,
		},
		{
			name: "valid token rejected for different cluster (spoofing guard)",
			arrange: func(t *testing.T, st *store.Memory) (string, string) {
				// Token was issued for prod-eu; agent claims to be staging.
				tok := seedToken(t, st, "prod-eu", time.Time{}, false)
				return "staging", tok
			},
			wantErr: true,
		},
		{
			name: "revoked token rejected",
			arrange: func(t *testing.T, st *store.Memory) (string, string) {
				tok := seedToken(t, st, "prod-eu", time.Time{}, true)
				return "prod-eu", tok
			},
			wantErr: true,
		},
		{
			name: "expired token rejected",
			arrange: func(t *testing.T, st *store.Memory) (string, string) {
				tok := seedToken(t, st, "prod-eu", time.Now().Add(-time.Hour), false)
				return "prod-eu", tok
			},
			wantErr: true,
		},
		{
			name: "unknown token rejected",
			arrange: func(t *testing.T, st *store.Memory) (string, string) {
				// Store has a real token; present a completely different one.
				_ = seedToken(t, st, "prod-eu", time.Time{}, false)
				return "prod-eu", "lse_garbage-token-that-was-never-issued"
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mem := store.NewMemory()
			v := enrollment.NewValidator(durableMem{mem})
			cluster, token := tc.arrange(t, mem)
			err := v.ValidateEnrollment(ctx, cluster, token)
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected success, got: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Part 2: enrollment.Validator → Gateway → gRPC Connect path
// ---------------------------------------------------------------------------

// newBufconnGateway wires a Gateway backed by a real enrollment.Validator over
// an in-memory bufconn. It returns a raw gRPC client for Connect calls, a
// buffered channel that receives each Link delivered by onConnect, and a cleanup
// func the caller must defer.
func newBufconnGateway(t *testing.T, mem *store.Memory) (pb.AgentServiceClient, chan agentlink.Link, func()) {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	validator := enrollment.NewValidator(durableMem{mem})

	linkCh := make(chan agentlink.Link, 4)
	gw := agentlink.NewGateway(
		"bufconn", validator, logger,
		func(l agentlink.Link) { linkCh <- l },
		nil,
	)

	srv := grpc.NewServer()
	pb.RegisterAgentServiceServer(srv, gw)
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}

	client := pb.NewAgentServiceClient(conn)
	cleanup := func() {
		_ = conn.Close()
		srv.Stop()
		_ = lis.Close()
	}
	return client, linkCh, cleanup
}

// openHello opens a Connect stream, sends the Hello, and returns the stream.
// Any error causes t.Fatal.
func openHello(
	t *testing.T,
	ctx context.Context,
	client pb.AgentServiceClient,
	cluster, token string,
) pb.AgentService_ConnectClient {
	t.Helper()
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	hello := &pb.AgentMessage{
		Payload: &pb.AgentMessage_Hello{
			Hello: &pb.Hello{
				Cluster:      cluster,
				Token:        token,
				AgentVersion: "v-integration-test",
			},
		},
	}
	if err := stream.Send(hello); err != nil {
		t.Fatalf("Send hello: %v", err)
	}
	return stream
}

// TestGatewayIntegration_ValidTokenConnects proves the full
// store→validator→gateway path: a freshly minted+saved token authorizes a
// Connect stream and the gateway delivers a Link for the correct cluster.
func TestGatewayIntegration_ValidTokenConnects(t *testing.T) {
	mem := store.NewMemory()
	plaintext := seedToken(t, mem, "eu-prod", time.Time{}, false)

	client, linkCh, cleanup := newBufconnGateway(t, mem)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := openHello(t, ctx, client, "eu-prod", plaintext)
	defer func() { _ = stream.CloseSend() }()

	select {
	case link := <-linkCh:
		if got := link.Cluster(); got != "eu-prod" {
			t.Fatalf("link.Cluster() = %q, want %q", got, "eu-prod")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("gateway never delivered a Link — valid token was not accepted")
	}
}

// TestGatewayIntegration_ConnectRejections asserts that every invalid token
// scenario causes the gateway to terminate the gRPC Connect stream with
// codes.Unauthenticated, tested through the real enrollment.Validator.
func TestGatewayIntegration_ConnectRejections(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(t *testing.T, mem *store.Memory) (cluster, token string)
	}{
		{
			name: "wrong cluster (spoofing guard)",
			arrange: func(t *testing.T, mem *store.Memory) (string, string) {
				tok := seedToken(t, mem, "prod-eu", time.Time{}, false)
				return "staging", tok // token bound to prod-eu; claim staging
			},
		},
		{
			name: "revoked token",
			arrange: func(t *testing.T, mem *store.Memory) (string, string) {
				tok := seedToken(t, mem, "prod-eu", time.Time{}, true)
				return "prod-eu", tok
			},
		},
		{
			name: "expired token",
			arrange: func(t *testing.T, mem *store.Memory) (string, string) {
				tok := seedToken(t, mem, "prod-eu", time.Now().Add(-time.Hour), false)
				return "prod-eu", tok
			},
		},
		{
			name: "unknown token",
			arrange: func(t *testing.T, mem *store.Memory) (string, string) {
				return "prod-eu", "lse_garbage-token-that-was-never-issued"
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mem := store.NewMemory()
			client, _, cleanup := newBufconnGateway(t, mem)
			defer cleanup()

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			cluster, token := tc.arrange(t, mem)
			stream := openHello(t, ctx, client, cluster, token)

			// After the server rejects the Hello the stream is terminated; the next
			// client Recv must carry codes.Unauthenticated.
			_, recvErr := stream.Recv()
			if recvErr == nil {
				t.Fatal("expected Unauthenticated error from Recv, got nil (server accepted bad token)")
			}
			st, ok := grpcstatus.FromError(recvErr)
			if !ok {
				t.Fatalf("expected a gRPC status error, got: %v", recvErr)
			}
			if st.Code() != codes.Unauthenticated {
				t.Fatalf("expected Unauthenticated, got %s: %v", st.Code(), recvErr)
			}
		})
	}
}
