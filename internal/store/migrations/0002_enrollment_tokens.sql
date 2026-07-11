-- 0002_enrollment_tokens: per-cluster agent enrollment tokens (replaces the
-- single shared LOTSMAN_AGENT_TOKEN model). Only the SHA-256 hash is stored; the
-- plaintext is shown once at creation. expires_at NULL means "no expiry" and maps
-- to a zero time.Time in Go.
CREATE TABLE IF NOT EXISTS enrollment_tokens (
    id         TEXT PRIMARY KEY,
    cluster    TEXT NOT NULL,
    hash       TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ,
    revoked    BOOLEAN NOT NULL DEFAULT false
);
CREATE INDEX IF NOT EXISTS enrollment_tokens_cluster_idx ON enrollment_tokens (cluster);
