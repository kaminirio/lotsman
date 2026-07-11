---
title: "Backlog Improvement Campaign Waves 0-3 2026-07-11"
type: report
tags: [report, improve, feature, ci, security, api, store, ui, cli, deploy]
created: 2026-07-11 17:58:00
updated: 2026-07-11 17:58:00
status: final
flow: improve
---

# Backlog Improvement Campaign Waves 0-3 2026-07-11

## Summary

A full-repo assessment on 2026-07-11 found that the docs (README, `CONTRIBUTING.md`, `docs/ARCHITECTURE.md` §12) still described Lotsman as a `TODO(impl)`, std-lib-only stubbed scaffold, while the adapters, Postgres store, gRPC agent link, scheduler, and auth/RBAC were in fact fully implemented and tested. A prioritized backlog of ~39 items (`BACKLOG.md` at the repo root) was created, grouped into four execution waves, and run to completion in worktree isolation: **Wave 0** (docs + CI hardening), **Wave 1** (backend hardening — timeouts, fail-closed agent auth, rate limiting, pagination, versioned migrations), **Wave 2** (features — metrics-in-timeline, wired watch-push, UI tests/live updates, cobra CLI, Helm chart, OpenAPI spec), and **Wave 3** (polish — retry/backoff, SSRF hardening, standardized errors, sliding sessions, CORS, a11y, two new ADRs). Every wave was gated on `gofmt`/`go vet`/`go build`/`go test -race`/`golangci-lint` (all green: 20 Go packages, ~176 auth/api tests, UI 62 tests) plus a code-review pass; deployment was validated on both docker-compose and a k3d single-cluster agent deploy.

## Details

### Context

`BACKLOG.md` root-caused the doc/reality gap: every subsystem the docs claimed was stubbed (`internal/sources/kubernetes`, `internal/sources/{loki,victoriametrics,argocd}`, `internal/store/postgres.go`, `internal/agentlink`, `internal/controlplane/scheduler.go`, `internal/auth`) was real, wired, and tested on `main`. The backlog prioritized ~39 items as P1/P2/P3 with S/M/L effort estimates, then grouped them into four waves designed so items in the same wave touch mostly disjoint files (safe for parallel worktree execution); later waves depend on earlier ones.

### Wave 0 — docs & CI (low-risk, unblocks trust)

- **DOC-1** — rewrote the stale "stubbed scaffold / std-lib-only" narrative in `README.md`, `CONTRIBUTING.md`, and `docs/ARCHITECTURE.md` §12 to reflect what's actually implemented vs. genuinely open.
- **CI-1** — added `govulncheck ./...`, `npm audit`, and a Trivy image scan to CI.
- **CI-2** — `go test ./...` now runs with `-race` plus coverage reporting.
- **CI-3** — Node parity fixed: CI now builds the UI on Node 26 (was 22), matching the Dockerfile.
- **CI-4** — added golangci-lint with a new `.golangci.yml` config.
- **DEPS-1** — evaluated and **left as-is**: the `google.golang.org/protobuf` pseudo-version pin is required because `k8s.io/client-go v0.36.2` depends on it; there is no tagged release to move to yet.

### Wave 1 — backend hardening

