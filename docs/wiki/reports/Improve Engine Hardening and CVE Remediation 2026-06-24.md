---
title: "Improve Engine Hardening and CVE Remediation 2026-06-24"
type: report
tags: [report, improve, engine, api, security, performance, testing]
created: 2026-06-24 15:27:46
updated: 2026-06-24 15:27:46
status: final
flow: improve
---

# Improve Engine Hardening and CVE Remediation 2026-06-24

## Summary

A structured `/improve` pass (scan → prioritize → fix → verify → review → document) over the full codebase on 2026-06-24 selected and merged four work bundles: engine-core test coverage (Bundle A), API/server hardening (Bundle B), performance improvements to the hot scan path, bus, and registry (Bundle C), and frontend/backend CVE remediation (Bundle D). A subsequent code-review pass caught one confirmed write-timeout regression (long-response handlers broken by the global 60s `WriteTimeout`) and one docstring/contract mismatch that were both corrected before close. Full test suite: **239 tests pass under `go test ./... -race`**; build/vet/gofmt clean; `npm audit` → 0 issues.

Changes are merged to the working tree; not committed — the user reviews and commits.

## Details

### Bundle A — Engine core test coverage (zero behavior change)

All four additions pin correctness invariants that previously had no automated guard.

| File | What is tested |
|---|---|
| `internal/engine/correlator_test.go` | Partial source failure: a Loki error must not blank metrics/k8s signals (ADR-0008 graceful-degradation invariant). |
| `internal/engine/ranker_boundary_test.go` | Change-window boundary: `dt == ChangeWindow` is **included** (guard is `dt > ChangeWindow`, not `>=`). Multi-change ordering golden test. |
| `internal/store/memory_test.go` | `store.Memory` concurrent read/write under `-race` — no lock gaps. |
| `internal/sources/remote/remote_test.go` | JSON round-trip, error-propagation, and empty-payload paths for the remote proxy. |

The ranker boundary pin is load-bearing: `dt == ChangeWindow` being included is a deliberate policy; changing `>` to `>=` in `internal/engine/ranker.go` would silently drop a boundary candidate. The test makes this visible.

### Bundle B — API / server hardening

| Change | File(s) | Notes |
|---|---|---|
| `ReadTimeout 30s` + `WriteTimeout 60s` on `http.Server` | `internal/api/api.go` | Slow-client protection. See regression note below. |
| `MaxBytesReader` 4 KiB on `POST /investigate` body | `internal/api/handlers.go` | Returns 413 on overflow. |
| `?limit=` capped at 1000 in `handleListEvents` | `internal/api/handlers.go` | Prevents unbounded list scans. |
| `handleListClusters` auth guard + per-cluster RBAC filter | `internal/api/handlers.go` | Transparent in direct/SSO-disabled mode; activates when per-cluster bindings land. See [[Authentication and RBAC]]. |
| Swallowed `SaveIncident` error now logged | `internal/api/handlers.go` | Was silently dropped. |
| GitHub OAuth calls use 10s-timeout `*http.Client` | `internal/auth/auth.go`, `oauth_handlers.go` | Was using `http.DefaultClient` (no timeout). |

