# ADR-0002 — Agent link: gRPC bidi stream over mTLS

**Status:** Accepted — implemented (`internal/agentlink`: `Gateway`, `Dialer`, `Link`)

## Context
The agent↔control-plane channel must carry two traffic shapes at once: request/response
(the control plane proxying source queries down) and server push (the agent streaming
Kubernetes/ArgoCD watch events up). It must be efficient over a single long-lived
connection the agent opens outbound, and strongly authenticated.

## Decision
A single **gRPC bidirectional stream** per agent, secured with **mTLS**, defined in
[`proto/lotsman.proto`](../../proto/lotsman.proto). One `Connect` stream multiplexes
`Query`/`QueryResult` and pushed `Event`s. The UI↔control-plane channel stays **REST +
SSE** — gRPC is only for agents.

The Go code models the link transport-free (`internal/agentlink`: `Link`, `Gateway`,
`Dialer`) so nothing above it imports gRPC.

## Consequences
- One connection, multiplexed; native streaming; strong typing via protobuf.
- mTLS gives mutual auth and identity without inbound exposure.
- The gRPC transport is implemented: the generated protobuf/codegen step is wired,
  and `internal/agentlink` provides the `Gateway` (control-plane side) and `Dialer`
  (agent side) with enrollment-token auth, keepalive, and reconnect. Bearer-token
  auth ships today; per-agent mTLS identity is the remaining hardening item
  (tracked as SEC-1) — the `insecure.NewCredentials()` seams mark where it slots in.
- Two protocols to operate (gRPC for agents, REST for UI) — acceptable; they have
  different needs.
