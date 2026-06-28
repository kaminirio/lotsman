---
title: "Feature Kubernetes Resource Inspection 2026-06-22"
type: report
tags: [report, feature, kubernetes, grpc, security, rbac, adapters]
created: 2026-06-22 14:31:13
updated: 2026-06-22 14:31:13
status: final
flow: feature
---

# Feature Kubernetes Resource Inspection 2026-06-22

## Summary

ConfigMaps, Secrets, Nodes, and extended Pod metadata were added to the Lotsman source seam end-to-end: each resource type went through all six lockstep locations (interface, kubernetes adapter, remote proxy, agent handle, `agentlink.RequestKind`, proto regen), new REST endpoints were added under `/api/v1/clusters/{cluster}/...`, and everything was validated live on a local k3d cluster through the in-cluster agent over real gRPC. Secret-value access is opt-in and admin-gated; x509 TLS certificate metadata is extracted from PEM secrets and always public. This builds directly on the pod and env-var inspection introduced in [[Feature Pod Inspection 2026-06-21]].

## Details

### New ClusterSource methods (`internal/sources/sources.go`)

Each method was carried through all six lockstep places. See [[Source-Agnostic Adapters]] for the invariant.

| Method | Return type | Notes |
|---|---|---|
| `ListConfigMaps(ctx, cluster, namespace)` | `[]model.ConfigMap, error` | All configmaps in namespace |
| `GetConfigMap(ctx, cluster, namespace, name)` | `*model.ConfigMap, error` | Single configmap with full data |
| `ListSecrets(ctx, cluster, namespace)` | `[]model.Secret, error` | Metadata only; values are gated |
| `GetSecret(ctx, cluster, namespace, name)` | `*model.Secret, error` | Values under reveal gate; cert metadata always returned |
| `ListNodes(ctx, cluster)` | `[]model.Node, error` | Cluster-wide; no namespace |

### New model types (`internal/model/`)

| Type | Fields |
|---|---|
| `ConfigMap` | Cluster/Namespace/Name + `Data map[string]string` + `CreatedAt` |
| `Secret` | Cluster/Namespace/Name + `Type` + `Data map[string][]byte` (values gated) + `Cert *CertInfo` |
| `CertInfo` | CN, Issuer, NotBefore, NotAfter, DNSSANs, SerialNumber, ExpiryStatus (Valid/Expiring/Expired) |
| `Node` | Name + Status + Roles + KubeletVersion + OS + Arch + InternalIP + Capacity + Allocatable + Age |

### Extended Pod model (`internal/model/pod.go`)

`Pod` was extended with:
- Phase, Ready flag, RestartCount, NodeName (was already present).
- Container list now carries image details.
- `OwnerRef` chain resolved: Pod → ReplicaSet → Deployment/StatefulSet, so env vars can be attributed to the owning workload rather than just the pod.
- Inline (`value:`) env vars are now attributed to their owning workload (Deployment/StatefulSet) as the env `source` alongside `secretKeyRef`/`configMapKeyRef` valueFrom references.

### Kubernetes adapter (`internal/sources/kubernetes/kubernetes.go`)

- `ListConfigMaps` / `GetConfigMap` — `client.CoreV1().ConfigMaps(ns)` list/get. Full `Data` map returned; no gating (configmaps are considered non-secret).
- `ListSecrets` / `GetSecret` — `client.CoreV1().Secrets(ns)` list/get. Values (`Data` bytes) are only populated when `Reveal` is true AND the agent env `LOTSMAN_ALLOW_ENV_REVEAL=1` is set. For secrets of type `kubernetes.io/tls`, PEM bytes in `tls.crt` are parsed (`x509.ParseCertificate`) to produce a `CertInfo` struct regardless of the reveal flag — cert metadata is always public.
- `ListNodes` — `client.CoreV1().Nodes().List`; returns capacity/allocatable from `node.Status.Capacity` / `node.Status.Allocatable`; roles from node label `node-role.kubernetes.io/<role>`.

### Six-places lockstep changes

