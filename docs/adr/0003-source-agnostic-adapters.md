# ADR-0003 — Source-agnostic adapter interfaces

**Status:** Accepted

## Context
Principles 1 and 2 demand that no backend leak into the core. We also need the same
correlation logic to run in direct mode (concrete adapters local) and agent mode
(adapters remote, behind the link).

## Decision
Define four neutral interfaces in `internal/sources` — `LogSource`, `MetricSource`,
`DeploymentSource`, `ClusterSource` — bundled per cluster as a `Provider`. Each has:
- a **concrete** implementation per backend (`loki`, `victoriametrics`, `argocd`,
  `kubernetes`) that runs in the agent;
- a **remote** implementation (`sources/remote`) that proxies calls over the agent
  link, used by the control plane in agent mode.

The engine depends only on `Provider`. The registry decides direct vs. remote; the
engine never finds out.

## Consequences
- Backends are swappable (VictoriaMetrics↔Prometheus is free; a different log store is
  one adapter).
- Correlation logic is written once and is trivially testable with fake providers.
- Two implementations per interface to maintain (concrete + remote) — but they are thin
  and mirror each other by request kind.
