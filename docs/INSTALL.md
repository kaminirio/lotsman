# Production install guide

How to deploy Lotsman to a real Kubernetes cluster with the Helm chart under
[`deploy/helm/lotsman`](../deploy/helm/lotsman). For local development use the
`make` flows in the [README](../README.md#quickstart) instead — this guide is for
operators standing up a durable, multi-cluster deployment.

- [Architecture recap](#architecture-recap)
- [Prerequisites](#prerequisites)
- [Generate secrets](#generate-secrets)
- [Install: direct (agentless) mode](#install-direct-agentless-mode)
- [Install: agent mode](#install-agent-mode)
- [Add an agent from another cluster](#add-an-agent-from-another-cluster)
- [External Postgres](#external-postgres)
- [SSO / OAuth](#sso--oauth)
- [Exposing the UI (Ingress)](#exposing-the-ui-ingress)
- [Production hardening notes](#production-hardening-notes)
- [Upgrade](#upgrade)
- [Uninstall](#uninstall)
- [Values reference](#values-reference)

---

## Architecture recap

Lotsman has two deployable components (see [`docs/ARCHITECTURE.md`](ARCHITECTURE.md)):

| Component | Ports | Role |
|---|---|---|
| **Control plane** (`lotsman-server`) | `8080` REST API + embedded UI, `9090` agent gRPC gateway | Runs the correlation engine, stores incidents, serves the UI. |
| **Agent** (`lotsman-agent`) | none (egress-only) | Runs *in each observed cluster*, reads Kubernetes/Loki/VictoriaMetrics/ArgoCD, and dials **out** to the control-plane gateway. |

Two modes:

- **Direct mode** — the control plane queries a single cluster's backends itself.
  No agent. Simplest single-cluster install.
- **Agent mode** — one or more agents (one per cluster) dial the control-plane
  gateway. Required for multi-cluster, and the only option when the control plane
  can't reach a cluster's API/backends directly.

> **Security (SEC-1):** the gateway is **fail-closed**. It refuses to start
> without a real `LOTSMAN_AGENT_TOKEN`, and agents authenticate with it. The chart
> enforces this — you must supply `secret.agentToken` (or an `existingSecret`).
> The `LOTSMAN_AGENT_ALLOW_INSECURE=1` dev escape hatch is intentionally **not**
> wired into this chart.

---

## Prerequisites

- Kubernetes **1.24+** (uses `policy/v1` PodDisruptionBudget, `networking.k8s.io/v1` Ingress).
- **Helm 3.8+**.
- Ability to pull the images from GHCR (public):
  `ghcr.io/kaminirio/lotsman-{server,agent}`.
- For multi-cluster: a way to expose the gateway (LoadBalancer, NodePort, or an
  L4/gRPC-capable Ingress) reachable from the other clusters.
- Optional: an external PostgreSQL instance for durable state (see below).
- Optional: an Ingress controller + TLS for the UI.

Pin an immutable app version rather than tracking `edge`. Either set the chart's
`appVersion` when you fork it, or override at install time:

```sh
--set image.tag=v0.1.0
```

---

## Generate secrets

Lotsman needs, at minimum, an **agent token**. If you enable SSO you also need a
**session secret** (inside the SSO config JSON). Generate both with `openssl`:

```sh
export LOTSMAN_AGENT_TOKEN="$(openssl rand -hex 32)"
export LOTSMAN_SESSION_SECRET="$(openssl rand -hex 32)"
```

You can hand these to Helm via `--set`/`--set-file`/`-f`, or pre-create a Secret
and point the chart at it with `secret.existingSecret` (recommended so tokens
never sit in your Helm values or release history):

```sh
kubectl create namespace lotsman
kubectl -n lotsman create secret generic lotsman-secrets \
  --from-literal=LOTSMAN_AGENT_TOKEN="$LOTSMAN_AGENT_TOKEN" \
  --from-literal=LOTSMAN_DATABASE_URL="postgres://lotsman:***@postgres.db.svc:5432/lotsman?sslmode=require"
# then: --set secret.existingSecret=lotsman-secrets
```

The Secret keys the chart reads are configurable (`secret.keys.*`) but default to
the `LOTSMAN_*` env var names: `LOTSMAN_AGENT_TOKEN`, `LOTSMAN_DATABASE_URL`,
`LOTSMAN_SSO_CONFIG`, `LOTSMAN_ARGOCD_TOKEN`. Only `LOTSMAN_AGENT_TOKEN` is
required; the rest are optional.

---

## Install: direct (agentless) mode

One cluster, control plane queries its backends directly, no agent:

```sh
helm install lotsman ./deploy/helm/lotsman \
  --namespace lotsman --create-namespace \
  --set image.tag=v0.1.0 \
  --set secret.agentToken="$LOTSMAN_AGENT_TOKEN" \
  --set agent.enabled=false \
  --set controlPlane.directMode.enabled=true \
  --set controlPlane.directMode.lokiUrl="http://loki-gateway.monitoring.svc:80" \
  --set controlPlane.directMode.victoriaUrl="http://vmselect.monitoring.svc:8481" \
  --set controlPlane.directMode.argocdUrl="http://argocd-server.argocd.svc:80"
```

> Direct mode uses the control plane's **in-cluster** ServiceAccount for the
> Kubernetes adapter. Grant it read RBAC equivalent to the agent ClusterRole if
> you want cluster inspection in this mode.

Port-forward the UI to verify:

```sh
kubectl -n lotsman port-forward svc/lotsman-control-plane 8080:8080
open http://localhost:8080
```

---

## Install: agent mode

Control plane + an agent in the **same** cluster:

```sh
helm install lotsman ./deploy/helm/lotsman \
  --namespace lotsman --create-namespace \
  --set image.tag=v0.1.0 \
  --set secret.agentToken="$LOTSMAN_AGENT_TOKEN" \
  --set agent.enabled=true \
  --set agent.cluster="prod-us-east" \
  --set agent.backends.lokiUrl="http://loki-gateway.monitoring.svc:80" \
  --set agent.backends.victoriaUrl="http://vmselect.monitoring.svc:8481" \
  --set agent.backends.argocdUrl="http://argocd-server.argocd.svc:80"
```

When the agent is deployed in the same release as the control plane, it defaults
`LOTSMAN_CONTROL_PLANE_ADDR` to the in-cluster gateway Service — no wiring needed.
The chart also creates the agent's read **ClusterRole/ClusterRoleBinding**
mirroring [`deploy/local/k8s/20-agent-rbac.yaml`](../deploy/local/k8s/20-agent-rbac.yaml)
(pods, events, nodes, pods/log, deployments/statefulsets/daemonsets, replicasets,
configmaps — **not** secrets).

---

## Add an agent from another cluster

To observe additional clusters, install an **agent-only** release in each one,
pointed at the hub cluster's gateway. First, expose the gateway on the hub.

**1. Expose the gateway** (on the control-plane release). Pick one:

```sh
# LoadBalancer (cloud) — restrict who can reach it:
helm upgrade lotsman ./deploy/helm/lotsman --reuse-values \
  --set controlPlane.gatewayService.type=LoadBalancer \
  --set 'controlPlane.gatewayService.loadBalancerSourceRanges={203.0.113.0/24}'

# or NodePort (bare-metal / k3d):
helm upgrade lotsman ./deploy/helm/lotsman --reuse-values \
  --set controlPlane.gatewayService.type=NodePort \
  --set controlPlane.gatewayService.nodePort=30090
```

Get the reachable address:

```sh
# LoadBalancer:
kubectl -n lotsman get svc lotsman-control-plane-gateway \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
# NodePort: use a node's external IP + the nodePort (e.g. 203.0.113.10:30090)
```

**2. Install the agent in the other cluster**, reusing the **same agent token**:

```sh
# kubectl/helm context now points at the SECOND cluster
helm install lotsman-agent ./deploy/helm/lotsman \
  --namespace lotsman --create-namespace \
  --set image.tag=v0.1.0 \
  --set controlPlane.enabled=false \
  --set agent.enabled=true \
  --set agent.cluster="prod-eu-west" \
  --set agent.controlPlaneAddr="203.0.113.10:9090" \
  --set secret.agentToken="$LOTSMAN_AGENT_TOKEN"
```

Notes:

- `agent.controlPlaneAddr` is **required** in agent-only installs (the chart fails
  fast if it's missing).
- Give each cluster a unique `agent.cluster` name — it's the identity shown in the
  UI cluster selector and used for RBAC scoping.
- The agent is **egress-only**; no inbound ports are opened in the observed cluster.

---

## External Postgres

Postgres is **not bundled** (out of scope). Without a DSN the control plane uses an
in-memory store — fine for a quick trial but **single-replica and non-durable**
(state is lost on restart). For production, provision Postgres yourself (managed
service or an operator) and pass the DSN:

```sh
--set secret.databaseUrl="postgres://lotsman:PASSWORD@postgres.db.svc:5432/lotsman?sslmode=require"
```

or include the `LOTSMAN_DATABASE_URL` key in your `existingSecret`. The control
plane applies its embedded migrations on startup. Only derived state (incidents,
change history, clusters) is persisted; logs/metrics are queried live.

---

## SSO / OAuth

SSO is configured with a single JSON blob passed as `LOTSMAN_SSO_CONFIG`
(`secret.ssoConfig`). It carries the session secret, external URLs, GitHub OAuth
credentials, and group→role bindings. Leave it empty to run without SSO.

Create the JSON (keep it in a file, out of shell history):

```json
{
  "session_secret": "REPLACE_WITH_openssl_rand_hex_32",
  "base_url": "https://lotsman.example.com",
  "ui_url": "https://lotsman.example.com",
  "github": {
    "client_id": "Iv1.xxxxxxxx",
    "client_secret": "xxxxxxxxxxxxxxxxxxxx"
  },
  "group_bindings": [
    { "group": "acme:sre", "role": "admin",  "cluster": "*" },
    { "group": "acme:dev", "role": "viewer", "cluster": "prod-eu-west" }
  ]
}
```

- `session_secret` must be **≥ 32 chars** and not a placeholder (validated at
  startup). Generate with `openssl rand -hex 32`.
- Register a GitHub OAuth app with callback `https://<base_url>/auth/callback/github`.
- Pass it to Helm as a file to avoid quoting pain:

```sh
--set-file secret.ssoConfig=./sso.json
```

or store it under the `LOTSMAN_SSO_CONFIG` key of your `existingSecret`. RBAC is
enforced on every handler; `group_bindings` map identity-provider groups to
Lotsman roles scoped by cluster/namespace.

---

## Exposing the UI (Ingress)

The `controlPlane.service` (port 8080) serves the REST API **and** the embedded UI.
Expose it with the built-in Ingress:

```sh
--set controlPlane.ingress.enabled=true \
--set controlPlane.ingress.className=nginx \
--set 'controlPlane.ingress.hosts[0].host=lotsman.example.com' \
--set 'controlPlane.ingress.hosts[0].paths[0].path=/' \
--set 'controlPlane.ingress.hosts[0].paths[0].pathType=Prefix' \
--set 'controlPlane.ingress.tls[0].secretName=lotsman-tls' \
--set 'controlPlane.ingress.tls[0].hosts[0]=lotsman.example.com'
```

The Ingress covers the UI/API only. The **gateway is gRPC** — expose it via
`controlPlane.gatewayService` (LoadBalancer/NodePort), not this Ingress, unless
your controller supports gRPC/HTTP2 backends.

---

## Production hardening notes

The chart ships secure defaults; review these before going live:

- **Images** are pinned via `image.tag` (defaults to the chart `appVersion`). Pin
  an immutable `vX.Y.Z` — don't ship `edge` to prod.
- **securityContext**: `runAsNonRoot`, `runAsUser: 65532`, `readOnlyRootFilesystem`,
  `allowPrivilegeEscalation: false`, all capabilities dropped, `seccompProfile:
  RuntimeDefault`.
- **Resources**: requests/limits set for both components; tune for your load.
- **Probes**: liveness + readiness hit `GET /healthz` on the API port.
- **PodDisruptionBudget**: enable with `controlPlane.podDisruptionBudget.enabled=true`
  and run `controlPlane.replicaCount>1` (requires external Postgres).
- **Gateway exposure**: prefer `loadBalancerSourceRanges` (or a private LB /
  NetworkPolicy) so only your clusters can reach `:9090`.
- **Agent RBAC** is read-only and, by default, **excludes Secrets**. Turning on
  `agent.allowEnvReveal=true` grants the agent cluster-wide Secret `get/list`
  (powers the admin env-reveal + Secrets browser) — a cluster-wide credential
  blast radius. Use **only** in trusted, single-tenant clusters.

---

## Upgrade

```sh
helm upgrade lotsman ./deploy/helm/lotsman --reuse-values \
  --set image.tag=v0.2.0
kubectl -n lotsman rollout status deploy/lotsman-control-plane
```

Roll back if needed:

```sh
helm rollback lotsman
```

When Postgres is configured, migrations run automatically on the new control-plane
pods at startup.

---

## Uninstall

```sh
helm uninstall lotsman -n lotsman
```

The agent's ClusterRole/ClusterRoleBinding are cluster-scoped and Helm-managed, so
they're removed with the release. An externally-created `existingSecret`, your
Postgres data, and the namespace itself are **not** deleted — remove them manually
if desired:

```sh
kubectl delete namespace lotsman
```

---

## Values reference

Key knobs (see [`values.yaml`](../deploy/helm/lotsman/values.yaml) for the full set
and inline docs):

| Value | Default | Purpose |
|---|---|---|
| `image.registry` / `image.tag` | `ghcr.io/kaminirio` / `""`→appVersion | Image source; pin `tag` in prod. |
| `secret.agentToken` | `""` | **Required** gateway/agent token (or use `existingSecret`). |
| `secret.existingSecret` | `""` | Use a pre-created Secret instead of chart-managed one. |
| `secret.databaseUrl` | `""` | External Postgres DSN; empty = in-memory. |
| `secret.ssoConfig` | `""` | SSO/OAuth JSON (session secret + GitHub + bindings). |
| `controlPlane.enabled` | `true` | Deploy the control plane. |
| `controlPlane.replicaCount` | `1` | Replicas (>1 needs Postgres + PDB). |
| `controlPlane.directMode.enabled` | `false` | Agentless single-cluster mode. |
| `controlPlane.service.type` | `ClusterIP` | UI/API Service type. |
| `controlPlane.gatewayService.type` | `ClusterIP` | Agent gateway: `ClusterIP`/`LoadBalancer`/`NodePort`. |
| `controlPlane.gatewayService.nodePort` | `""` | Fixed NodePort when type=NodePort. |
| `controlPlane.gatewayService.loadBalancerSourceRanges` | `[]` | Restrict LB access to the gateway. |
| `controlPlane.ingress.enabled` | `false` | Expose UI/API via Ingress. |
| `controlPlane.podDisruptionBudget.enabled` | `false` | Add a PDB. |
| `agent.enabled` | `true` | Deploy the in-cluster agent. |
| `agent.controlPlaneAddr` | `""` | Gateway address; required for agent-only installs. |
| `agent.cluster` | `default` | Unique logical cluster name. |
| `agent.backends.*` | Loki/VM/ArgoCD svc URLs | Backends this agent queries. |
| `agent.allowEnvReveal` | `false` | ⚠️ grants cluster-wide Secret read. |
