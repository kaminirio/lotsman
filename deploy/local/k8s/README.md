# Lotsman local k3d deployment + adapter validation

Stands up the control plane + in-cluster agent (agent mode, real gRPC link) plus
Loki / VictoriaMetrics / ArgoCD / a crash-looping demo workload, then validates the
four source adapters end-to-end through the agent.

## Safety
All commands MUST use the isolated kubeconfig and the `k3d-lotsman` context only:
```sh
export KUBECONFIG=/tmp/lotsman.kubeconfig
kubectl config current-context   # must print: k3d-lotsman
```

## Apply order
```sh
export KUBECONFIG=/tmp/lotsman.kubeconfig
kubectl apply -f 00-namespaces.yaml
kubectl apply -f 10-control-plane.yaml      # lotsman-control-plane (API :8080, gateway :9090)
kubectl apply -f 20-agent-rbac.yaml         # SA + ClusterRole (events/pods/pods/log/deploys/sts/ds) — NO secrets
kubectl apply -f 21-agent.yaml              # lotsman-agent, dials gateway, capabilities=[loki,vm,argocd,k8s]
# OPTIONAL (opt-in admin env-reveal only — see "Admin env reveal" below):
# kubectl apply -f 21-agent-rbac-reveal.yaml  # cluster-wide secrets/configmaps get + LOTSMAN_ALLOW_ENV_REVEAL=1
kubectl apply -f 30-demo-checkout.yaml      # ns demo, app=checkout, crash-loops -> BackOff warnings
kubectl apply -f 40-loki.yaml               # Loki single-binary (monitoring/loki:3100)
kubectl apply -f 50-alloy.yaml              # Alloy DaemonSet: ships pod logs -> {namespace,app}
kubectl apply -f 60-victoriametrics.yaml    # VM single-node (monitoring/victoriametrics:8428)

# ArgoCD (official install) + Application + auth — see 71-argocd-auth.README.md
kubectl create ns argocd
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
# (the applicationset CRD trips kubectl's annotation size limit; re-run with --server-side)
kubectl apply --server-side -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
kubectl apply -f 70-argocd-app.yaml         # Application checkout-guestbook, dest ns=demo
# then follow 71-argocd-auth.README.md to enable insecure HTTP + mint the lotsman token
```

## Validate
```sh
kubectl -n lotsman port-forward svc/lotsman-control-plane 8080:8080 &
curl -s -X POST localhost:8080/api/v1/investigate \
  -H 'Content-Type: application/json' \
  -d '{"cluster":"local","namespace":"demo","kind":"Deployment","name":"checkout"}' | jq
```
Expect a timeline with `kubernetes` (k8s_event), `loki` (log), and `argocd` (change)
signals, and a top hypothesis "Deploy of checkout-guestbook ... before the incident".
A captured run is in `evidence-investigate.json`.

## Reach the UI
```sh
export KUBECONFIG=/tmp/lotsman.kubeconfig
kubectl -n lotsman port-forward svc/lotsman-control-plane 8080:8080
# open http://localhost:8080
```

## Admin env reveal (opt-in, OFF by default)

The pod-inspection UI can resolve a pod's `valueFrom:` (secretKeyRef /
configMapKeyRef) env vars to their actual values for **admin** users. This is
**disabled by default** and requires BOTH of the following, together:

1. **RBAC:** `kubectl apply -f 21-agent-rbac-reveal.yaml` — grants the agent SA
   cluster-wide `get` on `secrets` and `configmaps`. This is a serious blast
   radius: a compromised agent can read every Secret in the cluster. Apply
   **only** in trusted, single-tenant clusters.
2. **Agent flag:** set `LOTSMAN_ALLOW_ENV_REVEAL=1` on the agent Deployment
   (`21-agent.yaml`). The agent ignores the control plane's wire `Reveal` flag
   unless this is set — so the RBAC grant alone resolves nothing, and a
   compromised/over-eager control plane cannot make an un-opted-in agent read
   Secrets (defense in depth).

**Without both:** Secret/ConfigMap-sourced env values are shown as references
only (a chip with the var name + source), never their values. Inline literal env
values are always masked for non-admins regardless of these settings (default-
deny: every literal value is redacted for non-admins).

> Direct mode (control plane in-process, no agent) has no agent gate; reveal
> there is governed solely by the control plane's admin check.

## Security posture

These manifests are a **local-dev template**, not a hardened production install
(see `deploy/helm/` for the production chart). The three limitations previously
flagged here have since been fixed in the control plane:

- **(a) Per-namespace RBAC is enforced.** SSO no longer grants every
  authenticated user a global view. Access is deny-by-default: a user sees only
  the clusters/namespaces granted by a matching `bindings`/`group_bindings` entry
  in `LOTSMAN_SSO_CONFIG`, and those bindings carry a `namespace` scope enforced
  by `internal/rbac` (`CanAccess` matches cluster **and** namespace). Global admin
  is granted only to `init_admin` and the anonymous local-dev principal.
- **(b) Pod logs are redacted for non-admins.** `GET .../pods/<pod>/logs` runs
  the body through `internal/redact` for non-admin viewers before returning it
  (admins see verbatim). Note the redactor is best-effort pattern-based, not a
  guarantee that every secret/PII shape is caught.
- **(c) Malformed SSO config fails closed.** A present-but-invalid
  `LOTSMAN_SSO_CONFIG` is fatal — the control plane refuses to start rather than
  silently degrading to the anonymous-admin path. An *unset* config still enables
  the intentional anonymous local-dev pass-through.

**Still dev-only, before you copy these as a production base:** the manifests use
`dev-token` for `LOTSMAN_AGENT_TOKEN` and plaintext (non-mTLS) agent↔control-plane
transport (SEC-1), pin `:dev` image tags, and add no NetworkPolicy. Use the Helm
chart and real secrets for anything multi-tenant.

## Tear down
```sh
k3d cluster delete lotsman
```
