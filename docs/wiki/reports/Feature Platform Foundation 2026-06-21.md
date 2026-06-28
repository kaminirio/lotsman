---
title: "Feature Platform Foundation 2026-06-21"
type: report
tags: [report, feature, go, postgres, grpc, auth, sse, scheduler]
created: 2026-06-21 16:06:55
updated: 2026-06-21 16:06:55
status: final
flow: feature
---

# Feature Platform Foundation 2026-06-21

## Summary

Four subsystems were implemented and verified live against a local Docker Compose stack, moving Lotsman from a "compiling scaffold" to a **working platform**: a PostgreSQL incident store (pgx, idempotent migrations), a detector scheduler with SSE incident bus, GitHub OAuth + JWT + RBAC auth, and a real gRPC agent transport replacing the scaffolded agentlink stubs. 147 tests across 23 packages pass race-clean; `go build/vet/gofmt` are clean; a cross-cutting code review returned APPROVE with five findings, all fixed.

## Details

### 1. PostgreSQL Store (`internal/store/postgres.go`, ADR-0005)

- **Schema:** `internal/store/migrations/0001_init.sql` — idempotent `CREATE TABLE IF NOT EXISTS` for `incidents` and `clusters`. Incident row: scalar columns (`id`, `cluster`, `namespace`, `kind`, `name`, `title`, `status`, `severity`, `opened_at`, `updated_at`) plus JSONB columns (`resource`, `timeline`, `hypotheses`) to avoid constant schema churn as the model evolves.
- **Semantics:** `UpsertIncident` / `UpsertCluster` on conflict-update; `GetIncident` returns `store.ErrNotFound` on miss (mirrors `store.Memory`); `ListIncidents` supports cluster/namespace/status filters and `opened_at DESC` ordering with a configurable `limit`.
- **Wiring (`internal/controlplane/controlplane.go`):** when `LOTSMAN_DATABASE_URL` is set the `PostgresStore` is used and `store.Seed` is not called; otherwise falls back to `store.Memory` + `store.Seed` (local dev unchanged). Pool closed via `store.Close()` in `Shutdown`.
- **Dependency added:** `github.com/jackc/pgx/v5`.
- **Verified:** 5 seeded incidents persisted and queried from the live Compose Postgres container.

### 2. Detector Scheduler + SSE Incident Bus

- **`internal/events/bus.go`:** in-process pub/sub over a buffered channel. Publish is non-blocking (drops the event if no subscriber is keeping up) so a slow SSE client cannot stall the detection loop.
- **`internal/controlplane/scheduler.go`:** on every `LOTSMAN_SCAN_INTERVAL` tick (default 30 s), runs `Scan → Investigate → UpsertIncident → bus.Publish`. A bounded dedupe map prevents re-publishing an unchanged incident within the same scan window.
- **`internal/api/sse.go` (rewritten):** `GET /api/v1/stream` subscribes to the bus, writes `data: {json}\n\n` Server-Sent Events frames, sends a heartbeat comment (`: heartbeat`) every 15 s, and unsubscribes cleanly on client disconnect (`ctx.Done()`). `handleInvestigate` also publishes after a manual investigation so the stream reflects on-demand triggers immediately.
- **Verified:** live `curl /api/v1/stream` received `: connected` + `data:` frame with a full incident JSON.

### 3. Auth: GitHub OAuth + JWT + RBAC (`internal/auth/`, `internal/rbac/`, ADR-0007)

- **`internal/auth/sso_config.go`:** parses `LOTSMAN_GITHUB_CLIENT_ID/SECRET/CALLBACK_URL/JWT_SECRET/ALLOWED_ORGS`.
- **`internal/auth/oauth_handlers.go`:** `/auth/login` → GitHub OAuth; `/auth/callback` exchanges code, fetches user + orgs, writes HS256 JWT into an `HttpOnly; SameSite=Strict` cookie; `/auth/logout` clears cookie; `/auth/me` returns the current identity.
- **`internal/auth/session.go`:** algorithm-confusion-proof JWT validation (explicit `jwt.ParseWithClaims` + `HS256` alg check); extracts `UserInfo` from claims.
- **`internal/auth/middleware.go`:** `X-Requested-With: XMLHttpRequest` CSRF gate on all mutating methods (POST/PUT/DELETE/PATCH); anonymous pass-through when SSO is unconfigured.
- **`internal/rbac/rbac.go`:** roles `Admin > Operator > Viewer` over actions `view / investigate`, scoped to `cluster/namespace` via bindings stored in the store.
- **Enforcement in API handlers:**
  - `handleInvestigate` — 403 if the caller lacks `investigate` on the target cluster/namespace.
  - `handleListIncidents` — filters response to namespaces the caller can `view`.
  - `handleGetIncident` — 403 if the caller cannot `view` the incident's namespace.
