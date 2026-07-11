# ADR-0010 — Per-cluster agent enrollment tokens

**Status:** Accepted

## Context

The original agent authentication model ([ADR-0001](0001-topology-agent-control-plane.md),
[ADR-0002](0002-agent-link-grpc.md)) used a single shared secret (`LOTSMAN_AGENT_TOKEN`)
that both the control plane and every agent were configured with. The gateway accepted any
`Hello` whose `Token` field matched that one string.

This model had three concrete weaknesses:

1. **No per-cluster revocation.** Revoking access for one cluster required rotating the
   shared secret across every agent and the control plane simultaneously — an operational
   event, not a targeted revocation.
2. **Cluster-name spoofing.** Any agent holding the shared token could claim any cluster
   name in its `Hello`. A compromised or misconfigured agent in cluster A could present
   itself as cluster B, hijacking that cluster's query path in the registry.
3. **Single point of exposure.** One plaintext secret in every deployment (control plane
   Secret + each agent Secret). Exposure of any one copy compromises the entire fleet.

## Decision

Replace the shared-token model with **per-cluster enrollment tokens**, hard cutover — no
shared-token or accept-any fallback remains.

- The `internal/enrollment` package mints tokens: `Generate()` produces an `lse_`-prefixed
  plaintext from 32 bytes of `crypto/rand` (256 bits entropy). Only the **SHA-256 hash**
  is persisted in the store ([ADR-0005](0005-postgres-state.md)); the plaintext is shown
  **once** at creation and never stored.
- Each token is **bound to a cluster name** at mint time. `Validator.ValidateEnrollment`
  enforces: not-found → reject, revoked → reject, expired → reject, and cluster mismatch →
  reject. Failure in any case produces a single generic error so the caller cannot
  distinguish "no such token" from "revoked" (no enumeration oracle). The gateway maps every
  non-nil error to gRPC `Unauthenticated`.
- `internal/config.Server` drops `AgentToken`; the gateway `NewGateway` takes a
  `TokenValidator` interface instead of a string. The agent side retains `LOTSMAN_AGENT_TOKEN`
  as the per-cluster token it presents in `Hello`.
- The store ([ADR-0005](0005-postgres-state.md)) gains an `EnrollmentToken` entity with
  CRUD: `SaveEnrollmentToken`, `GetEnrollmentTokenByHash`, `ListEnrollmentTokens`,
  `RevokeEnrollmentToken`. The Postgres schema is in
  `internal/store/migrations/0002_enrollment_tokens.sql`; in-memory parity is in
  `internal/store/memory.go`.
- The admin API exposes three **admin-gated** endpoints (see [ADR-0007](0007-auth-github-oauth.md)):
  `POST /api/v1/enrollment-tokens` (returns plaintext once, 201),
  `GET /api/v1/enrollment-tokens` (metadata only, never plaintext/hash),
  `POST /api/v1/enrollment-tokens/{id}/revoke` (204/404).
- `cmd/lotsman` gains `cluster-token generate|list|revoke` subcommands that wrap the API.
- The UI admin **Clusters** page (`ui/app/admin/clusters/page.tsx`) lists clusters and onboards
  a new one: mint → reveal-once → a ready-to-run `helm install lotsman-agent` command, plus the
  token list/revoke. (A `GET /api/v1/enrollment-defaults` endpoint supplies the gateway address +
  public chart reference for the generated command.)
- Helm charts updated: `lotsman-control-plane` no longer sets a shared token; each
  `lotsman-agent` release uses the per-cluster minted token (see [ADR-0009](0009-helm-packaging.md)).

**Onboarding flow:** mint a per-cluster token on the control plane (admin API or
`lotsman cluster-token generate <cluster>`) → install `lotsman-agent` in that cluster with
the token set as `agentToken.value` → the agent's `Hello{Cluster, Token}` is validated
against the issued token, bound to that cluster name.

## Consequences

- **Per-cluster revocation** is now a single API call; the effect takes hold on the agent's
  next reconnect (active streams are not forcibly terminated — they run until the next
  heartbeat/reconnect cycle).
- **Cluster-name spoofing is closed**: a valid token for cluster A cannot authenticate as
  cluster B.
- **Dev/local flow now requires minting**: running with multiple agents in direct mode is
  unaffected (no gateway), but agent-mode setups require at least one `cluster-token generate`
  call or admin API call before agents can connect.
- **A durable store is mandatory for agent onboarding.** Enrollment tokens are not
  re-derivable, so issuing them against the in-memory store would silently lock out every
  agent on the next control-plane restart. The control plane therefore enforces durability
  ([ADR-0005](0005-postgres-state.md)): `store.Store` exposes `Durable()` (Postgres → true,
  in-memory → false); the three admin endpoints return **503** (pointing at
  `LOTSMAN_DATABASE_URL`) and the gateway validator rejects every `Hello` when the store is
  ephemeral. A startup warning is logged in agent mode without a database. Set
  `LOTSMAN_DATABASE_URL` to enable enrollment. (Direct mode has no gateway and is unaffected.)
- **Expiry is optional** (TTL in hours, 0 = no expiry). Expiry and revocation both take
  effect on the next reconnect.
- **SHA-256 without salt** is acceptable here: the 256 bits of `crypto/rand` entropy in the
  token leave no viable dictionary or rainbow-table attack surface. Revisit if token length
  ever shrinks.
- mTLS ([ADR-0002](0002-agent-link-grpc.md)) remains the production-hardening path for the
  agent link; the per-cluster token then becomes a SPIFFE-style identity check rather than
  a shared secret. The `TokenValidator` interface keeps that swap local to `NewGateway`.
