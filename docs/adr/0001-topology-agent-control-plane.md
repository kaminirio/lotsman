# ADR-0001 — In-cluster agent + central control plane

**Status:** Accepted

## Context
Lotsman must monitor any Kubernetes cluster on any cloud (principle 1). The major
fork for such a tool is how signals reach the brain: agentless (control plane queries
everything directly) vs. an in-cluster agent that forwards. Agentless is simplest but
requires the control plane to reach every cluster's Loki/VM/ArgoCD/Kube API — often
impossible across NATs, private networks, and clouds.

## Decision
Run a lightweight **agent in each cluster** that **dials out** to a central **control
plane**. The agent is the single egress point; clusters expose nothing inbound. The
control plane also supports a degenerate **direct mode** (no agent) for a single
reachable cluster — the default for local dev and the "solve my own stack first" path.

## Consequences
- Works uniformly across clouds and private networks; firewall/NAT friendly.
- Per-cluster isolation and resilience; the agent can buffer during control-plane
  blips.
- More to build (agent lifecycle, enrollment, the link) — mitigated by direct mode,
  which exercises the same `sources.Provider` seam (see [ADR-0003](0003-source-agnostic-adapters.md)).
