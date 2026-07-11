-- 0001_init: control-plane state schema (ADR-0004/ADR-0005).
-- Applied and recorded once by the versioned migrator (see postgres.go migrate);
-- kept idempotent (CREATE ... IF NOT EXISTS) so it records cleanly against a
-- pre-existing scaffold schema. Later migrations need not be idempotent.
-- Persist DERIVED state only — logs/metrics are queried live through agents.

-- Incidents: scalar columns for the fields ListIncidents filters/sorts on, plus
-- JSONB for the nested model structs (ResourceRef, []Signal, []Hypothesis).
CREATE TABLE IF NOT EXISTS incidents (
    id         TEXT        PRIMARY KEY,
    cluster    TEXT        NOT NULL,
    namespace  TEXT        NOT NULL DEFAULT '',
    kind       TEXT        NOT NULL DEFAULT '',
    name       TEXT        NOT NULL DEFAULT '',
    title      TEXT        NOT NULL DEFAULT '',
    status     TEXT        NOT NULL,
    severity   INTEGER     NOT NULL,
    opened_at  TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    resource   JSONB       NOT NULL,
    timeline   JSONB       NOT NULL DEFAULT '[]'::jsonb,
    hypotheses JSONB       NOT NULL DEFAULT '[]'::jsonb
);

-- ListIncidents filters by cluster and/or status, most-recent-first.
CREATE INDEX IF NOT EXISTS incidents_cluster_opened_at_idx
    ON incidents (cluster, opened_at DESC);
CREATE INDEX IF NOT EXISTS incidents_status_idx
    ON incidents (status);

-- Clusters: mirrors the store.Cluster record shape.
CREATE TABLE IF NOT EXISTS clusters (
    name          TEXT    PRIMARY KEY,
    env           TEXT    NOT NULL DEFAULT '',
    region        TEXT    NOT NULL DEFAULT '',
    connected     BOOLEAN NOT NULL DEFAULT false,
    agent_version TEXT    NOT NULL DEFAULT ''
);
