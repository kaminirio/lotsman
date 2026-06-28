# ADR-0004 — Query-through telemetry, not re-ingestion

**Status:** Accepted

## Context
A monitoring tool could ingest and store logs/metrics itself (its own TSDB + log store)
or query the systems that already hold them. The operator already runs Loki and
VictoriaMetrics — best-in-class at exactly this.

## Decision
**Query through** to the existing backends on demand; do **not** re-ingest or duplicate
telemetry. Lotsman persists only its own *derived* state (incidents, change history,
config) — see [ADR-0005](0005-postgres-state.md). The one exception is **change
events**: ArgoCD history is ephemeral and is the backbone of investigation, so changes
are persisted.

## Consequences
- Far less to build and operate; no storage/retention/cardinality problems to re-solve.
- Lower cost; the source of truth stays in the operator's existing stack.
- Query latency/availability depends on the upstreams — handled by the correlator's
  per-source failure tolerance and (future) result caching.
