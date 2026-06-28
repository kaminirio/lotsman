# ADR-0007 — Auth: GitHub OAuth + JWT sessions + RBAC

**Status:** Accepted

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
- Scaffold ships the interface and anonymous fallback; OAuth/JWT/RBAC are `TODO(impl)`.
