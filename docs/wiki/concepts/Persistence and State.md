---
title: "Persistence and State"
type: concept
tags: [concept, architecture, postgres, store]
created: 2026-06-21 16:08:49
updated: 2026-06-21 16:08:49
status: current
aliases: ["Store", "Postgres Store", "Query-Through"]
---

# Persistence and State

## Overview

Lotsman is **query-through, not a data lake** (ADR-0004): telemetry (logs, metrics) is fetched live from backends on demand; only derived state is persisted. The persistence layer is a `store.Store` interface with two implementations: `store.Memory` (local dev) and `store.PostgresStore` (production, ADR-0005).

## What is Persisted

| Entity | Store table | Notes |
|---|---|---|
| `Incident` | `incidents` | Scalar fields + JSONB `resource`, `timeline`, `hypotheses` |
| `Cluster` | `clusters` | Registration metadata |
| RBAC Bindings | _(in-memory only, not yet persisted)_ | See [[Authentication and RBAC]] |

Telemetry (Loki logs, VictoriaMetrics metrics, ArgoCD deploy history, Kubernetes events) is **never** written to the store — always queried live through [[Source-Agnostic Adapters]].

## PostgresStore (`internal/store/postgres.go`)

- **Migrations:** `internal/store/migrations/0001_init.sql` — idempotent `CREATE TABLE IF NOT EXISTS` for `incidents` and `clusters`. Run automatically on `NewPostgresStore`.
- **JSONB for evolving fields:** `resource`, `timeline`, and `hypotheses` are stored as JSONB to avoid constant schema migrations as the `model.Incident` shape evolves.
- **UPSERTs:** `UpsertIncident` / `UpsertCluster` on conflict-update; `GetIncident` returns `store.ErrNotFound` on miss.
- **Queries:** `ListIncidents` supports `cluster`, `namespace`, `status` filters; `opened_at DESC`; configurable `limit`.
- **Pool lifecycle:** opened in `NewPostgresStore`; `pool.Close()` called in `controlplane.Shutdown`.
- **Dependency:** `github.com/jackc/pgx/v5`.

## Wiring in Control Plane (`internal/controlplane/controlplane.go`)

```
LOTSMAN_DATABASE_URL set?
  yes -> NewPostgresStore (no seed)
  no  -> store.Memory + store.Seed (local dev unchanged)
```

`store.Seed` should be removed once Postgres is always active.

## Memory Store (`store.Memory`)

Used when `LOTSMAN_DATABASE_URL` is absent. `store.Seed(st)` pre-populates a sample investigation (ArgoCD deploy ranked as probable cause) so `make run-server` + `curl /api/v1/incidents` returns useful data without a database.

## Open Items

- RBAC bindings need a `bindings` table; currently reset on restart. See [[Authentication and RBAC]].
- `store.Seed` call in direct-mode path should be removed after Postgres is always wired.

## Relationships & Context

- **Parent concept:** [[Lotsman]]
- **Related:** [[Source-Agnostic Adapters]], [[Correlation Engine]], [[Authentication and RBAC]]
- **Implementation report:** [[Feature Platform Foundation 2026-06-21]]
- **Relevant skills:** `golang-database` (pgx patterns), `wshobson/agents@postgresql-table-design` — see [[Development Skills]]
- **Sources:** `internal/store/`, `docs/adr/0004-query-through-telemetry.md`, `docs/adr/0005-postgres-store.md`
