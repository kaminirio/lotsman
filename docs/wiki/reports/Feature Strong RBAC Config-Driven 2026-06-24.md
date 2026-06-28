---
title: "Feature Strong RBAC Config-Driven 2026-06-24"
type: report
tags: [report, auth, rbac, feature, security]
created: 2026-06-24 16:14:46
updated: 2026-06-24 16:14:46
status: final
flow: feature
---

# Feature Strong RBAC Config-Driven 2026-06-24

## Summary

Config-driven "strong RBAC" replaces the old global-viewer bootstrap for SSO-authenticated users. Subject and group bindings are declared in `LOTSMAN_SSO_CONFIG`; access is **deny-by-default** for any user whose login/groups do not match an explicit binding. The feature covers the full stack: config schema, enforcer resolution, GitHub group fetch at login, a read-only admin inspector API, and a matching UI admin page. 293 tests pass under `go test ./... -race`; build/vet/gofmt clean. Not committed — pending user review.

## Details

### Config schema (`internal/auth/sso_config.go`)

`SSOConfig` gained two new binding slices:

| Field | Type | Notes |
|---|---|---|
| `bindings` | `[]Binding` | `{subject, role, cluster, namespace}` — subject is a GitHub login |
| `group_bindings` | `[]GroupBinding` | `{group, role, cluster, namespace}` — group is an org slug ("acme") or team slug ("acme/team") |

Validation rules enforced by `ParseSSOConfig`:
- `role` must be one of `admin`, `operator`, `viewer`.
- `cluster` must be non-empty; use `"*"` for a global (all-cluster) binding.
- `namespace` may be empty, which means all namespaces within the specified cluster.
- GitHub OAuth scope now requests `read:org` to support group membership queries.

### Enforcer resolution (`internal/auth/auth.go` — `Enforcer()`)

Single function that produces the effective `rbac.Enforcer` for a request:

| Condition | Granted |
|---|---|
| SSO disabled OR anonymous provider | Global admin (local-dev pass-through, unchanged) |
| `init_admin` login | Global admin |
| All other users | Union of matching subject bindings + matching group bindings from the JWT `Groups` claim |
| No binding matches | Deny everything (DENY-BY-DEFAULT) |

The old global-viewer fallback is removed. Key fix during code review: anonymous pass-through is now keyed on the **provider** being anonymous, not on the login string being `"anonymous"` — prevents a crafted `login=anonymous` token from gaining admin.

### Group membership (`internal/auth/session.go`, `oauth_handlers.go`)

- `SessionClaims` and `User` gained `Groups []string`.
- Groups are fetched at GitHub OAuth callback via `GET /user/orgs` + `GET /user/teams` (only executed when `group_bindings` are configured).
- Membership is embedded in the session JWT; it is a **login-time snapshot** refreshed on re-login (~8 h expiry). A stale membership window is a documented limitation.
- `read:org` failure degrades gracefully to an empty groups slice — user can still log in but will not match any group binding.

### Login gate (`sso_config.go` — `IsLoginAllowed`)

A user may complete the GitHub OAuth callback if they are:
1. On the static allowlist (`allowed_logins`), OR
2. Named as a `subject` in any binding, OR
3. A member of any group named in `group_bindings`.

Login by itself grants no access — the enforcer still applies deny-by-default on every API call.

Critical fix: groups are now fetched **before** the gate is evaluated so that group-only users are not locked out.

### Cluster-wide vs namespace-scoped binding distinction (`internal/rbac/rbac.go`)

Pre-existing `CanAccess` had a bug where an empty query namespace (meaning "all namespaces in a cluster") was granted by any binding for that cluster, including namespace-scoped ones. Fixed:

- A cluster-wide query (empty namespace) is now granted **only** by a binding with wildcard namespace.
- `CanViewCluster` added for cluster enumeration: a namespace-scoped viewer still sees their cluster listed in `/api/v1/clusters`, but `handleListNodes` remains strict (requires full-cluster view).

### Admin inspector API

Two new read-only endpoints (both require admin):

| Endpoint | Description |
|---|---|
| `GET /api/v1/admin/rbac/config` | Sanitized binding list — never includes secrets or JWT keys |
| `GET /api/v1/admin/rbac/effective?user=<login>` | Computed bindings for a given login + `is_admin` flag |

Returns 401 if unauthenticated, 403 if not admin. Config is **immutable at runtime**; the inspector is read-only by design (management surface becomes a mutation API once the Postgres store lands).

`GET /auth/me` now also returns `is_admin` (bool) and `groups` (non-null string array).

### UI changes

| File | Change |
|---|---|
| `ui/lib/auth.ts` | `AuthUser` gained `is_admin`, `groups` |
| `ui/lib/auth-context.tsx` | `useAuth()` exposes `isAdmin` |
| `ui/lib/api.ts` | Types updated for new `/auth/me` shape |
| `ui/components/layout-shell.tsx` | Admin-only "Access" nav item (visibility gated on `isAdmin`) |
| `ui/app/admin/rbac/page.tsx` | New read-only `/admin/rbac` page: binding matrix + effective-permissions lookup by login |

UI gating is **convenience-only** — real enforcement is server-side.

### Test coverage

- Deny-by-default (no matching binding).
- Per-cluster and per-namespace isolation.
- `_all` cluster-wide-grant requirement (the namespace sentinel fix).
- Operator-vs-viewer action gating.
- Group binding — org slug, team slug, case-folding.
- `init_admin` and SSO-disabled pass-through.
- Groups JWT round-trip.
- Admin inspector: 401/403/200 + no-secret-leak.
- `handleMe` `is_admin` field.
- Not covered: live `fetchGitHubGroups` HTTP call — needs a fake-server harness (deferred).

## Caveats and open items

- `fetchGitHubGroups` integration test missing — requires a fake GitHub HTTP server; noted as follow-up.
- Group membership snapshot in the JWT is stale until re-login (~8 h); a webhook-invalidation approach would eliminate the window but is deferred.
- Bindings are config-file-only; a runtime grant/revoke admin API and persistence to a `bindings` Postgres table are the next auth step (blocked on Postgres store completion).
- mTLS for the gRPC agent link remains an open item under ADR-0002 (separate from this feature).
- UI gating (`isAdmin` guard on nav) is aesthetic; server enforcement is the real gate.

## Related

- Affected concept: [[Authentication and RBAC]]
- Project: [[Lotsman]]
- Prior auth work: [[Feature Platform Foundation 2026-06-21]]
- Hardening context: [[Improve Engine Hardening and CVE Remediation 2026-06-24]]
- Persistence dependency: [[Persistence and State]]
