---
title: "Authentication and RBAC"
type: concept
tags: [concept, architecture, auth, rbac, oauth, jwt]
created: 2026-06-21 16:08:49
updated: 2026-07-11 17:58:00
status: current
aliases: ["Auth", "SSO", "GitHub OAuth"]
---

# Authentication and RBAC

## Overview

Lotsman uses GitHub OAuth for SSO, HS256 JWT cookies for sessions, and a config-driven cluster/namespace-scoped RBAC model for access control (ADR-0007). Bindings are declared in `LOTSMAN_SSO_CONFIG` as subject (GitHub login) bindings and group (org/team slug) bindings. Access is **deny-by-default**: a user with no matching binding is denied on every API call. When SSO is unconfigured (local dev), every request is treated as Anonymous with global Admin — no code-path changes needed for the single-developer experience.

## Components

### SSO Config (`internal/auth/sso_config.go`)

Parsed from environment variables:

| Env var | Purpose |
|---|---|
| `LOTSMAN_GITHUB_CLIENT_ID` | OAuth app client ID (if unset, auth is disabled → anonymous-admin) |
| `LOTSMAN_GITHUB_CLIENT_SECRET` | OAuth app secret |
| `LOTSMAN_GITHUB_CALLBACK_URL` | OAuth callback URL |
| `LOTSMAN_JWT_SECRET` | HMAC key for JWT signing |
| `LOTSMAN_SSO_CONFIG` | JSON/YAML blob containing `allowed_logins`, `bindings`, and `group_bindings` |

`SSOConfig` binding fields:

| Field | Type | Notes |
|---|---|---|
| `bindings` | `[]Binding` | `{subject, role, cluster, namespace}` — subject is a GitHub login handle |
| `group_bindings` | `[]GroupBinding` | `{group, role, cluster, namespace}` — group is an org slug or `org/team` slug |

Validation rules (enforced by `ParseSSOConfig`):
- `role` must be one of `admin`, `operator`, `viewer`.
- `cluster` must be non-empty; use `"*"` for a global (all-cluster) binding. An empty cluster string is rejected.
- `namespace` may be empty, meaning all namespaces within the named cluster.
- GitHub OAuth scope includes `read:org` so group membership can be queried.

### Enforcer resolution (`internal/auth/auth.go` — `Enforcer()`)

The single function that maps a session to an `rbac.Enforcer`:

| Condition | Result |
|---|---|
| SSO disabled OR **anonymous provider** | Global admin (local-dev pass-through) |
| `init_admin` login | Global admin |
| All other authenticated users | Union of subject-matched bindings + group-matched bindings from the JWT `Groups` claim |
| No binding matches | **Deny everything** (deny-by-default, no fallback) |

The anonymous pass-through is keyed on the **provider** field being anonymous, not on the login string, which prevents a crafted `login=anonymous` JWT from gaining admin.

### OAuth Handlers (`internal/auth/oauth_handlers.go`)

- `GET /auth/login` — redirects to GitHub OAuth with a signed `state` CSRF token.
- `GET /auth/callback` — exchanges code for token; fetches GitHub user; fetches org + team membership (when `group_bindings` are configured); checks `IsLoginAllowed`; writes JWT cookie.
- `POST /auth/logout` — clears the session cookie.
- `GET /auth/me` — returns current user identity: login, roles, `is_admin` (bool), `groups` (non-null string array), anonymous flag.

### Login gate (`sso_config.go` — `IsLoginAllowed`)

A user is admitted through the OAuth callback if they are:
1. On the static `allowed_logins` list, OR
2. Named as a `subject` in any binding, OR
3. A member of any group named in `group_bindings`.

Login admission grants nothing on its own — the enforcer applies deny-by-default on every subsequent API call.

### Session (`internal/auth/session.go`)

