---
title: "Source-Agnostic Adapters"
type: concept
tags: [concept, architecture, adapters]
created: 2026-06-21 13:30:00
updated: 2026-07-11 17:58:00
status: current
aliases: ["Sources", "Provider"]
---

# Source-Agnostic Adapters

## Overview

The seam that makes Lotsman cloud/environment-agnostic: four neutral Go interfaces — `LogSource`, `MetricSource`, `DeploymentSource`, `ClusterSource` — bundled per cluster as a `Provider`. The [[Correlation Engine]] depends ONLY on these interfaces, never on Loki/VictoriaMetrics/ArgoCD/Kubernetes directly (ADR-0003). All four concrete adapters are now implemented and production-capable; see [[Feature Source Adapter Implementation 2026-06-21]] for the implementation report.

## Interfaces (`internal/sources/sources.go`)

| Interface | Method(s) | Adapter package |
|---|---|---|
| `LogSource` | `QueryLogs(ctx, ResourceRef, TimeRange, query string) ([]Signal, error)` | `internal/sources/loki` |
| `MetricSource` | `QueryInstant`, `QueryRange` | `internal/sources/victoriametrics` |
| `DeploymentSource` | `ChangeEvents(ctx, ResourceRef, TimeRange) ([]Signal, error)` | `internal/sources/argocd` |
| `ClusterSource` | `Events`, `ListWorkloads`, `ListPods`, `PodLogs`, `ListConfigMaps`, `GetConfigMap`, `ListSecrets`, `GetSecret`, `ListNodes` | `internal/sources/kubernetes` |

## Core Principles

- **Two implementations per interface** — concrete adapters live in the agent (`internal/sources/{loki,victoriametrics,argocd,kubernetes}`); a remote proxy (`internal/sources/remote`) marshals the same calls over the agent link. The [[Correlation Engine]] cannot tell which it is using. See [[Agent Control Plane Topology]].
- **Query-through, not ingest** — telemetry is queried live; only derived state (incidents, change history, clusters, config) is persisted (ADR-0004).
- **Adding a source method touches 6 places in lockstep** — the interface (`internal/sources/sources.go`), the concrete adapter, the remote proxy, the agent `handle` (`internal/agent/agent.go`), `agentlink.RequestKind`, and `proto/lotsman.proto`.
- **Backend types must never escape their package** — no Loki/VictoriaMetrics/ArgoCD/Kubernetes client types in `internal/engine`, `internal/api`, or `internal/model`.

## Concrete Adapter Details

### Loki (`internal/sources/loki/loki.go`)

- `GET /loki/api/v1/query_range`; synthesizes `{namespace="…",app="…",pod="…"}` LogQL selector when no query override is given.
- Decodes the Loki `streams` envelope; maps to `Signal{Kind: SignalLog}` via `model.ResourceFromLabels`; nanosecond timestamps; default cap 1000.
- Severity from `level` or `detected_level` label.
- **Stdlib HTTP only** — no external dependency.

### VictoriaMetrics (`internal/sources/victoriametrics/victoriametrics.go`)

- `QueryInstant` → `GET /api/v1/query` (vector); `QueryRange` → `GET /api/v1/query_range` (matrix).
- Decodes the standard Prometheus envelope `{status, data: {resultType, result}}`; float-second timestamps; `strconv.ParseFloat` for values.
- Prometheus is a drop-in alternative — same HTTP API surface.
- **Stdlib HTTP only** — no external dependency.

### ArgoCD (`internal/sources/argocd/argocd.go`)

- `GET /api/v1/applications` to list apps; application history is on the Application object at `.status.history` — there is **no separate `/history` endpoint**.
- Matches by exact resource name, then falls back to destination namespace; no match returns `(nil, nil)`.
- Syncs within the `TimeRange` mapped to `Signal{Kind: SignalChange}` with `ChangeRef`.
- Bearer auth; empty token omits the header.
- **Stdlib HTTP only** — no external dependency.

### Kubernetes (`internal/sources/kubernetes/kubernetes.go`)

