---
title: "Feature First-Party Auth 2026-07-01"
type: report
tags: [report, feature, security, auth, oauth, rbac]
created: 2026-07-01 11:00:02
updated: 2026-07-01 11:00:02
status: final
flow: feature
---

# Feature First-Party Auth 2026-07-01

## Summary

Replaced Lotsman's "anonymous request = global admin" auth model with **admin-provisioned
first-party username/password accounts** plus optional **GitHub / Google / Azure-Entra
OAuth SSO** — governed by [ADR-0011](../../adr/0011-first-party-auth.md) (supersedes the
GitHub-only model of ADR-0007). Local auth is now **always on**: every request except a
small allowlist requires a valid session and gets `401` otherwise; there is no
"SSO unconfigured → anonymous admin" fallback left anywhere in the code. Admin is now a
per-user database attribute (`users.is_admin`), not a config binding. 417 Go tests pass
(up from 383), plus Postgres integration tests and a clean UI build.

## Details

### New/changed identity model

- `store.User`: `{id, username, email, password_hash (bcrypt cost 12), is_admin, active,
  sso_provider, sso_subject, created_at, updated_at}`. New migration
  `internal/store/migrations/0003_users.sql` — works on both `store.Memory` and
  `store.Postgres`; case-insensitive unique indexes on `lower(username)` / `lower(email)`.
- `internal/auth/auth.go` — package doc now states the always-on model explicitly. The old
  `!m.enabled` / `auth.Anonymous()` global-admin short-circuit is **deleted**.
  `Manager.Enforcer(u User)` (`internal/auth/auth.go:204-226`) is the single admin-resolution
  point: `u.IsAdmin` (sourced from `store.User.IsAdmin`, never from the JWT) synthesizes
  `rbac.Binding{Role: RoleAdmin, Cluster: Wildcard}`; non-admins still get the union of
  config `Bindings`/`GroupBindings` matched by login/groups, deny-by-default as before.
- `Manager.CurrentUser` (`internal/auth/auth.go:167-191`) requires BOTH a valid,
  non-revoked session cookie AND a matching **active** account row in the store — no
  anonymous fallback path exists.
- Bootstrap admin: `internal/auth/bootstrap.go` `EnsureBootstrapAdmin` runs on every startup,
  idempotently seeding/re-promoting the account named by `LOTSMAN_ADMIN_USER` /
  `LOTSMAN_ADMIN_PASSWORD` (create-if-missing; if it exists, force `active=true,
  is_admin=true` but leave the password hash untouched so an operator's password rotation
  survives restarts). Blank username/password is a no-op — no open self-signup.

### Provider registry (`internal/auth/providers.go`)

`Provider` interface: `AuthCodeURL`, `Exchange`, `FetchIdentity → Identity{Email, Subject,
Verified, DisplayName}`. Three adapters, each enabled only when its flat-env creds are
non-empty (`buildProviders`, `providers.go:120-135`):

| Provider | Identity source | Notes |
|---|---|---|
| GitHub | `/user` + `/user/emails` | Trusts only `primary && verified` (falls back to any verified) email |
| Google | OIDC `openidconnect.googleapis.com/v1/userinfo` | `email_verified` claim always present → trusted only when explicitly `true` |
| Azure/Entra | `login.microsoftonline.com/{tenant}/oauth2/v2.0` + `graph.microsoft.com/oidc/userinfo` | See tenant-pinning below |

**Azure tenant pinning (security-critical, `providers.go:53-67`):** `validAzureTenant`
rejects an empty tenant and the multi-tenant authorities `common`/`organizations`/
`consumers` — the provider is simply not built (not offered) in those cases. Reason:
`graph.microsoft.com/oidc/userinfo` does not return `email_verified`, so under a shared
authority an attacker could sign in from an arbitrary tenant with a self-set email claim
and get it silently trusted. `fetchOIDCUserinfo(..., trustDirectoryEmail bool)`
(`providers.go:260-288`) trusts the ABSENT-claim case only for Azure (`true`), and only
because the tenant is pinned to a directory the operator controls; Google passes `false`
(claim is always present, so absence would be anomalous). An **explicit**
`email_verified:false` is always honored (denied) for both, regardless of the flag.