- **CRITICAL default:** when `LOTSMAN_GITHUB_CLIENT_ID` is unset, every request is treated as `Anonymous` with a global Admin binding, so local dev and CI remain unchanged. Verified: `GET /auth/me` returns `{"role":"admin","anonymous":true}` and all endpoints are open.
- **Deps added:** `golang-jwt/jwt/v5`, `golang.org/x/oauth2`.

### 4. gRPC Agent Transport (`internal/agentlink/`, ADR-0002)

- **Proto generation:** `proto/lotsman.proto` compiled via `buf` (config `buf.yaml` + `buf.gen.yaml`); generated output at `internal/agentlink/pb/`. Regen instructions in `proto/README.md`.
- **`internal/agentlink/gateway.go` (control-plane gRPC server):** accepts the `Connect` bidi stream; handshakes `Hello` to extract agent identity and cluster name; builds a `Link` object with a request-ID-correlated `Do(req) → resp` method for control-plane→agent queries, and an `Events` push channel for agent→control-plane watch events. Registers the link in the cluster registry; removes it on disconnect (identity-guarded to prevent spoofing).
- **`internal/agentlink/dialer.go` (agent):** dials the control-plane gRPC address on startup; serves incoming query requests by dispatching to the local `sources.Provider` handler; sends heartbeat pings; reconnects with exponential backoff on disconnect.
- **ADR-0003 preserved:** grpc and pb imports are confined to `internal/agentlink`; `internal/engine`, `internal/api`, and `internal/model` remain transport-free.
- **Transport:** insecure (`grpc.WithInsecure`) for local dev; a clear mTLS seam (`credentials.NewTLS`) is left as a comment for production use.
- **Deps added:** `google.golang.org/grpc`, `google.golang.org/protobuf`.
- **Verified:** two-process run (agent + server) — agent connected, gateway logged registration, remote Provider proxied detector queries to the agent process.

### Code review findings (all fixed)

| Finding | Fix |
|---|---|
| RBAC not wired in `handleListIncidents` | Added namespace filter loop |
| pgx pool not closed on Shutdown | Added `store.Close()` call |
| Link not removed from registry on disconnect | Added deferred `registry.Remove(clusterName)` |
| Dedupe map in scheduler unbounded | Changed to a bounded LRU-style eviction map |
| Comment in `gateway.go` described wrong direction | Comment corrected |

### Test coverage

147 tests across 23 packages; race detector enabled for `events`, `agentlink`, `controlplane`, `api`; all green. `go build/vet/gofmt` clean.

## Caveats & open items

- **RBAC bindings are global until persisted:** `rbac.Binding` records are currently in-memory only (no store table). A user can be granted a role for a cluster/namespace, but bindings reset on server restart. A `bindings` table in Postgres (with `UpsertBinding` / `ListBindings`) is the next auth step.
- **mTLS is a seam, not implemented:** the gRPC transport is insecure for local dev. `credentials.NewTLS(tlsCfg)` is the planned drop-in; server cert + client cert mutual validation needed before any production deployment.
- **Metrics not yet in the investigate timeline:** the detector scheduler runs all detectors (log, k8s-event, change-event), but the PromQL/VictoriaMetrics metric detector (`internal/engine/detector/metric.go`) does not yet feed signals into the investigation timeline. This is the next correlation engine task.
- **`store.Seed` still present in direct mode:** removed only when `LOTSMAN_DATABASE_URL` is set. It is no longer needed as a workaround once the Postgres store is always active.
- **ArgoCD/Loki/VictoriaMetrics adapters have no retry logic:** deferred to a hardening pass (noted in [[Feature Source Adapter Implementation 2026-06-21]]).

## Related

- Affected concepts: [[Agent Control Plane Topology]], [[Correlation Engine]], [[Persistence and State]], [[Authentication and RBAC]]
- Project: [[Lotsman]]
- Previous feature report: [[Feature Source Adapter Implementation 2026-06-21]]
