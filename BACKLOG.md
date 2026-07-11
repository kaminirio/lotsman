# Lotsman — Improvement Backlog

_Generated 2026-07-11 from a full-repo assessment (backend adapters/engine, API/auth/security, and UI/CLI/deploy/CI/docs). Intended as the source list for isolated worktree implementation runs._

## Current state (ground truth)

Health baseline on `main`:

- `go build ./...` ✅ · `go vet ./...` ✅ · `go test ./...` ✅ **313 tests / 25 packages**
- CI runs gofmt + vet + build + test + UI build. All green.

**The scaffold is no longer a scaffold.** Every subsystem the README/`docs/ARCHITECTURE.md` §12/`CONTRIBUTING.md` still describe as _"stubbed with `TODO(impl)`, std-lib only"_ is in fact **implemented, wired, and tested**:

| Documented as stubbed | Reality |
|---|---|
| Kubernetes adapter | Real client-go impl, 1028 LOC, all 11 `ClusterSource` methods (`internal/sources/kubernetes/kubernetes.go`) |
| VictoriaMetrics / Loki / ArgoCD adapters | Real HTTP/PromQL/LogQL/REST impls (89–91% coverage) |
| Postgres store | pgx pool + embedded migrations, auto-selected when `DatabaseURL` set (`internal/store/postgres.go`) |
| gRPC agentlink | Full gateway + dialer, enrollment auth, keepalive, reconnect, 14/14 request kinds (`internal/agentlink/`) |
| Detector scheduler + SSE bus | Live poll loop + non-blocking event fan-out (`internal/controlplane/scheduler.go`, `internal/events/bus.go`) |
| Auth (OAuth + JWT + RBAC) | Complete; RBAC enforced on every handler, JWT alg pinned, OAuth CSRF/cookie flags correct |

Test coverage (weak spots worth noting): `store` 18.9% (Postgres path largely DB-gated), `sources/remote` 24%, `internal/agent` 19%, `engine/detector` 0%, `internal/config` 0%, `auth` 48%.

**Verified secure (no action):** RBAC on every resource handler, admin-gated RBAC endpoints, scope-filtered SSE, OAuth `crypto/rand` state + cookie-bound double-submit, JWT HS256/issuer/audience pinning, `HttpOnly`/`SameSite=Lax`/`Secure`-gated cookies, `session_secret` validation + placeholder denylist, `%q`-quoted LogQL, default-deny secret masking.

---

## Priority legend

Effort: **S** ≤½ day · **M** ~1–2 days · **L** >2 days. Priority: **P1** do first · **P2** next · **P3** nice-to-have. No P0 — nothing blocks the build.

---

## P1 — do first

### DOC-1 · Correct the stale "stubbed scaffold" narrative · S
`docs/ARCHITECTURE.md` §12, `CONTRIBUTING.md:8-13,21,54,60-62`, `README.md` "Status", `go.mod` header comment all claim adapters/store/gRPC/auth are `TODO(impl)` and the module is std-lib-only. Contradicted by the code, `go.mod` deps, and the test suite. Rewrite the status/roadmap to reflect what's actually done and what's left (this file). **Misleads every new contributor.**

### CI-1 · Add security scanning to CI · S
No govulncheck / gosec / trivy / CodeQL anywhere, despite `Makefile:21-23` already defining an `audit` target. Add `govulncheck ./...` + `npm audit` gate to `ci.yml`, and a trivy image scan to `release.yml:106-124` before tagging `:latest`/`:edge`.

### CI-2 · Run tests with `-race` + coverage · S
`ci.yml:43-44` runs plain `go test ./...`. The codebase is concurrent (scheduler, event bus, agentlink demux, registry) — add `-race`, plus coverage reporting/threshold to stop erosion.

### CI-3 · Node parity: CI builds UI on Node 22, Dockerfile on Node 26 · S
`.github/workflows/ci.yml:57` pins `node-version: "22"`; `Dockerfile:1` uses `node:26-alpine`. CI can pass while the shipped image build breaks. Align to Node 26.

### SEC-1 · Agent link: enforce token (fail closed) + implement mTLS · M
Two merged findings. (a) `gateway.go:82-83` empty `LOTSMAN_AGENT_TOKEN` accepts **any** non-empty token (dev fallback) — a rogue agent can register any cluster name and receive proxied user queries. Fail closed outside local dev. (b) `gateway.go:65` / `dialer.go:64` are explicit `insecure.NewCredentials()` seams — agent↔control-plane traffic (incl. secret values) is plaintext with only a shared bearer token for identity. Add mTLS / per-cluster identity.

### SRC-1 · No HTTP client timeout on Loki/VM/ArgoCD adapters · S
All three fall back to `http.DefaultClient` (no timeout) and are constructed with `nil` (`agent.go:49-51`, `controlplane.go:69-71`); `engine.Investigate`/`Correlator.Timeline` set no deadline either. A hung backend stalls an investigation/scan **indefinitely**. Inject a timeout-bearing client.