- **SRC-1 / ENG-2** — HTTP timeouts on the Loki/VictoriaMetrics/ArgoCD adapters (previously `http.DefaultClient`, no timeout) plus a per-source `context.WithTimeout` in `Correlator.Timeline` (`internal/engine/correlator.go`) so one slow source can no longer stall an entire investigation/scan.
- **SEC-1** — `internal/agentlink` fail-closed on an empty `LOTSMAN_AGENT_TOKEN`: the previous dev fallback accepted *any* non-empty token from a connecting agent. Fail-closed is now the default; `LOTSMAN_AGENT_ALLOW_INSECURE` is the explicit opt-in for local dev without a token. **mTLS (per-cluster agent identity) remains a TODO** — the transport is still `insecure.NewCredentials()`.
- **API-1** — incident-list pagination added, and the RBAC-filter-after-limit bug fixed: `filterVisibleIncidents` previously ran *after* the DB `Limit`, so a scoped viewer could see far fewer than the requested count even when more visible incidents existed. Fix pushes scope earlier and adds a bounded scan cap to prevent unbounded query cost.
- **API-2** — per-IP token-bucket rate limiting added on the OAuth handshake (`/auth/login`, `/auth/callback`) and `POST /api/v1/investigate`, closing a brute-force/hammer surface that previously had no limiter anywhere in `router.go`.
- **API-3** — pod-logs `tail` parameter capped (was parsed with no upper bound — a `tail=99999999` resource-abuse vector).
- **STORE-1** — a versioned Postgres migrator replaces the old "re-apply every embedded `CREATE ... IF NOT EXISTS` file on every startup" scheme: a `schema_migrations` table tracks applied versions, guarded by `pg_advisory_lock` so concurrent replica startups can't race the migration.
- **STORE-2** — cluster connection state is now persisted (`SaveCluster`) on real agent connect, not only from the seed path — restart/history/region for real clusters is no longer lost.
- **SEC-2** — expanded secret/PII redaction patterns in `internal/redact` (GitHub tokens, Slack tokens, GCP SA-JSON keys, broader token/PII coverage beyond the original pattern set).

### Wave 2 — features

- **ENG-1** — metrics are now gathered into the correlated timeline (previously `MetricSource` was never queried by the correlator, so metric anomalies never reached the ranker); a metric-anomaly hypothesis was added to `internal/engine/ranker.go`.
- **LINK-1** — the built-but-dead agent→control-plane watch-event push path (`Dialer.WithEventFeed`/`pushLoop`, `gateway.dispatchEvent`, `Link.Events()`) is now wired end to end: the agent drains a Kubernetes **poll-feed** (not a true `client-go` informer) and pushes events to the gateway, which the scheduler now drains from `registry.go`. Detection is no longer poll-only at the scheduler tick; pushed signals reach the bus without waiting for the next 30 s scan.
- **UI-1 / UI-2 / UI-3** — Vitest + React Testing Library and a flat-config ESLint were added to the UI (previously zero tests and a declared-but-nonfunctional lint script); SSE-based live incident updates and polling were wired into the incidents/events/pods pages (previously manual-refresh-only); global error boundaries (`error.tsx`/`global-error.tsx`/`not-found.tsx`) were added.
- **CLI-1** — the CLI was rebuilt on cobra (previously a hand-rolled `flag` scaffold exposing only `version`); see ADR-0010.
- **DEP-1 / DOC-4** — a Helm chart (control plane + agent, values for SSO/DB/backends) and an `INSTALL.md` production-install guide were added; previously only dev-flavored `deploy/local/k8s/*` manifests existed.
- **API-4** — an OpenAPI 3.1 spec is now served at `GET /api/v1/openapi.yaml`; the REST surface was previously undocumented for API clients.

### Wave 3 — polish

