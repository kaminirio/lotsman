---
title: "Feature Multi-Cluster and Mock Removal 2026-06-22"
type: report
tags: [report, feature, multi-cluster, grpc, k3d, seed]
created: 2026-06-22 14:31:17
updated: 2026-06-22 14:31:17
status: final
flow: feature
---

# Feature Multi-Cluster and Mock Removal 2026-06-22

## Summary

Multi-cluster support was validated end-to-end with two real k3d clusters (`local` and `local-2`) each running their own agent; the control plane routes per-cluster queries over separate agent gRPC links and the UI cluster selector shows both. The `LOTSMAN_SEED` config flag (default `true` for local dev, `false` in the cluster deployment) was added so the seeded sample clusters (prod-eu, staging) and sample incident no longer appear in deployed environments. `GET /api/v1/clusters` now merges the live registry rather than returning hard-coded cluster names.

## Details

### Multi-cluster: what changed

#### Registry and Provider resolver

`internal/controlplane/registry.go` already supported multiple cluster entries. The change was:

- `GET /api/v1/clusters` now calls `registry.Clusters()` which returns only clusters currently registered (i.e., with a live agent connection or direct-mode config). Previously it returned a hard-coded list; now only real clusters appear.
- The `api.Config.Sources` field was refactored from a single `sources.Provider` to a registry interface:

```go
type SourceRegistry interface {
    Provider(cluster string) (sources.Provider, error)
    Clusters() []string
}
```

`controlplane.Registry` implements this interface; the API layer calls `Provider(cluster)` per request, enabling per-cluster routing without knowing how many clusters exist.

#### Two-cluster validation (k3d)

- Cluster `local`: k3d cluster with agent deployed as a Pod in `lotsman` namespace, registered under cluster name `local`.
- Cluster `local-2`: separate k3d cluster (`k3d cluster create lotsman2`) with its own agent Pod; registered as `local-2`.
- Both agents dial out to the same control plane address; each opens an independent gRPC bidi stream.
- Control plane maintained two registry entries simultaneously; per-cluster API calls routed to the correct agent.
- Observed: distinct pod lists, node lists, and workload inventories from each cluster via the same UI with cluster selector toggle.

### `LOTSMAN_SEED` config flag

| Env var | Purpose | Default |
|---|---|---|
| `LOTSMAN_SEED` | Enable seeded sample data (sample clusters + sample incident) | `true` |

When `false`, the `store.Seed(st)` call in `internal/controlplane` is skipped and no synthetic data is added. Set to `false` in the cluster deployment manifests (`deploy/local/k8s/`) so that the prod-eu / staging clusters and the seeded ArgoCD incident do not appear alongside real cluster data.

The `store.Seed` call remains in the codebase for local dev experience; the note in CLAUDE.md ("Remove the `store.Seed` call once Postgres is always active") is superseded by `LOTSMAN_SEED=false` — the seed call is now permanently opt-in.

### API: cluster list endpoint

```
GET /api/v1/clusters
-> ["local", "local-2"]
```

Previously returned hard-coded names. Now returns `registry.Clusters()` — only clusters with a live agent gRPC stream (or the direct-mode cluster) are included.

### Verification

- `k3d cluster list` showed `lotsman` and `lotsman2` running.
- Both agents registered; `/api/v1/clusters` returned `["local", "local-2"]`.
- `/api/v1/clusters/local/namespaces/default/pods` and `/api/v1/clusters/local-2/namespaces/default/pods` returned distinct pod lists.
- UI cluster selector displayed both; switching clusters updated all resource tables.
- With `LOTSMAN_SEED=false`, no prod-eu/staging entries or sample incident appeared.

## Caveats & open items

- When an agent disconnects (network interruption), the cluster disappears from the registry immediately. There is no grace period or stale-cache fallback — the UI shows the cluster as gone. A reconnect-with-backoff in the agent handles transient failures, but a brief outage will cause a 404 on in-flight requests.
- The `store.Seed` removal from `internal/controlplane` is now gated on `LOTSMAN_SEED`; the CLAUDE.md open task ("Remove `store.Seed` call once Postgres is always active") is effectively resolved — the call is permanently conditional. The task can be closed.
- No cluster health status endpoint yet — the cluster selector shows connected clusters but does not surface agent version, last-seen time, or agent health.
- mTLS between agents and control plane is still deferred; all gRPC links are insecure.

## Related

- Affected concepts: [[Agent Control Plane Topology]], [[Source-Agnostic Adapters]], [[Persistence and State]]
- Project: [[Lotsman]]
- Feature context: [[Feature Kubernetes Resource Inspection 2026-06-22]], [[Feature Lens UI Redesign 2026-06-22]]