- HS256 JWT written into an `HttpOnly; SameSite=Strict` cookie.
- Algorithm-confusion-proof: uses `jwt.ParseWithClaims` with an explicit `HS256` algorithm check — rejects RS256/none tokens.
- `SessionClaims` and `User` carry `Groups []string` (embedded at login time; refreshed on re-login).
- Membership is a **login-time snapshot** — a user's group membership is stale until they re-authenticate.
- **Sliding expiry (2026-07-11 campaign)** — the previous fixed 8h hard-logout was replaced with a sliding session: activity extends the session, tracked by a lineage ID with a 24h absolute cap so a lineage cannot be extended indefinitely. A code-review finding during that campaign — a revoked lineage could still slide — was fixed by checking revocation on every slide, not only at issuance. See [[Backlog Improvement Campaign Waves 0-3 2026-07-11]].

### Rate Limiting (`internal/auth` + `internal/api`)

Per-IP token-bucket rate limiting was added on `GET /auth/login`, `GET /auth/callback`, and `POST /api/v1/investigate` (2026-07-11 campaign) — previously no route had a limiter, leaving the OAuth handshake and the expensive investigate path open to brute-force/hammer abuse.

### CSRF Middleware (`internal/auth/middleware.go`)

- All mutating methods (POST, PUT, DELETE, PATCH) require the `X-Requested-With: XMLHttpRequest` header.
- Anonymous pass-through when `LOTSMAN_GITHUB_CLIENT_ID` is unset.

### RBAC (`internal/rbac/rbac.go`)

- **Roles:** `Admin > Operator > Viewer`.
  - Admin: all actions.
  - Operator: `view` + `investigate`.
  - Viewer: `view` only.
- **Actions:** `view`, `investigate`.
- **Scope:** each binding carries `(role, cluster, namespace)`; `"*"` wildcards supported for cluster and namespace.

**Cluster-wide vs namespace-scoped distinction (critical):**

A cluster-wide query (empty namespace in the request, meaning "any namespace") is granted **only** by a binding whose namespace is a wildcard. A namespace-scoped binding (e.g., `namespace: "default"`) does NOT grant cluster-wide access. This prevents a namespace-scoped viewer from reading all namespaces via the `_all` sentinel.

`CanViewCluster` is a separate predicate used for cluster enumeration (`handleListClusters`): a namespace-scoped viewer still sees their cluster listed, but `handleListNodes` requires full-cluster view.

## Admin Inspector API

Two read-only admin endpoints (both require admin role; 401 if unauthenticated, 403 if non-admin):

| Endpoint | Returns |
|---|---|
| `GET /api/v1/admin/rbac/config` | Sanitized binding list — never includes secrets, JWT keys, or client credentials |
| `GET /api/v1/admin/rbac/effective?user=<login>` | Computed bindings for the given login + `is_admin` flag |

The config is immutable at runtime. The inspector surface is read-only by design; a mutation API is deferred until the Postgres store lands.

## Enforcement in API Handlers

| Handler | Enforcement |
|---|---|
| `handleInvestigate` | 403 if caller lacks `investigate` on target cluster/namespace |
| `handleListIncidents` | Response filtered to namespaces caller can `view` |
| `handleGetIncident` | 403 if caller cannot `view` incident's namespace |
| `handleListClusters` | Admin guard + `CanViewCluster` filter (namespace-scoped viewers see clusters they have any binding for) |
| `handleListPods` | 403 if caller cannot `view` target cluster/namespace |
| `handlePodLogs` | 403 if caller cannot `view` target cluster/namespace |
| `handleListConfigMaps` | 403 if caller cannot `view` target cluster/namespace |
| `handleGetConfigMap` | 403 if caller cannot `view` target cluster/namespace |
| `handleListSecrets` | 403 if caller cannot `view` target cluster/namespace |
| `handleGetSecret` | 403 if caller cannot `view` target cluster/namespace; value reveal additionally requires admin + `LOTSMAN_ALLOW_ENV_REVEAL=1` |
| `handleListNodes` | 403 if caller cannot `view` entire cluster (namespace-scoped binding is not sufficient) |
| `handleExplainIncident` | 403 if caller cannot `view` incident's cluster/namespace |

## Permission-Driven Env-Var Reveal

