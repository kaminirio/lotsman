# ADR-0011 — First-party username/password auth + multi-provider SSO

**Status:** Accepted

## Context

The original auth model ([ADR-0007](0007-auth-github-oauth.md)) was GitHub-only and,
critically, **fell open to an anonymous global admin** whenever `LOTSMAN_SSO_CONFIG` was
unset (the local-dev path). `auth.Anonymous()` + the `!m.enabled` short-circuit in
`Manager.Enforcer` handed every unauthenticated request full admin. That is fine for a
laptop demo but a latent "config typo in production → unauthenticated admin" exposure, and
it blocks any deployment that wants real accounts without wiring GitHub OAuth.

We also want more than GitHub: operators asked for Google and Azure AD (Entra) SSO, and for
first-party local accounts so a self-hosted install works with **no** external IdP.

## Decision

Replace the anonymous model with **admin-provisioned first-party accounts + optional
GitHub/Google/Azure SSO**. Local username/password auth is **always on**; there is no
"auth disabled" mode and **no anonymous access path** — every non-allowlisted request must
carry a valid session or gets `401`.

### Identity model

- A `store.User` is the sole principal: `{id, username, email, password_hash (bcrypt),
  is_admin, active, sso_provider, sso_subject, timestamps}`. Migration
  `0003_users.sql` (idempotent, `lower()` unique indexes on username/email).
- **Admin is an account attribute** (`is_admin`), not a config binding. `Manager.Enforcer`
  synthesizes a global admin binding for `is_admin` accounts; non-admins keep the existing
  config-driven RBAC (`CanView`/`CanInvestigate` via subject/group bindings). The
  anonymous/`!enabled` admin short-circuit is deleted.
- **Bootstrap admin**: `auth.EnsureBootstrapAdmin` idempotently seeds the first admin from
  `LOTSMAN_ADMIN_USER` / `LOTSMAN_ADMIN_PASSWORD` on every startup (create-if-missing,
  else force active+admin; the password is left untouched so an operator rotation survives
  restarts). No open self-signup.

### Session strategy

Unchanged from ADR-0007: HS256 JWT in an HttpOnly `lotsman_session` cookie
(`MintSession`/`VerifySession`, 8h TTL), with an in-memory JTI revocation denylist so
logout is effective before expiry. `CurrentUser` now additionally loads the account and
rejects the request if it is missing or inactive.

### Provider registry

`auth.Provider` abstracts an OAuth/OIDC IdP: `AuthCodeURL`, `Exchange`, and
`FetchIdentity → {email, subject, verified, displayName}`. Three adapters:
- **GitHub** — `/user` + `/user/emails` (verified primary email).
- **Google** — OIDC `openidconnect.googleapis.com/v1/userinfo`.
- **Azure/Entra** — v2 endpoints under `login.microsoftonline.com/{tenant}` +
  `graph.microsoft.com/oidc/userinfo`.

The enabled set is built from flat env: a provider is active **iff** its client id + secret
(and tenant, for Azure) are non-empty. `GET /auth/providers` reports
`{local:true, github, google, azure}`; each `GET /auth/login/{provider}` 404s unless
configured.

### SSO account-mapping rule (callback)

On callback, fetch the provider's **verified** email (unverified/empty → deny
`?error=no_account`). Then:
- Local account with that email exists and **active** → link (`sso_provider`/`sso_subject`)
  and log in.
- No account → **auto-provision an active, non-admin account** *only if* the email domain
  is in `LOTSMAN_AUTH_ALLOWED_DOMAINS` (comma-separated); otherwise deny
  `?error=no_account`.
- Account exists but **inactive** → deny `?error=inactive`.

### Flat-env configuration

The `LOTSMAN_SSO_CONFIG` JSON blob is **superseded** by flat env vars (the parser is kept
only for backward-compatible tests and non-admin RBAC bindings):
`LOTSMAN_SESSION_SECRET` (≥32 chars; empty → random **ephemeral** secret + WARN that
sessions won't survive a restart), `LOTSMAN_BASE_URL`, `LOTSMAN_UI_URL`,
`LOTSMAN_ADMIN_USER`, `LOTSMAN_ADMIN_PASSWORD`, `LOTSMAN_AUTH_ALLOWED_DOMAINS`, and per
provider `LOTSMAN_OAUTH_{GITHUB,GOOGLE,AZURE}_CLIENT_ID/SECRET` (+ `_AZURE_TENANT`).

### API surface

- `POST /auth/login {username,password}` → 200 + cookie / 401 `invalid credentials`.
  Allowlisted (reachable without a session) but still CSRF-guarded (`X-Requested-With`).
- `GET /auth/me` → 200 `{login,name,email,provider,is_admin,groups}` / 401.
- `GET /auth/login/{provider}` → 302; `GET /auth/callback/{provider}` → applies the mapping
  rule, sets cookie, 302 to the UI (`/` on success, `/login?error=…` on failure).
- Admin-gated user management (mirrors the enrollment-token handlers): `GET/POST
  /api/v1/users`, `PATCH`/`DELETE /api/v1/users/{id}`, with a **last-active-admin /
  self-lockout guard** returning 409.

## Consequences / blast radius of removing anonymous

- **DIRECT mode / local dev** no longer implies open admin. `make run-server` now exports
  `LOTSMAN_ADMIN_USER=admin`, `LOTSMAN_ADMIN_PASSWORD=admin`, and a dev
  `LOTSMAN_SESSION_SECRET` so there is a working login out of the box.
- **CLI**: `lotsman login` prompts for a password (no echo), POSTs `/auth/login`, and caches
  the `lotsman_session` cookie at `~/.config/lotsman/session.json` (0600). The
  `cluster-token` client auto-loads that cookie and now always sends the `X-Requested-With`
  CSRF header (mutations are enforced for everyone).
- **Tests**: every test that asserted anonymous=admin was rewritten to authenticate a real
  account (or to assert 401). The `store.Store` interface grew user CRUD implemented on both
  the in-memory and Postgres stores.
- Users work on **both** stores, but only the bootstrap admin is re-seeded on an ephemeral
  store, so provisioning a non-bootstrap account there logs a WARN.
- mTLS/agent enrollment ([ADR-0010](0010-per-cluster-enrollment-tokens.md)) is unchanged;
  this ADR only governs the human/UI/CLI auth plane.