- **Lazy, non-failing constructor** `New(cluster, kubeconfigPath)` — never returns an error so `engine_test.go` stays green; clientset built on first call via `rest.InClusterConfig()` or kubeconfig, then stored as `kubernetes.Interface`.
- Test seam: `newWithClient(client kubernetes.Interface)` accepts `fake.NewSimpleClientset`.
- **Direct List calls only** — the `ClusterSource` interface has no lifecycle hook, so informer-based watches are not used. `Events` lists and filters by `TimeRange`; `ListWorkloads` returns Deployments, StatefulSets, and DaemonSets.
- **Pod inspection** — `ListPods` lists pods in a namespace (optionally filtered by workload label) and returns `[]model.Pod` with container and env var details; the Pod owner-ref chain is resolved (Pod → ReplicaSet → Deployment/StatefulSet) so env vars carry owning-workload attribution. `PodLogs` fetches container stdout via the apiserver's logs endpoint, capped at 1 MiB with a `Truncated` flag; default tail is 200 lines. Both methods flow through the same direct/remote seam as all other `ClusterSource` methods (see [[Feature Pod Inspection 2026-06-21]]).
- **ConfigMaps** — `ListConfigMaps(ctx, cluster, namespace)` and `GetConfigMap(ctx, cluster, namespace, name)` return full `Data map[string]string`. ConfigMaps are not secret-gated.
- **Secrets** — `ListSecrets` / `GetSecret` gate `Data` bytes behind the `Reveal` flag (`LOTSMAN_ALLOW_ENV_REVEAL=1` on the agent + admin caller); metadata is always returned. For secrets of type `kubernetes.io/tls`, `tls.crt` PEM bytes are parsed by `x509.ParseCertificate` to produce a `model.CertInfo` struct (CN, Issuer, NotBefore/NotAfter, DNS SANs, SerialNumber, ExpiryStatus) — cert metadata is always public regardless of the reveal flag.
- **Nodes** — `ListNodes(ctx, cluster)` returns `[]model.Node` with Status, Roles (from `node-role.kubernetes.io/<role>` labels), KubeletVersion, OS/Arch, InternalIP, Capacity, and Allocatable from `node.Status`.
- **Env var reveal** — when the `Reveal` flag is set (requires `LOTSMAN_ALLOW_ENV_REVEAL=1` in the agent environment and admin role on the caller), `ListPods` resolves `valueFrom` references (Secret/ConfigMap) to actual values with provenance metadata. Non-admin callers receive all literal values masked and `valueFrom` shown as unresolved reference chips. The same gate applies to `GetSecret` values. See [[Authentication and RBAC]].
- **Severity escalation** — Kubernetes `Warning` events with critical reasons (OOMKilled, CrashLoopBackOff, BackOff, FailedScheduling, FailedMount, Evicted, Unhealthy, ImagePullBackOff, …) are escalated to `SeverityError`; the engine detector in `internal/engine/detector/kubernetes.go` gates candidates at `>= SeverityError`.
- **Dependency added:** `k8s.io/client-go v0.36.2` (+ `k8s.io/api`, `k8s.io/apimachinery`). The 3 HTTP adapters remain stdlib-only.
- **QPS/Burst/Timeout tuned** (2026-07-11 campaign) — `client-go`'s rest config previously left these unset (library defaults); now tuned explicitly to avoid client-side throttling surprises under load.
- **Watch is still poll-based** — the agent's Kubernetes watch-event feed (used by the now-wired push path, see [[Agent Control Plane Topology]]) polls rather than using a `client-go` informer; a true informer is future work.

### HTTP Adapter Hardening (2026-07-11 campaign)

The three stdlib-HTTP adapters (loki, victoriametrics, argocd) previously used `http.DefaultClient` with no timeout and a single `Do` call with no retry. Both gaps were closed: each adapter is now constructed with a timeout-bearing `*http.Client`, and transient failures are retried with backoff. See [[Backlog Improvement Campaign Waves 0-3 2026-07-11]].

## Dependency Boundary

| Adapter | External dep | Note |
|---|---|---|
| loki | none | stdlib HTTP |
| victoriametrics | none | stdlib HTTP |
| argocd | none | stdlib HTTP |
| kubernetes | `k8s.io/client-go v0.36.2` | `Dockerfile` `COPY go.sum` uncommented; `go` directive `1.26.0` |

`go build ./...` remains green offline for the three HTTP adapters; client-go requires network access only at `go mod download` time.

## Relationships & Context

- **Parent concept:** [[Lotsman]]
- **Related:** [[Agent Control Plane Topology]], [[Correlation Engine]], [[Persistence and State]]
- **Relevant skills:** `grafana/skills@loki`, `grafana/skills@promql` — see [[Development Skills]]
- **Implementation reports:** [[Feature Source Adapter Implementation 2026-06-21]], [[Feature Pod Inspection 2026-06-21]], [[Feature Kubernetes Resource Inspection 2026-06-22]]
- **Improve pass report:** [[Improve Engine Hardening and CVE Remediation 2026-06-24]] (remote proxy JSON round-trip + error-propagation + empty-payload tests added)
- **Backlog campaign report:** [[Backlog Improvement Campaign Waves 0-3 2026-07-11]] (HTTP client timeouts + retry/backoff; client-go QPS/Burst/Timeout tuning)
- **Sources:** `internal/sources/sources.go`, `docs/adr/0003-source-agnostic-adapters.md`, `docs/adr/0004-query-through-telemetry.md`