Applied to pod inspection (see [[Feature Pod Inspection 2026-06-21]]). The model is **default-deny on literal values**, not a name denylist:

- **Admin** (including anonymous-admin in local dev): literal env var values returned verbatim; `valueFrom` references (`secretKeyRef` / `configMapKeyRef`) are resolved to actual values by the kubernetes adapter, with provenance metadata returned alongside.
- **Viewer / Operator**: ALL literal values are masked regardless of their name; `valueFrom` references are shown as unresolved reference chips (key name and source kind visible, value not fetched).

The `Reveal` flag is propagated on the gRPC wire request to the agent, but the agent honours it only when `LOTSMAN_ALLOW_ENV_REVEAL=1` is set in the agent environment (default off). The Kubernetes RBAC grant for `secrets` and `configmaps` read is split into an opt-in overlay `deploy/local/k8s/21-agent-rbac-reveal.yaml`; the default agent RBAC only grants `pods/log`. This two-key design (agent env flag + RBAC overlay) means a compromised control plane cannot force secret disclosure without explicit agent-side opt-in.

The same gate applies to `GetSecret`: secret data bytes (`Data`) are only returned when the caller is admin AND the agent has `LOTSMAN_ALLOW_ENV_REVEAL=1`. x509 certificate metadata (`CertInfo`) parsed from `kubernetes.io/tls` secrets is always returned regardless of the reveal gate.

## UI Admin Surface

- `useAuth()` exposes `isAdmin` derived from the `/auth/me` `is_admin` field.
- `layout-shell.tsx` shows an admin-only "Access" nav item when `isAdmin` is true.
- `/admin/rbac` page (`ui/app/admin/rbac/page.tsx`): read-only binding matrix + effective-permissions lookup by GitHub login.

UI gating is **convenience-only** — all real enforcement is server-side.

## Known Limitations

- **Group membership is a login-time snapshot** — stale until re-login. A webhook-invalidation mechanism would eliminate the window but is deferred.
- **`fetchGitHubGroups` has no integration test** — requires a fake-GitHub HTTP server harness (follow-up).
- **Bindings are config-file-only, not persisted** — no runtime grant/revoke; must restart the server to change bindings. A `bindings` Postgres table and mutation API are the next auth step (blocked on Postgres store). See [[Persistence and State]].
- **Pod logs returned unscrubbed** — `CanView` gates access but no secret-pattern redaction is applied to log content.
- **mTLS for gRPC agent transport** — still open under ADR-0002 (separate feature). Agent enrollment is fail-closed as of the 2026-07-11 campaign, but the transport itself remains plaintext (`insecure.NewCredentials()`).
- **Session revocation is in-memory per-replica** — the 2026-07-11 campaign's sliding-session lineage fix closes the "revoked-but-still-sliding" escape, but a durable/HA-shared revocation store (Redis/PG) is still open.

## Open Items

- [ ] Persist bindings to Postgres (`bindings` table) + runtime grant/revoke admin API.
- [ ] Fake-GitHub test harness for `fetchGitHubGroups`.
- [ ] Pod log content scrubbing (secret-pattern redaction).
- [ ] mTLS for gRPC agent link (ADR-0002).
- [ ] Durable/HA session revocation store (Redis/PG-backed).

## Relationships and Context

- **Parent concept:** [[Lotsman]]
- **Related:** [[Persistence and State]], [[UI Design System]]
- **Implementation reports:** [[Feature Platform Foundation 2026-06-21]], [[Feature Pod Inspection 2026-06-21]], [[Feature Kubernetes Resource Inspection 2026-06-22]]
- **Strong RBAC feature report:** [[Feature Strong RBAC Config-Driven 2026-06-24]]
- **Improve pass report:** [[Improve Engine Hardening and CVE Remediation 2026-06-24]]
- **Backlog campaign report:** [[Backlog Improvement Campaign Waves 0-3 2026-07-11]] (sliding sessions, rate limiting, fail-closed agent auth)
- **Sources:** `internal/auth/`, `internal/rbac/`, `docs/adr/0007-auth-github-oauth.md`
