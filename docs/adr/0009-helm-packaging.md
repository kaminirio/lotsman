# ADR-0009 — Helm packaging: two charts, one per topology role

**Status:** Accepted

## Context
The control plane and the agent have to be deployable to Kubernetes by operators,
not just via the hand-rolled `deploy/local/k8s` manifests (which exist for local
dev). The two components have fundamentally different deployment shapes, set by the
topology ([ADR-0001](0001-topology-agent-control-plane.md)): the control plane runs
**once**, centrally; the agent runs **once per monitored cluster** and dials **out**
to the control plane, so only that direction needs network reachability. They share
exactly one piece of state — the **agent token** — and otherwise have disjoint
config, RBAC, and lifecycles.

## Decision
Ship **two independent Helm charts** under `deploy/helm`:
`lotsman-control-plane` and `lotsman-agent` (not one umbrella chart with toggled
subcomponents).

- Each chart owns its Deployment, ServiceAccount, Secret, and RBAC, and exposes the
  same `LOTSMAN_*` env surface (`internal/config`) as named values.
- The shared agent token lives in a per-chart Secret (`agentToken.value`) or an
  externally-managed Secret (`agentToken.existingSecret`); the operator sets the
  same value on both sides.
- **Direct mode** is a control-plane value (`config.directMode`), not a third chart:
  it drops the agent gateway from the Service and grants the control plane the same
  cluster-read RBAC the agent would otherwise carry.
- The agent's cluster-wide Secret-read grant (env-reveal,
  [ADR-0007](0007-auth-github-oauth.md) / ARCHITECTURE §10) is a **double-gated
  opt-in** — `rbac.envReveal` (the RBAC) **and** `allowEnvReveal` (the app flag) —
  both off by default, mirroring the `21-agent-rbac-reveal.yaml` warning.

## Consequences
- Operators install/upgrade the control plane and each agent independently, on their
  own schedules and versions — matching how the fleet actually grows (add a cluster
  ⇒ `helm install lotsman-agent`).
- No false coupling: an umbrella chart would imply the agent and control plane share
  a release boundary they do not.
- Mild duplication between the two charts' `_helpers.tpl` and token plumbing,
  accepted as the cost of independence. If a shared library chart becomes worth it,
  it slots in under the same two public charts.
- The raw `deploy/local/k8s` manifests remain the local-dev quickstart; the charts
  are the path to real multi-cluster deployments.