### UI-1 · UI has zero tests + broken lint · M
15 pages + 11 components + non-trivial `lib/{styles,logparse,api}.ts` are untested. `package.json:9` declares `"lint": "next lint"` but there's **no eslint dep/config** and Next 16 removed built-in `next lint` — so no linting runs at all. Add Vitest + React Testing Library, add ESLint (flat config), then gate both in the CI `ui` job (`ci.yml:46-65`).

### DEP-1 · Ship a Helm chart / production manifests · L
Only `deploy/local/k8s/*` exists (dev-flavored). No Helm chart, Kustomize base/overlays, or versioned production manifests — users can't install the agent + control plane into a real cluster without hand-editing local YAML. Add a chart (control plane + agent, values for SSO/DB/backends) and a production install guide (**DOC-4**).

---

## P2 — next

### ENG-1 · Metrics are absent from the correlated timeline · M
`correlator.go:32-39` gathers logs + deployments + k8s events only; `MetricSource` is never queried, so metric anomalies never reach the timeline or ranker — the "correlation engine" ignores one of its three pillars. Wire metric gather + a metric-anomaly hypothesis into `ranker.go`.

### LINK-1 · Wire the built-but-dead watch-event push path · M
Full push infra exists (`Dialer.WithEventFeed`/`pushLoop`, `gateway.dispatchEvent`, `Link.Events()`) but `agent.go:58` never calls `WithEventFeed`, the k8s adapter exposes no watch, and nothing drains `link.Events()` in `registry.go:51`. Result: detection is poll-only (30s ticks) despite streaming infra; pushed signals would fill the 64-buffer and drop. Wire agent watch → gateway → scheduler.

### API-1 · Incident list: pagination + RBAC-filter-after-limit bug · M
`handlers.go:99-113` hardcodes `Limit:100`, no page/cursor. Worse, `filterVisibleIncidents` runs **after** the DB limit, so a scoped viewer can get far fewer than 100 visible incidents even when more exist. Push scope into the store query or paginate post-filter.

### API-2 · Rate limiting (OAuth handshake + investigate) · M
No limiter on any route (`router.go`). `GET /auth/login|callback` each make outbound GitHub calls with no throttle (brute-force surface); `POST /api/v1/investigate` runs a live multi-source gather + persists — an authed operator can hammer it. Add per-IP/per-user limits.

### API-3 · Pod-logs `tail` uncapped + parse error swallowed · S
`handlers.go:345-348` does `tail, _ = strconv.ParseInt(...)` with no upper bound (events endpoint already caps `limit`/`since` at `:717,:726`). `tail=99999999` is a resource-abuse vector. Cap it.

### SEC-2 · Redaction coverage gaps · M
`redact.go:26-41` misses GitHub tokens (`ghp_`/`github_pat_`/`gho_`), Slack (`xox[baprs]-`), GCP SA-JSON keys, generic high-entropy blobs, non-`bearer` API tokens, and IP/phone PII. Application points are correct (logs/configmaps/timeline/LLM prompt) — only pattern coverage is short. Also apply redaction on backend-error bodies (`loki.go:94`, `victoriametrics.go:128`, `argocd.go:158` echo up to 512B to clients/logs — **SEC-4/P3**).

### SEC-3 · Durable session revocation + fresher group state · M
`revocation.go:17` is in-memory per-replica (logout is a no-op across HA replicas); `oauth_handlers.go:142` bakes GitHub groups into an 8h JWT (`session.go:15`) so revoked org/team membership persists until expiry. Add a shared revocation store (Redis/PG) and/or shorter TTL + refresh.

### STORE-1 · Versioned migration tooling · M
`postgres.go:46-63` re-applies every embedded file via `CREATE ... IF NOT EXISTS` each startup with no `schema_migrations` table (single `0001_init.sql`). `ALTER`/backfill/destructive migrations are impossible. Add a versioned migrator.

### STORE-2 · Persist cluster connection state · S–M
`SaveCluster` is only called from seed (`seed.go:20-21`); live agent connect/disconnect and direct-mode clusters never persist — `handleListClusters` unions the registry at read time (`handlers.go:522`), so restart/history/region for real clusters is lost. Persist on connect.

### SRC-2 · Kubernetes List calls are unbounded · M
Every `List` uses empty `ListOptions{}` (no `Limit`/`Continue`): `kubernetes.go:124,172,186,200,265,329,424,813,865`. Large namespaces load all objects in one call. Add pagination.

### SRC-3 · ArgoCD full-fetch fallback + fuzzy attribution · S–M
`argocd.go:132-138` re-fetches **all** applications when search returns 0; `bestMatch` (`:169`) is a name/namespace heuristic that can mis-attribute change events. Bound the fallback, tighten matching.

### ENG-2 · Per-source timeout in Timeline · S
`correlator.go:32-39` calls sources with raw ctx, no `context.WithTimeout` — one slow source delays the whole gather. Add per-source deadlines (complements SRC-1).

### CLI-1 · CLI is a stub (only `version`) · L
`cmd/lotsman/main.go:1-43` is a hand-rolled `flag` scaffold; doc comment promises cobra subcommands for cluster/agent management + running investigations. Investigate is currently curl-only (`Makefile:61`). Build the real cobra CLI (+ **CLI-2/P3**: shared config for endpoint/token/`-o json|table`).

