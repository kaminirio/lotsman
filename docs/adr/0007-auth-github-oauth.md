# ADR-0007 — Auth: GitHub OAuth + JWT sessions + RBAC

**Status:** Accepted — implemented (`internal/auth`, `internal/rbac`)

## Context
The control plane needs authenticated operators and authorization scoped to clusters/
namespaces. GitHub OAuth is a natural choice for developer-facing operator tooling:
no identity server to operate, and org/team membership is queryable, enabling
group-based RBAC bindings.

## Decision
Use **GitHub OAuth** (extensible to Google/Microsoft) with **HttpOnly JWT session
cookies** and a structured `SSO_CONFIG` shape, plus **RBAC** scoping visibility to
clusters/namespaces. The UI auth context follows the OAuth callback flow so login UX
integrates cleanly with the single-page app. With SSO unconfigured, every request is
treated as an anonymous admin (local dev pass-through).

## Consequences
- GitHub identity is familiar to operators; org/team slugs map directly to RBAC groups.
- HttpOnly cookies + a CSRF header (`X-Requested-With`) on mutations — no token storage
  in `localStorage`.
- Implemented in `internal/auth` (GitHub OAuth flow, JWT session cookies, group
  resolution) and `internal/rbac` (per-cluster/per-namespace bindings enforced on
  every resource handler). The anonymous local-dev fallback remains for an unset
  `LOTSMAN_SSO_CONFIG`; a present-but-invalid config now fails closed (the control
  plane refuses to start) rather than degrading to anonymous admin.