### SSO account-mapping rule (`internal/auth/oauth_handlers.go:174-258`, `resolveSSOUser`)

On callback: unverified/empty email → deny (`?error=no_account`). Otherwise, in order:

1. **Returning identity** — look up by `(provider, subject)` FIRST (not email). Found +
   active → log in. Found + inactive → deny `?error=inactive`. This means an email change
   on the provider side can never strand a linked account.
2. **Existing local account with that verified email**, not yet linked → bind
   `sso_provider`/`sso_subject` to it and log in (first-link).
3. **Existing local account already linked to a DIFFERENT `(provider, subject)`** — denied
   (`?error=no_account`), not silently re-linked. This closes the cross-provider /
   email-only account-hijack vector.
4. **No local account** — auto-provision an **active, non-admin** account only if the email
   domain is in `LOTSMAN_AUTH_ALLOWED_DOMAINS`; otherwise deny.

### Flat-env configuration (supersedes `LOTSMAN_SSO_CONFIG` for admin/session config)

`LOTSMAN_SESSION_SECRET` (≥32 chars; empty → random ephemeral secret + WARN, session
doesn't survive restart — `internal/auth/auth.go:114-128`), `LOTSMAN_BASE_URL`,
`LOTSMAN_UI_URL`, `LOTSMAN_ADMIN_USER`, `LOTSMAN_ADMIN_PASSWORD`,
`LOTSMAN_AUTH_ALLOWED_DOMAINS`, `LOTSMAN_OAUTH_{GITHUB,GOOGLE,AZURE}_CLIENT_ID/SECRET`,
`LOTSMAN_OAUTH_AZURE_TENANT` (wired in `internal/config/config.go:73-151`). The
`LOTSMAN_SSO_CONFIG` JSON parser (`sso_config.go`) is kept only for the non-admin
subject/group RBAC bindings and backward-compatible tests — it no longer controls
whether auth is "enabled".

### API surface

| Route | Method | Notes |
|---|---|---|
| `/healthz`, `/api/v1/version`, `/auth/providers` | any | Allowlisted, no session needed |
| `/auth/login` | POST | Allowlisted but still CSRF-guarded (`X-Requested-With`) — `internal/auth/oauth_handlers.go:46-77` |
| `/auth/login/{provider}` | GET | 302 to IdP; 404 if provider not configured |
| `/auth/callback/{provider}` | GET | Applies the mapping rule; sets cookie; 302 to `/` or `/login?error=…` |
| `/auth/me` | GET | `{login,name,email,provider,is_admin,groups}` / 401 |
| `/api/v1/users` | GET, POST | Admin-gated, list/create accounts |
| `/api/v1/users/{id}` | PATCH, DELETE | Admin-gated update/delete |

`isUnprotected` (`internal/auth/middleware.go:53-62`) is the exact allowlist; every other
`/api/v1/*` route requires `CurrentUser` to resolve, else `401`. Every mutating method
(everywhere, including the allowlisted `/auth/login` and `/auth/logout`) requires
`X-Requested-With` or gets `403`.

### Last-admin / self-lockout guard (`internal/api/users.go:271-290`, `guardLastAdmin`)

`PATCH`/`DELETE` that would demote or deactivate an admin is checked in two layers: the
handler pre-check (precise `409` message, plus a **self-lockout** rail — an admin cannot
strip their own admin access even when other admins exist) and an **atomic store-level
recheck** (`store.UserPatch.GuardLastActiveAdmin`, `CountActiveAdmins`) so a concurrent
demotion of a different admin can't race two independent requests down to zero admins.
Implemented identically on `store.Memory` and `store.Postgres`.

### CLI, Makefile, UI

- `cmd/lotsman/login.go` — `lotsman login` prompts for a password (no terminal echo via
  `golang.org/x/term`), `POST /auth/login`, caches the `lotsman_session` cookie plus API URL
  and username at `~/.config/lotsman/session.json` (mode `0600`,
  `cmd/lotsman/session.go:27-40`).
- `cmd/lotsman/clustertoken.go` now auto-loads the cached cookie
  (`loadCachedCookie()`) and always sends `X-Requested-With` — mutations are enforced for
  everyone, including the CLI.
- `Makefile` `run-server` now exports `LOTSMAN_ADMIN_USER=admin`,
  `LOTSMAN_ADMIN_PASSWORD=admin`, and a dev `LOTSMAN_SESSION_SECRET` so local dev has a
  working login out of the box instead of the old anonymous-admin pass-through.
- UI: new `ui/app/login/page.tsx` and `ui/app/admin/users/page.tsx`; `ui/lib/auth.ts` /
  `ui/lib/auth-context.tsx` lost the synthetic-anonymous branch — `useAuth()` now reflects
  a real `/auth/me` 401 as logged-out, driving a redirect to `/login`.

### Test coverage added

`internal/auth/{bootstrap,login,password,providers,rbac_resolution}_test.go`,
`internal/api/{users_http,router_middleware}_test.go`,
`internal/store/{user_memory,postgres_user}_test.go`. Coverage includes: SSO mapping
branches (link/deny/auto-provision/hijack-deny), middleware-401-on-everything-else,
bootstrap idempotency (create vs re-promote, password left untouched), the atomic
last-admin guard under both stores, per-provider identity fetch (GitHub email fallback
chain, Google/Azure OIDC userinfo), and the Azure directory-trust-vs-explicit-false
verification matrix. Total: 417 Go tests pass (was 383), plus Postgres integration tests
and a clean `next build`.

## Caveats & open items

- **No PKCE on the OAuth authorization-code flow** — CSRF protection is state-cookie only
  (`internal/auth/oauth_handlers.go:88-125`); acceptable for confidential-client
  server-side flow but a documented gap versus PKCE-hardened flows.
- **No API to manage non-admin RBAC bindings in the flat-env world** — `Bindings`/
  `GroupBindings` are still only sourced from the legacy `LOTSMAN_SSO_CONFIG` JSON blob;
  non-admin users remain deny-by-default with no runtime grant mechanism. Same open item as
  before this feature (see [[Authentication and RBAC]] → Open Items), now scoped more
  precisely: admin grant/revoke has an API (`/api/v1/users`), fine-grained
  view/investigate bindings do not.
- **`EnsureBootstrapAdmin` re-promotes/reactivates on every boot** — if an operator
  deliberately demotes or deactivates the `LOTSMAN_ADMIN_USER` account, the next restart
  silently restores it. Intentional (no-lockout guarantee) but worth flagging.
- **No CLI auth tests yet** — `cmd/lotsman/login.go` / `session.go` have no unit tests in
  this pass.
- **No logout button in the UI shell yet** — `POST /auth/logout` exists and is exercised by
  tests, but nothing in `ui/components/layout-shell.tsx` calls it yet.
- **Users on a non-durable (in-memory) store do not survive restart** except the bootstrap
  admin, which is idempotently re-seeded; `handleCreateUser` logs a WARN in that case
  (`internal/api/users.go:109-113`).
- Agent enrollment tokens (ADR-0010) and mTLS (ADR-0002) are unaffected — this feature only
  changes the human/UI/CLI auth plane, not the agent↔control-plane link.

## Related

- ADR: [ADR-0011 — First-party username/password auth + multi-provider SSO](../../adr/0011-first-party-auth.md) (supersedes [ADR-0007](../../adr/0007-auth-github-oauth.md) for the "who can authenticate" question)
- Affected concept: [[Authentication and RBAC]] (rewritten to describe this as current reality)
- Project: [[Lotsman]]
- Related report: [[Feature Per-Cluster Enrollment Tokens 2026-06-28]] (agent-side auth, unaffected by this change)