### UI-2 · Auto-refresh / live updates · M
Every page loads once with a manual Refresh button — no `setInterval`/`EventSource`/SSE anywhere in `ui/`, despite the backend SSE incident bus. For a live incident tool, stale incidents/events/pods is a real gap. Wire the SSE stream (or polling) into incidents/events/pods.

### UI-3 · Global error boundary + not-found · S
No `error.tsx`/`global-error.tsx`/`not-found.tsx` in `ui/app`. A render-time exception white-screens. Add boundaries.

### CI-4 · golangci-lint + gate UI lint/test · S
CI has only gofmt + vet. Add golangci-lint/staticcheck; once UI-1 lands, gate `npm run lint` + tests in the `ui` job.

### API-4 · OpenAPI spec · M
No spec anywhere; REST surface in `router.go` is undocumented for clients. Add an OpenAPI doc (and optionally serve it).

### DOC-4 · Production install / operator guide · M
Docs cover local `make` flows + architecture only. Add a "deploy to a real cluster" guide (blocked on DEP-1).

---

## P3 — nice-to-have

- **ENG-3 · Richer ranker heuristics** (M) — `ranker.go:24-61` only emits deploy-before-incident + OOM/evicted; add log-burst / metric-anomaly hypotheses + corroboration confidence.
- **SEC-5 · Deepen SSRF validation** (S) — `config.go:117-137` blocks only link-local IP literals at startup; misses DNS→metadata, IPv4-mapped IPv6, private ranges. Resolve+check at dial time.
- **API-5 · Standardize error/response shapes** (S) — three shapes coexist: `writeError` JSON, `writeJSON(w,401,nil)` empty, `http.Error` text/plain (`middleware.go`, `oauth_handlers.go`). Unify.
- **API-6 · Validate investigate/list inputs** (S) — `handlers.go:192-219` passes empty/garbage `Cluster/Namespace/Kind/Name` straight to the engine; `:101` passes raw `status` unvalidated to the store filter.
- **API-7 · Session sliding expiry / refresh** (S–M) — fixed 8h token hard-logs-out active users (`session.go`).
- **API-8 · CORS config** (S) — none set; a split-origin deploy (`ui_url` ≠ `base_url`) can't make credentialed calls. Document embedded-only or add configurable CORS.
- **SRC-4 · HTTP retry/backoff on transient failures** (S) — loki/vm/argocd do a single `Do`.
- **SRC-5 · client-go QPS/Burst/Timeout tuning** (S) — `kubernetes.go:99` sets none.
- **STORE-3 · Cap `ListIncidents` when `Limit==0`** (S) — `postgres.go:154` unbounded SELECT.
- **UI-4 · Severity badge collapses critical/error** (S) — `styles.ts:89-100` gives critical and error identical red; `eventSeverityStyle` (`:41-52`) differentiates. Align.
- **UI-5 · Accessibility pass** (M) — skip-to-content, table caption/scope, color-only container-status squares (`container-squares.tsx`), automated a11y check.
- **DEP-2 · Harden local k8s manifests** (M) — resource limits, securityContext, NetworkPolicy, pinned tags before users copy them as a template.
- **DOC-3 · ADRs for UI testing + CLI design** (S) — no ADR ≥0009 covering UI-1 or CLI-1 decisions.
- **HYG-1 · `internal/ui/ui.go:19` panics on embed failure** (S) — graceful error instead.
- **HYG-2 · Dead `sources.ErrNotImplemented`** (S) — `sources.go:27` no longer returned by any adapter; referenced only by stub-tolerance tests. Remove.
- **HYG-3 · Split doc comment** (trivial) — `handlers.go:234-260` `maskPodSecrets` doc is bisected by `redactedIncident`.
- **CI-5 · Verify UI export→embed contract in CI** (S) — `ci.yml` builds UI but discards output; the `ui/out`→`internal/ui/dist` embed (Makefile/Dockerfile only) is untested until release.
- **DEPS-1 · Pin protobuf off pseudo-version** (S) — `go.mod` uses `google.golang.org/protobuf v1.36.12-0.20260120…` (a pinned pre-release commit); move to a tagged release.

---

## Suggested worktree execution waves

Grouped so items in a wave touch mostly disjoint files (safe to run as parallel worktrees); later waves depend on earlier ones.

**Wave 0 — docs & CI (low-risk, unblocks trust):** DOC-1, CI-1, CI-2, CI-3, CI-4, DEPS-1.

**Wave 1 — backend hardening (parallel-safe, mostly separate packages):** SRC-1 + ENG-2 (timeouts), SEC-1 (agentlink), API-1/API-2/API-3 (api), STORE-1/STORE-2 (store), SEC-2 (redact).

**Wave 2 — features:** ENG-1 (metrics in timeline) → then LINK-1 (watch push, depends on ENG/scheduler), UI-1 (tests+eslint) → UI-2/UI-3, CLI-1, DEP-1 → DOC-4, API-4.

**Wave 3 — polish:** all P3 items, opportunistically.
