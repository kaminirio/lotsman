# ADR-0005 — PostgreSQL for control-plane state

**Status:** Accepted

## Context
Derived state — incidents, timelines, hypotheses, change history, clusters, users,
RBAC, config — needs durable, queryable storage. PostgreSQL via `pgx` is a well-proven
fit for this mix of relational and semi-structured (JSONB) data.

## Decision
Use **PostgreSQL** via **pgx**. Persist derived state only (per
[ADR-0004](0004-query-through-telemetry.md)). Model access behind the `store.Store`
interface; ship an in-memory implementation for dev/tests and a pgx implementation for
production. The `ClusterRecord` shape (env/region metadata) is designed for long-term
schema stability so that future integrations with other tools remain straightforward.

## Consequences
- Relational fit for incidents/timelines/changes; JSONB for flexible signal payloads.
- Single database technology; no ORM — hand-written pgx queries keep the query layer explicit.
- Standard PostgreSQL tooling (migrations, backups, monitoring) applies without special adaptation.