| Place | Change |
|---|---|
| `internal/sources/sources.go` | 5 new method signatures on `ClusterSource` |
| `internal/sources/kubernetes/kubernetes.go` | Concrete implementations for all 5 methods |
| `internal/sources/remote/remote.go` | Remote proxy marshalling for all 5 over agent gRPC link |
| `internal/agent/agent.go` (`handle`) | Dispatches 5 new `RequestKind`s to local provider |
| `internal/agentlink/kind.go` | Added `LIST_CONFIGMAPS`, `GET_CONFIGMAP`, `LIST_SECRETS`, `GET_SECRET`, `LIST_NODES` |
| `proto/lotsman.proto` | Proto regenerated; request/response messages for all 5 added; pb output in `internal/agentlink/pb/` |

### REST API (`internal/api/`)

All routes are under `/api/v1/clusters/{cluster}/...` and RBAC-gated by `CanView` on the target cluster and namespace. The `api.Config.Sources` field was refactored from a direct `sources.Provider` to a small registry interface (`Provider(cluster string) (sources.Provider, error)` + `Clusters() []string`) implemented by `controlplane.Registry` — this removes the direct-mode special-case and lets the API layer be cluster-agnostic.

| Method + Path | Description |
|---|---|
| `GET /api/v1/clusters/{cluster}/namespaces/{ns}/pods` | List pods (phase/ready/restarts/node/containers); `?workload=` filter |
| `GET /api/v1/clusters/{cluster}/namespaces/{ns}/pods/{pod}/logs` | Container stdout; `?container=&tail=`; 1 MiB cap |
| `GET /api/v1/clusters/{cluster}/namespaces/{ns}/configmaps` | List configmaps |
| `GET /api/v1/clusters/{cluster}/namespaces/{ns}/configmaps/{name}` | Get single configmap with data |
| `GET /api/v1/clusters/{cluster}/namespaces/{ns}/secrets` | List secrets (metadata only unless reveal gated) |
| `GET /api/v1/clusters/{cluster}/namespaces/{ns}/secrets/{name}` | Get single secret (values under reveal gate; cert always) |
| `GET /api/v1/clusters/{cluster}/nodes` | List nodes (cluster-wide) |
| `GET /api/v1/clusters/{cluster}/namespaces/{ns}/workloads` | List workloads (Deployments/StatefulSets/DaemonSets) |
| `GET /api/v1/clusters/{cluster}/namespaces/{ns}/events` | List events |
| `GET /api/v1/clusters` | List connected clusters (merges live registry) |

Namespace sentinel: `_all` is accepted on list routes to query across all namespaces.

### Secret reveal and RBAC gate (security model)

The env-var/secret reveal model introduced in [[Feature Pod Inspection 2026-06-21]] was extended consistently to secrets:

- **Admin + `LOTSMAN_ALLOW_ENV_REVEAL=1` on agent + `deploy/local/k8s/21-agent-rbac-reveal.yaml` RBAC overlay applied**: secret values returned.
- **Otherwise**: `Data` is nil (metadata and cert info only).
- **Certificate metadata**: always returned, even without reveal. `CertInfo` includes CN, issuer, NotBefore/NotAfter, DNS SANs, serial number, and an `ExpiryStatus` badge (`Valid` / `Expiring` within 30 days / `Expired`).

### Verification (live k3d)

All endpoints validated against a local k3d cluster (`local`) with agent connected over gRPC:

- Listed configmaps in `kube-system`; retrieved `coredns` configmap with full `Data`.
- Listed secrets in `default`; TLS secret returned `CertInfo` without reveal; full PEM values returned with reveal enabled.
- Listed all nodes; capacity/allocatable fields matched `kubectl describe node`.
- Owner-chain resolution confirmed: pod env vars attributed to parent Deployment.

## Caveats & open items

- The opt-in RBAC overlay (`21-agent-rbac-reveal.yaml`) is applied manually; no Helm toggle exists yet.
- `GetSecret` value reveal is admin-only at the API layer but there is no per-secret ACL — any admin can reveal any secret in any namespace.
- Multi-tenant: `CanView` gates all routes but still grants authenticated SSO users global view (per-namespace RBAC isolation deferred; see [[Authentication and RBAC]]).
- Node-level metrics (CPU/memory usage) are not yet joined from VictoriaMetrics into the `Node` model.

## Related

- Affected concepts: [[Source-Agnostic Adapters]], [[Agent Control Plane Topology]], [[Authentication and RBAC]]
- New concept: [[LLM Incident Explainer]]
- Project: [[Lotsman]]
- Prior report: [[Feature Pod Inspection 2026-06-21]]