**Write-timeout regression — caught and fixed in review:** the global `WriteTimeout: 60s` broke three handlers with legitimately long or streaming responses: `handleInvestigate` (live multi-source gather), `handleExplainIncident` (the Ollama explainer's own budget is 90s), and `handleGetPodLogs`. Fix: a shared `clearWriteDeadline(w http.ResponseWriter)` helper was extracted in `internal/api/respond.go`; it calls `rc.SetWriteDeadline(time.Time{})` via the `http.ResponseController` and is applied to all four long/streaming handlers (including the SSE stream handler). The 60s cap remains in force for all fast GET endpoints.

### Bundle C — Performance: hot scan path, bus, registry

These changes alter observable behavior and are reflected in the updated concept notes.

**`Engine.ScanAndInvestigate` (new hot-path method) — `internal/engine/engine.go`**

The previous scheduler tick performed two source-provider resolutions and two calls into the detector layer per cluster per tick. The new `ScanAndInvestigate` method resolves the provider once, detects once, and investigates each candidate against the same resolved provider in a single pass:

```go
// Old scheduler hot path (two resolutions):
candidates := engine.Scan(ctx, cluster)
engine.Investigate(ctx, cluster, candidates)

// New single method:
incidents, err := engine.ScanAndInvestigate(ctx, cluster)
```

The scheduler's `scanner` interface was collapsed to expose only `ScanAndInvestigate`. The public `Scan` and `Investigate` methods are preserved as part of the API surface (used in tests and the on-demand investigation path via `POST /api/v1/incidents/:id/investigate`).

**Contract fix (caught in review):** the initial `ScanAndInvestigate` docstring promised partial incidents on `ctx.Cancel` but the only caller discarded them. Fixed: the method now returns `nil, ctx.Err()` on context cancellation; the docstring matches. This is safe under the query-through persistence model — the next tick re-scans from scratch.

**`IncidentBus.Publish` — `internal/events/bus.go`**

Previous `Publish` sent to all subscriber channels while holding the subscriber-list lock, which meant a blocked subscriber goroutine would stall detection. Fix: `Publish` now snapshots the subscriber slice under the lock and releases it before sending. A per-subscriber mutex + closed-flag gate prevents send-on-closed-channel panics when a subscriber disconnects mid-publish.

**`Memory.GetIncident` / `ListIncidents` — `internal/store/memory.go`**

- `GetIncident` returns a struct-header copy (shallow copy — Timeline/Hypotheses slices still alias the stored value; comment updated to state the header-only guarantee accurately rather than overpromising deep copy).
- `ListIncidents` skips the full sort when `Limit == 1`.

**`Registry.Provider` memoization — `internal/controlplane/registry.go`**

The per-cluster `remote.Provider` wrapper is now memoized: it is built once on first access (or rebuilt on reconnect) and evicted on disconnect, rather than being reconstructed on every engine tick. This is relevant under multi-cluster operation where the registry is called once per cluster per scan cycle.

### Bundle D — CVE remediation

| Item | Files | Detail |
|---|---|---|
| `postcss` forced to ≥8.5.10 via `overrides` | `ui/package.json`, `ui/package-lock.json` | Fixes GHSA-qx2v-qp2m-jg93 (XSS). `npm audit`: 2 moderate → 0. |
| `golang.org/x/net` bumped to `v0.55.0` | `go.mod`, `go.sum` | Fixes CVE-2026-25680. |
| `make audit` target added | `Makefile` | Opt-in: runs `govulncheck ./...` + `npm audit --prefix ui`. Not part of the default `make` target. |

## Code-Review Findings and Resolutions

| Finding | Severity | Resolution |
|---|---|---|
| `WriteTimeout 60s` broke `handleInvestigate`, `handleExplainIncident`, `handleGetPodLogs`, SSE stream | Confirmed blocker | Fixed: `clearWriteDeadline(w)` helper in `internal/api/respond.go`; applied to all four handlers. |
| `ScanAndInvestigate` contract mismatch (docstring claimed partial return on cancel, caller discarded) | Confirmed contract bug | Fixed: returns `nil, ctx.Err()` on cancel; docstring updated. |
| `Memory.GetIncident` comment overstated deep-copy guarantee | Plausible/minor | Softened: comment now states header-only shallow-copy guarantee accurately. |

## Deferred / Accepted Trade-offs

- **Bus snapshot alloc per publish** and **SSE per-event buffer alloc** — deliberate trade-offs vs the bugs they fixed (blocked-subscriber stall; send-on-closed-channel panic). Not regressed.
- **`engine.Scan` is now only test-referenced** — kept as public API surface for the on-demand path and future use; not removed.
- **`handleListClusters` per-cluster RBAC filter is currently a no-op** under the global-only RBAC policy but sits at the correct architectural layer and will activate when per-cluster bindings land (requires the Postgres store).

## Caveats and Open Items

The following are deliberately deferred (documented §12 architectural work, out of scope for a quick improve pass):

- **gRPC agent-link mTLS (ADR-0002)** — the link is currently plaintext with insecure credentials; token enrollment accepts any non-empty token. See [[Agent Control Plane Topology]] §caveats.
- **Per-cluster RBAC bindings** — the current `Enforcer` grants global-viewer to every authenticated non-admin user. Per-cluster and per-namespace isolation requires the Postgres store (`bindings` table). See [[Authentication and RBAC]] §Open Items.
- **`internal/ui/dist/index.html` change** in the working tree is from an earlier `make ui-build` during stack validation — not part of this improve pass.

## Related

- Affected concepts: [[Correlation Engine]], [[Agent Control Plane Topology]], [[Source-Agnostic Adapters]], [[Authentication and RBAC]]
- Project: [[Lotsman]]
