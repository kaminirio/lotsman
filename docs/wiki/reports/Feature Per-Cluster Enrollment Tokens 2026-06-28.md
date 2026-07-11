---
title: "Feature Per-Cluster Enrollment Tokens 2026-06-28"
type: report
tags: [report, feature, security, agentlink, enrollment]
created: 2026-06-28 20:05:34
updated: 2026-06-28 20:05:34
status: final
flow: feature
---

# Feature Per-Cluster Enrollment Tokens 2026-06-28

## Summary

Replaced the single shared `LOTSMAN_AGENT_TOKEN` model with **per-cluster enrollment
tokens** — a hard cutover with no shared-token or accept-any fallback. Each cluster
receives its own `lse_`-prefixed token minted via the admin API or CLI; only the
SHA-256 hash is persisted; the plaintext is revealed once. The gateway validates
every agent `Hello` against the issued token with cluster binding enforced,
closing the cluster-name spoofing gap present in the prior model.

## Details

### New packages and files

| File | What |
|---|---|
| `internal/enrollment/enrollment.go` | `Generate()` mints `lse_`-prefixed token (32 bytes crypto/rand); `Hash()` SHA-256 hex; `Validator.ValidateEnrollment()` |
| `internal/enrollment/enrollment_test.go` | Unit tests: prefix, uniqueness, hash stability, all validation outcomes (6 cases) |
| `internal/store/migrations/0002_enrollment_tokens.sql` | Postgres table + cluster index |
| `internal/api/enrollment.go` | `handleCreateEnrollmentToken`, `handleListEnrollmentTokens`, `handleRevokeEnrollmentToken` |
| `internal/api/enrollment_http_test.go` | HTTP handler tests |
| `internal/store/enrollment_memory_test.go` | Memory store CRUD tests |
| `cmd/lotsman/clustertoken.go` | `cluster-token generate|list|revoke` CLI subcommands |
| `ui/app/admin/enrollment/page.tsx` | Admin UI: mint, reveal-once panel, token table, revoke |
| `internal/agentlink/enrollment_integration_test.go` | End-to-end: store → Validator → Gateway gRPC stream (see below) |

### Modified files

| File | Change |
|---|---|
| `internal/agentlink/gateway.go:36-61` | `NewGateway` takes `TokenValidator` interface instead of a string; `Connect` validates `Hello` via the interface before registering the link |
| `internal/config/config.go:14-20` | `Server.AgentToken` field removed; replaced with a comment describing the new model |
| `internal/store/memory.go` | `SaveEnrollmentToken`, `GetEnrollmentTokenByHash`, `ListEnrollmentTokens`, `RevokeEnrollmentToken` added |
| `internal/store/postgres.go` | Same four methods added; reads migration `0002_enrollment_tokens.sql` |
| `internal/api/router.go` | Three enrollment endpoints registered |
| `internal/controlplane/controlplane.go` | `NewGateway` call updated to pass `enrollment.NewValidator(store)` |
| `deploy/helm/lotsman-control-plane` | `agentToken` value removed |
| `deploy/helm/lotsman-agent` | `agentToken.value` is now the per-cluster token |
| `deploy/local/k8s/10-control-plane.yaml` | Removed `LOTSMAN_AGENT_TOKEN` env var |
| `deploy/local/k8s/21-agent.yaml`, `multicluster/cluster2-agent.yaml` | Comments updated to reference per-cluster token flow |

### Key design decisions

**Hard cutover, no fallback.** The gateway `Connect` handler validates via
`TokenValidator` only; there is no "if token not found, try shared secret" path.
Comments in `gateway.go:138-147` make this explicit.

**Cluster binding closes spoofing.** `enrollment.go:92-98` — after the hash matches,
`rec.Cluster != cluster` → `errClusterMismatch`. A token for `prod-eu` presented by
an agent claiming `staging` is rejected. This was the primary security gap in the old
model.

**No enumeration oracle.** `enrollment.go:82-85` — store lookup failure returns the
same generic `errUnauthorized` as revoked. The caller cannot distinguish "token not
found" from "token revoked" from "store error".

**One-time plaintext.** The 201 response body (`enrollment.go:101-108`) is the only
place the plaintext appears; the store never holds it; `handleListEnrollmentTokens`
deliberately omits both `token` and `hash` fields from the response DTO.

**SHA-256 unsalted is acceptable here** because the 256-bit `crypto/rand` entropy in
the token body makes dictionary and rainbow-table attacks infeasible. The decision is
commented in `enrollment.go:33-37`.

### Test coverage

| Test file | What is covered |
|---|---|
| `internal/enrollment/enrollment_test.go` | Generate uniqueness + prefix; Hash determinism; accept matching; reject wrong cluster, revoked, expired, unknown, empty |
| `internal/store/enrollment_memory_test.go` | Save, GetByHash, List, Revoke; not-found errors |
| `internal/api/enrollment_http_test.go` | Create (201 + plaintext), list (no hash), revoke (204/404); non-admin 403 |
| `internal/agentlink/enrollment_integration_test.go` | **Integration**: store → Validator → Gateway gRPC stream |

The integration test (`enrollment_integration_test.go`) uses `bufconn` to wire the
real `enrollment.Validator` (backed by `store.Memory`) into a live `Gateway` gRPC
server. Two test functions cover the full matrix:
- `TestEnrollmentValidatorIntegration` — 5 table-driven direct-validator cases
  (valid, wrong-cluster spoofing guard, revoked, expired, unknown).
- `TestGatewayIntegration_ValidTokenConnects` — full path to `onConnect` callback.
- `TestGatewayIntegration_ConnectRejections` — all 4 invalid cases produce
  `codes.Unauthenticated` on the gRPC stream.

## Caveats and open items

- **Durable store now REQUIRED for enrollment** — because in-memory tokens would
  vanish on restart and lock out every agent, the control plane refuses to mint
  (admin endpoints → 503) or validate (gateway rejects) tokens unless
  `LOTSMAN_DATABASE_URL` is set. Enforced via `store.Store.Durable()` (memory→false,
  Postgres→true). Direct mode is unaffected. Tracked in [[Persistence and State]]
  and [[Authentication and RBAC]].
- **Revocation is next-reconnect only** — active gRPC streams are not forcibly
  terminated when a token is revoked. Force-revocation would require stream
  cancellation in the Gateway's link registry, deferred.
- **CLI/UI auth for SSO-enabled control planes** — the `--cookie` flag threads a
  `lotsman_session` through the CLI. Token creation from the UI follows the same admin
  gating as the RBAC admin page. No OIDC/token-exchange path for non-interactive
  automation yet.
- **ADR-0002 mTLS** remains open. The `TokenValidator` interface makes that swap
  local to `NewGateway` when the mTLS CA lands.

## Related

- Affected concepts: [[Authentication and RBAC]], [[Agent Control Plane Topology]]
- Project: [[Lotsman]]
- ADR: `docs/adr/0010-per-cluster-enrollment-tokens.md`
