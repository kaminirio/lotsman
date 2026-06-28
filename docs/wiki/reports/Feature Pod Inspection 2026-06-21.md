---
title: "Feature Pod Inspection 2026-06-21"
type: report
tags: [report, feature, kubernetes, pods, rbac, grpc, security]
created: 2026-06-21 17:47:22
updated: 2026-06-21 17:47:22
status: final
flow: feature
---

# Feature Pod Inspection 2026-06-21

## Summary

Pod inspection was added to Lotsman end-to-end: the API exposes pod listing and container log streaming; applied env vars are shown with permission-driven masking; the full source seam (interface, kubernetes adapter, remote proxy, agent handler, agentlink `RequestKind`, proto) was extended in the required six places. A security audit was run in parallel and three hardening measures were applied. The feature was validated live against a local k3d cluster through the in-cluster agent over real gRPC — real pod logs, real Secret and ConfigMap env var resolution, real provenance chips.

## Details

### New model types (`internal/model/pod.go`)

| Type | Purpose |
|---|---|
| `Pod` | Cluster/namespace/name + containers list + phase/node |
| `Container` | Name + image + env vars |
| `ContainerEnvVar` | Name + value + source (`literal`, `secretKeyRef`, `configMapKeyRef`) + `Revealed bool` |
| `EnvVarSource` | Enum for literal / secretKeyRef / configMapKeyRef |
| `PodLogsResult` | Lines `[]string` + `Truncated bool` + container name |

### Six-places lockstep change for `ClusterSource`

`ClusterSource` in `internal/sources/sources.go` gained two methods:

```go
ListPods(ctx context.Context, cluster, namespace string) ([]model.Pod, error)
PodLogs(ctx context.Context, cluster, namespace, pod, container string, tail int) (*model.PodLogsResult, error)
```

All six lockstep locations were updated:

| Place | Change |
|---|---|
| `internal/sources/sources.go` | Two new method signatures on `ClusterSource` |
| `internal/sources/kubernetes/kubernetes.go` | Concrete: `client.CoreV1().Pods(ns).List` + `GetLogs`; env var expansion reads Secrets/ConfigMaps when `Reveal` flag set |
| `internal/sources/remote/remote.go` | Remote proxy: marshals requests over the agent gRPC link |
| `internal/agent/agent.go` `handle` | Dispatches `LIST_PODS` / `POD_LOGS` request kinds to local provider |
| `internal/agentlink/kind.go` | Added `RequestKindListPods`, `RequestKindPodLogs` |
| `proto/lotsman.proto` | Proto regenerated; `ListPodsRequest/Response`, `PodLogsRequest/Response` messages added |

### API routes

Both routes are under the existing API config (`api.Config.Sources` wired to `engine.ProviderResolver`); both are RBAC-gated by `CanView` on the target cluster/namespace.

| Method + Path | Description |
|---|---|
| `GET /api/v1/clusters/{cluster}/namespaces/{ns}/pods?workload=` | List pods (optionally filtered by workload label) |
| `GET /api/v1/clusters/{cluster}/namespaces/{ns}/pods/{pod}/logs?container=&tail=` | Fetch container stdout; default tail 200 lines; capped at 1 MiB with `truncated: true` flag |

### Permission-driven env-var reveal model

Env vars are exposed under a default-deny model, not a name denylist:

- **Admin** (and anonymous-admin in local dev): literal values returned verbatim; `valueFrom` references (Secret/ConfigMap) are resolved to their actual values by the kubernetes adapter with provenance (`secretKeyRef`/`configMapKeyRef` source chip).
- **Non-admin Viewer/Operator**: ALL literal values are masked (not just values that look like secrets); `valueFrom` references are shown as an unresolved reference chip — the key name and source kind are visible but the value is not fetched.

The `Reveal` flag is carried on the wire request to the agent but the agent honours it only when `LOTSMAN_ALLOW_ENV_REVEAL=1` (default off). This means a compromised or misconfigured control plane cannot force secret disclosure without the agent opt-in.

### Security hardening applied during audit

Three hardening measures were applied during the concurrent security review:

1. **Default-deny masking** — the original implementation used a name denylist (e.g., `PASSWORD`, `SECRET`). Replaced with full literal masking for non-admins so that no literal value is ever returned to a non-admin regardless of naming convention.
2. **Agent `Reveal` flag guard** — the agent ignores the wire `Reveal` flag unless `LOTSMAN_ALLOW_ENV_REVEAL=1` is set in the agent's environment. Cluster-side secrets/configmaps RBAC is therefore never activated unintentionally.
3. **Opt-in RBAC overlay** — the default agent RBAC manifest (`deploy/`) only grants `pods/log` access. A new OPT-IN overlay `deploy/local/k8s/21-agent-rbac-reveal.yaml` grants the cluster-wide `secrets` and `configmaps` read needed for env var resolution. Operators must explicitly apply this overlay to enable env reveal.

### Documented security limitations (must-fix before multi-tenant/prod)

These are known and documented, not silent:

- **SSO-enabled mode makes every authenticated user a global viewer** — no per-namespace isolation yet. A user who can authenticate can list pods and read logs in any namespace.
- **Pod logs are returned unscrubbed** — no secret-pattern scrubbing is applied to log lines. `CanView` gates access but does not sanitize content.
- **Malformed `LOTSMAN_SSO_CONFIG` fails open to anonymous-admin** — a misconfigured SSO environment renders auth disabled, granting full admin to all requesters.

### UI (`/catalog` workloads browser)

A new page `/catalog` was added to the Next.js UI:

- Cluster + namespace picker dropdowns.
- Pods table (name, containers, phase, node).
- Pod detail drawer with two tabs: **Logs** (auto-refreshed tail; truncation warning) and **Env** (table with lock icon + source chip for masked/valueFrom values).
- Static export embedded as `internal/ui/dist/`.

### Verification (live k3d)

Validation was performed against a local k3d cluster with the in-cluster agent connected over real gRPC (not direct mode):

- Listed checkout and guestbook pods in their respective namespaces.
- Resolved `DB_PASSWORD` from a Kubernetes Secret — value returned with `secretKeyRef` provenance chip (admin role).
- Resolved `LOG_LEVEL` from a ConfigMap — value returned with `configMapKeyRef` provenance chip.
- Retrieved real stdout log lines from the checkout container.

All through the agent; the control plane's engine had no direct cluster access.

## Caveats & open items

- The three documented security limitations above are must-fix before any multi-tenant or production deployment (see [[Authentication and RBAC]]).
- Log scrubbing (secret-pattern redaction on log lines) is deferred.
- Per-namespace RBAC isolation for SSO users is deferred (tracked in [[Authentication and RBAC]] open items).
- The opt-in RBAC overlay (`21-agent-rbac-reveal.yaml`) is manual; no Helm value or automated toggle exists yet.
- mTLS for the gRPC agent link remains a deferred production hardening item (see [[Agent Control Plane Topology]]).

## Related

- Affected concepts: [[Source-Agnostic Adapters]], [[Authentication and RBAC]], [[Agent Control Plane Topology]]
- Project: [[Lotsman]]
