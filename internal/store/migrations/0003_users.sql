-- 0003_users: first-party user accounts (ADR-0011). Replaces the anonymous
-- =global-admin model with admin-provisioned local accounts plus optional
-- OAuth SSO linkage. password_hash is a bcrypt hash and is empty for SSO-only
-- accounts. sso_provider/sso_subject link a local account to an external
-- identity once it has signed in. is_admin is the sole source of the global
-- admin grant (the RBAC Enforcer builds an admin binding for it).
CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL,
    email         TEXT NOT NULL,
    password_hash TEXT NOT NULL DEFAULT '',
    is_admin      BOOLEAN NOT NULL DEFAULT false,
    active        BOOLEAN NOT NULL DEFAULT true,
    sso_provider  TEXT NOT NULL DEFAULT '',
    sso_subject   TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Username and email are unique case-insensitively so "Alice" and "alice"
-- cannot both be provisioned and SSO email matching is case-insensitive.
CREATE UNIQUE INDEX IF NOT EXISTS users_username_lower_idx ON users (lower(username));
CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_idx ON users (lower(email));