- **SRC-4** — retry/backoff added to the Loki/VictoriaMetrics/ArgoCD adapters (previously a single `Do` with no retry on transient failure).
- **SRC-5** — `client-go` QPS/Burst/Timeout tuned explicitly (previously unset, i.e. client-go defaults).
- **STORE-3** — `ListIncidents` capped when `Limit == 0` (was an unbounded `SELECT`).
- **ENG-3** — a log-burst ranker hypothesis added alongside the existing deploy-before-incident and OOM/evicted heuristics.
- **SEC-5** — deepened SSRF validation in `internal/config` (previously only link-local IP literals were blocked at startup; DNS→metadata, IPv4-mapped IPv6, and private-range resolution-at-dial-time gaps closed).
- **HYG-1** — `internal/ui/ui.go` no longer panics on an embed failure; fails gracefully instead.
- **API-5** — the three coexisting error/response shapes (`writeError` JSON, empty `writeJSON(w,401,nil)`, `http.Error` text/plain) unified to one standard error shape.
- **API-6** — input validation added on `/investigate` and list endpoints (previously empty/garbage `Cluster/Namespace/Kind/Name`/`status` values passed straight through to the engine/store).
- **API-7** — sliding session expiry replaces the fixed 8h hard-logout: sessions extend on activity, with lineage-based revocation and a 24h absolute cap so a session can't be extended indefinitely.
- **API-8** — CORS made opt-in/configurable (previously unset, blocking any split-origin UI/API deploy from making credentialed calls).
- **UI-4 / UI-5** — severity badge collapse fixed (critical vs. error now visually distinct, matching `eventSeverityStyle`) and an accessibility pass (skip-to-content, table caption/scope, non-color-only status indicators); new tests added for the `use-live` hook.
- **DEP-2** — dev k8s manifests hardened (resource limits, `securityContext`, `NetworkPolicy`, pinned tags) so they're no longer a risky copy-paste template.
- **CI-5** — a CI job now verifies the UI export → `internal/ui/dist` embed contract (previously untested until release).
- **DOC-3** — two new ADRs added: `docs/adr/0009-ui-testing-and-lint.md` and `docs/adr/0010-cli-cobra.md`. Remaining stale security claims in other ADRs and `deploy/local/k8s/README.md` were also corrected.

### Verification

Every wave was gated on `gofmt`, `go vet`, `go build ./...`, `go test ./... -race`, and `golangci-lint` — all green across 20 Go packages, with ~176 auth/api tests and 62 UI tests passing. Each wave also went through a code-review pass; findings that were confirmed blockers were fixed before the wave closed:

| Finding | Resolution |
|---|---|
| NaN value could reach persisted metrics, dropping the incident silently on save | Fixed — value validated/guarded before persistence |
| An incident-query path remained unbounded despite API-1's pagination work | Fixed — bounded scan cap added |
| The new versioned migrator could crash-loop under concurrent replica startup | Fixed — `pg_advisory_lock` serializes migration application |
| The sliding-session revocation design had an escape allowing a revoked session lineage to keep sliding | Fixed — revocation check applied at every slide, not just at issuance |

Deployment was validated on two environments:

- **docker-compose full stack** — migrations apply cleanly, pagination and rate-limit (`429`) behavior verified, SSE stream verified, the new OpenAPI route serves, `/investigate` verified end to end.
- **k3d single-cluster agent deploy** — validated agent dial-out, fail-closed token auth (SEC-1), and the real Kubernetes watch-push path (LINK-1) turning live crash-loop events into incidents without waiting for the next poll tick.

## Caveats & open items

- **Agent link mTLS (ADR-0002)** — still a TODO; the gRPC transport remains `insecure.NewCredentials()` with only the (now fail-closed) shared bearer token for identity. Per-cluster certs are the next step.
- **Watch path is a poll-feed, not a true informer** — LINK-1 wires the push infrastructure end to end, but the agent side polls Kubernetes rather than using a `client-go` informer/watch. A true informer is future work.
- **Session revocation store is in-memory per-replica** — API-7's sliding-session lineage fix closes the escape found in review, but durable/HA revocation (Redis/PG-backed, shared across replicas) is still open (this was SEC-3 in the backlog, not fully closed by this campaign).
- **Trivy release scan is report-only** — CI-1 added the scan, but it doesn't yet gate the build/push step; decoupling build/push from the scan result is deferred.
- Changes are merged to the working tree in the campaign's worktree; not committed by this agent — the user reviews and commits.

## Related

* Affected concepts: [[Agent Control Plane Topology]], [[Authentication and RBAC]], [[Source-Agnostic Adapters]], [[Correlation Engine]], [[Persistence and State]], [[UI Design System]]
* Project: [[Lotsman]]
* Prior improve pass: [[Improve Engine Hardening and CVE Remediation 2026-06-24]]
