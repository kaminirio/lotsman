---
title: "Feature Source Adapter Implementation 2026-06-21"
type: report
tags: [report, adapters, feature, go, kubernetes, loki, victoriametrics, argocd]
created: 2026-06-21 14:35:16
updated: 2026-06-21 14:35:16
status: final
flow: feature
---

# Feature Source Adapter Implementation 2026-06-21

## Summary

All four concrete source adapters in `internal/sources` were implemented, replacing the previous `ErrNotImplemented` stubs. Loki, VictoriaMetrics, ArgoCD, and Kubernetes adapters now fully satisfy the `LogSource`, `MetricSource`, `DeploymentSource`, and `ClusterSource` interfaces (ADR-0003), lighting up **direct mode** against a real cluster. A review-caught contract bug in Kubernetes event severity mapping was also fixed. 66 tests pass across 20 packages; `go vet` and `gofmt` are clean.

## Details

### Loki (`internal/sources/loki/loki.go`)

- `QueryLogs` calls `GET /loki/api/v1/query_range`.
- Synthesizes a `{namespace="…",app="…",pod="…"}` LogQL selector when no query override is supplied.
- Decodes the Loki `streams` response envelope.
- Maps each log entry to `model.Signal{Kind: SignalLog}` via `model.ResourceFromLabels`; nanosecond timestamps; default cap 1000 results.
- Severity derived from `level` or `detected_level` label.
- Stdlib HTTP only — no external dependency added.

### VictoriaMetrics (`internal/sources/victoriametrics/victoriametrics.go`)

- `QueryInstant` → `GET /api/v1/query` (vector result type).
- `QueryRange` → `GET /api/v1/query_range` (matrix result type).
- Decodes the Prometheus envelope `{status, data: {resultType, result}}`.
- Float-second timestamps; values parsed with `strconv.ParseFloat`.
- Prometheus is a drop-in alternative behind the same adapter (same HTTP API surface).
- Stdlib HTTP only — no external dependency added.

### ArgoCD (`internal/sources/argocd/argocd.go`)

- `ChangeEvents` calls `GET /api/v1/applications` to list all applications; there is **no `/history` endpoint** — history lives on the Application object itself at `.status.history`.
- Matches the application owning the requested resource by exact name, then falls back to destination namespace.
- Syncs within the requested `TimeRange` are mapped to `model.Signal{Kind: SignalChange}` with a populated `ChangeRef`.
- Bearer auth; empty token omits the Authorization header.
- No match returns `(nil, nil)` — not an error.
- Stdlib HTTP only — no external dependency added.

### Kubernetes (`internal/sources/kubernetes/kubernetes.go`)

- `ClusterSource` interface has no lifecycle hook, so a **lazy, non-failing constructor** pattern is used: `New(cluster, kubeconfigPath)` never returns an error; the clientset is built on the first call via `rest.InClusterConfig()` or kubeconfig file, then stored.
- Stored as `kubernetes.Interface` to allow a `newWithClient(client kubernetes.Interface)` test seam with `fake.NewSimpleClientset`.
- `Events` — direct List call filtered by `TimeRange`; no informers.
- `ListWorkloads` — lists Deployments, StatefulSets, and DaemonSets via direct List calls.
- **Dependency added:** `k8s.io/client-go v0.36.2` (+ `k8s.io/api`, `k8s.io/apimachinery`). The `COPY go.sum` line in `Dockerfile` was uncommented; `go` directive normalized to `1.26.0`.

### Review-caught contract bug (fixed)

The Kubernetes adapter originally mapped all `Warning` events to `SeverityWarning`. The engine detector at `internal/engine/detector/kubernetes.go` gates incident candidates at `>= SeverityError`, so Warning events could never open an incident. Critical `Warning` reasons (OOMKilled, CrashLoopBackOff, BackOff, FailedScheduling, FailedMount, Evicted, Unhealthy, ImagePullBackOff, and similar) are now escalated to `SeverityError`. A regression test (`fake.NewSimpleClientset` + critical-reason table) locks this in.

### Test coverage

- Loki, VictoriaMetrics, ArgoCD: `httptest` table-driven tests covering response parsing and mapping.
- Kubernetes: `fake.NewSimpleClientset` tests covering severity escalation, time-range filtering, and ref mapping.
- Full suite: **66 tests, 20 packages**; vet/gofmt clean.

### What was NOT needed

The interfaces, `sources/remote` proxy, `internal/agent/agent.go` `handle`, `agentlink.RequestKind` set, and `proto/lotsman.proto` were already complete. This work was purely filling stubs — no "six places in lockstep" churn was required.

## Caveats & open items

- Informer-based watch (streaming events from the apiserver) is not implemented; the Kubernetes adapter uses direct List calls only, consistent with the current `ClusterSource` interface contract.
- The 3 stdlib adapters (Loki, VictoriaMetrics, ArgoCD) have no retry or circuit-breaker logic; those are deferred to a later hardening pass.
- VictoriaMetrics is tested against the Prometheus envelope; compatibility with VictoriaMetrics-specific extensions is unverified.
- `store.Seed` call in `internal/controlplane` is still active; it should be removed once the Postgres store lands.

## Related

- Affected concept: [[Source-Agnostic Adapters]]
- Project: [[Lotsman]]
- Related concepts: [[Correlation Engine]], [[Agent Control Plane Topology]]
