# ADR-0002 — Agent link: gRPC bidi stream over mTLS

**Status:** Accepted

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
- Requires a protoc/buf codegen step (deferred; the scaffold uses transport-free stubs).
- Two protocols to operate (gRPC for agents, REST for UI) — acceptable; they have
  different needs.
