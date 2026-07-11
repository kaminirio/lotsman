# Lotsman Helm charts

Two charts that mirror the [agent ↔ control-plane topology](../../docs/adr/0001-topology-agent-control-plane.md):

| Chart | Install where | How many | What it does |
|---|---|---|---|
| [`lotsman-control-plane`](./lotsman-control-plane) | central / management cluster | **one** | Terminates agent links, runs the correlation engine, persists derived state, serves the API + embedded UI. |
| [`lotsman-agent`](./lotsman-agent) | **each** monitored cluster | one per cluster | Reads cluster data through the source-agnostic adapters and dials **out** to the control plane. |

They are separate charts on purpose: the control plane runs once with its own
lifecycle, while an agent runs in every monitored cluster and only needs **egress**
to the control plane (the control plane never dials back). Each agent holds its own
**per-cluster enrollment token**, minted on the control plane after installation —
see [ADR-0010](../../docs/adr/0010-per-cluster-enrollment-tokens.md). There is no
shared token across the control plane and agents.

The charts wrap the same containers and `LOTSMAN_*` env surface as the raw manifests
in [`../local/k8s`](../local/k8s); those manifests remain the local-dev quickstart.

## Layouts

### A) One reachable cluster — direct mode (no agent)

The "solve my own stack first" path. The control plane reads its own cluster
in-process; no agent, no token to share.

```sh
helm install lotsman ./lotsman-control-plane \
  --namespace lotsman --create-namespace \
  --set config.directMode=true \
  --set config.cluster=local
```

`config.directMode=true` makes the chart grant the control plane cluster-read RBAC
(`rbac.create`, on by default) and drop the agent gateway port from its Service.

### B) Central control plane + agents (multi-cluster)

**1. Control plane** (management cluster) — no shared token to configure, but a
**durable store is required for agent onboarding**: enrollment tokens can't be
re-derived, so with an in-memory store the control plane refuses to mint them
(`503`) and rejects all agents. Point it at Postgres:

```sh
helm install lotsman ./lotsman-control-plane \
  --namespace lotsman --create-namespace \
  --set config.databaseUrl='postgres://USER:PASS@HOST:5432/lotsman?sslmode=disable'
```

If agents live outside this cluster, expose the gateway port (`9090`) — e.g.
`--set service.type=LoadBalancer`, or a NodePort/gRPC-aware Ingress. The bundled
Ingress only fronts the API+UI port (`8080`); the gateway is gRPC/h2c and is not
routed through it.

**2. Mint a per-cluster enrollment token** on the control plane for each agent
cluster. The easiest path is the admin **Clusters** page (`/admin/clusters`): it
lists clusters and, on "enroll", mints the token and hands you the exact
`helm install lotsman-agent` command to paste into the target cluster. For that
generated command to be accurate, set the advertised gateway address on the
control plane so the UI can fill it in:

```sh
--set config.enrollment.publicGatewayAddr=lotsman.example.com:9090
```

Prefer the terminal? Use the CLI (requires the control-plane API to be reachable):

```sh
# Mint a token for cluster prod-eu; the plaintext is printed once to stdout.
TOKEN=$(lotsman cluster-token generate prod-eu --api http://lotsman.example.com:8080)
```

Alternatively, call the admin API directly:

```sh
TOKEN=$(curl -s -X POST http://lotsman.example.com:8080/api/v1/enrollment-tokens \
  -H 'Content-Type: application/json' \
  -d '{"cluster":"prod-eu"}' | jq -r .token)
```

**3. Install an agent in each monitored cluster** — its own minted token, a
**unique** cluster name, pointed at the control plane's reachable gateway address:

```sh
helm install lotsman-agent ./lotsman-agent \
  --namespace lotsman --create-namespace \
  --set config.cluster=prod-eu \
  --set config.controlPlaneAddr=lotsman.example.com:9090 \
  --set agentToken.value="$TOKEN" \
  --set config.loki.url=http://loki-gateway.monitoring.svc:80 \
  --set config.victoria.url=http://vmselect.monitoring.svc:8481 \
  --set config.argocd.url=http://argocd-server.argocd.svc:80
```

Repeat steps 2–3 per cluster. Each cluster gets its own token; revoking one does
not affect others. Within the same cluster as the control plane, use
`config.controlPlaneAddr=lotsman-control-plane.lotsman.svc:9090`.

## Supplying the agent token via an existing Secret

Instead of inlining the per-cluster token, reference a Secret you manage (e.g. from
an external secrets operator). It must hold the token under `existingSecretKey`
(default `agent-token`):

```sh
--set agentToken.existingSecret=lotsman-agent-prod-eu --set agentToken.existingSecretKey=token
```

## Managing enrollment tokens

| Action | CLI | Admin UI |
|---|---|---|
| Mint a token for a cluster | `lotsman cluster-token generate <cluster>` | Admin → Enrollment |
| List tokens + status | `lotsman cluster-token list` | Admin → Enrollment |
| Revoke a token | `lotsman cluster-token revoke <id>` | Admin → Enrollment |

Revocation takes effect on the agent's next reconnect. The plaintext is only ever
shown at mint time; the list endpoint and UI return metadata only.

## Key values

Control plane (`lotsman-control-plane/values.yaml`):

| Value | Default | Notes |
|---|---|---|
| `config.directMode` | `false` | Read local cluster in-process; no agent. |
| `config.seed` | `false` | Demo seed data — keep off in real deployments. |
| `config.databaseUrl` | `""` | Postgres DSN; empty ⇒ in-memory store (single pod). |
| `config.llm.url` | `""` | LLM incident-explainer; empty disables `/explain`. |
| `ingress.enabled` | `false` | Fronts API+UI (`8080`) only. |
| `rbac.create` | `true` | Cluster-read role, used only in `directMode`. |

The control-plane chart has **no `agentToken` value** — enrollment tokens are minted
via the API after installation.

Agent (`lotsman-agent/values.yaml`):

| Value | Default | Notes |
|---|---|---|
| `config.controlPlaneAddr` | `lotsman-control-plane.lotsman.svc:9090` | **Required.** Gateway address agents dial out to. |
| `config.cluster` | `default` | **Required, unique per install.** Must match the cluster name used when the token was minted. |
| `agentToken.value` | `""` | **Required.** Per-cluster enrollment token minted for this cluster. |
| `config.loki.url` / `config.victoria.url` / `config.argocd.url` | in-cluster defaults | Empty falls back to built-in defaults; unreachable backends degrade gracefully. |
| `config.argocd.token` | `""` | ArgoCD API token (stored in the chart's Secret). |
| `rbac.envReveal` + `allowEnvReveal` | `false` | ⚠ See below. |

### ⚠ Secret env-reveal (opt-in, dangerous)

By default the agent has **no** access to Secret values. Setting BOTH
`rbac.envReveal=true` (grants cluster-wide `get`/`list` on Secrets) and
`allowEnvReveal=true` (arms the agent to resolve them) powers the admin "reveal
resolved env" feature and the Secrets browser. With it, anyone who compromises the
agent can read **every Secret in the cluster**. Enable only in a **trusted,
single-tenant** cluster. Each flag alone is inert — the RBAC grant without the app
flag resolves nothing, and the app flag without RBAC is denied by the API server.

## Verify

```sh
helm lint lotsman-control-plane lotsman-agent
helm template lotsman ./lotsman-control-plane | kubectl apply --dry-run=client -f -

# control plane up:
kubectl -n lotsman port-forward svc/lotsman-control-plane 8080:8080
curl localhost:8080/api/v1/incidents | jq

# agents registered:
curl localhost:8080/api/v1/clusters | jq
```
