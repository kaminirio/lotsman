---
title: "Agent Control Plane Topology"
type: concept
tags: [concept, architecture, kubernetes, grpc]
created: 2026-06-21 13:30:00
updated: 2026-07-11 17:58:00
status: current
aliases: ["Agent Mode", "Direct Mode"]
---

# Agent Control Plane Topology

## Overview

How Lotsman reaches cluster signals: a lightweight in-cluster **agent** dials OUT to a central **control plane** (egress-only — NAT/firewall friendly, no inbound exposure). Komodor-style (ADR-0001, ADR-0002). The gRPC transport is now fully implemented and verified live; this is no longer a stub.

## Core Principles & Mechanisms

- **Agent dials out** — clusters expose nothing inbound; the agent is the single egress point.
- **gRPC bidi stream** — one long-lived stream multiplexes control-plane→agent query requests and agent→control-plane watch events (`proto/lotsman.proto`, `internal/agentlink`). Transport is insecure for local dev; a mTLS seam (`credentials.NewTLS`) is left for production (see [[Feature Platform Foundation 2026-06-21]] §caveats). Agent enrollment is now **fail-closed**: an empty `LOTSMAN_AGENT_TOKEN` previously accepted any non-empty token from a connecting agent (dev fallback), letting a rogue agent register any cluster name. It now rejects enrollment unless `LOTSMAN_AGENT_ALLOW_INSECURE` is explicitly set for local dev without a token. mTLS/per-cluster identity is still open — see [[Backlog Improvement Campaign Waves 0-3 2026-07-11]].
- **Watch-event push wired end to end** — the previously built-but-dead push path (`Dialer.WithEventFeed`/`pushLoop`, `gateway.dispatchEvent`, `Link.Events()`) is now live: the agent drains a Kubernetes **poll-feed** (not a true `client-go` informer/watch) and pushes events over the link; the scheduler now drains `registry.go`'s `link.Events()` so pushed signals reach the incident bus without waiting for the next scan tick. Detection is no longer poll-only at the 30 s scheduler cadence. See [[Backlog Improvement Campaign Waves 0-3 2026-07-11]].
- **Two modes, one engine** — *agent mode* (control plane holds a `remote.Provider` that proxies through the link) vs *direct mode* (`LOTSMAN_DIRECT_MODE=1`, control plane talks to one cluster's backends directly). The [[Correlation Engine]] cannot tell which, because both resolve to a `sources.Provider`.
- **Registry** — `internal/controlplane/registry.go` maps cluster name → Provider (direct or remote); implements `engine.ProviderResolver`. In agent mode the link is registered on `Hello` handshake and removed on disconnect (identity-guarded). The registry also implements `api.SourceRegistry` (a small interface: `Provider(cluster) → sources.Provider` + `Clusters() []string`) so the API layer resolves per-cluster providers without any direct-mode special-cases. The per-cluster `remote.Provider` wrapper is **memoized**: built once on first call (or rebuilt on reconnect) and evicted on disconnect, so multi-cluster scan cycles avoid repeated wrapper reconstruction.
- **Multi-cluster validated** — two simultaneous k3d clusters (`local` and `local-2`) were operated concurrently, each with their own agent gRPC stream, each returning distinct node/pod/workload inventories. See [[Feature Multi-Cluster and Mock Removal 2026-06-22]].

## gRPC Gateway (`internal/agentlink/gateway.go`)

Runs in the control plane as a gRPC server. On each `Connect` bidi stream:

1. Reads the first message as a `Hello` (contains agent identity + cluster name).
2. Builds a `Link` with a request-ID-correlated `Do(req) → resp` method for synchronous control-plane queries, plus an `Events` push channel for asynchronous agent→control-plane signals.
3. Registers the link in the cluster registry.
4. Defers `registry.Remove(clusterName)` — executed when the stream closes.

## gRPC Dialer (`internal/agentlink/dialer.go`)

Runs in the agent. On startup:

1. Dials the control-plane gRPC address; opens the `Connect` bidi stream.
2. Sends `Hello`.
3. Serves incoming query requests by dispatching to the local `sources.Provider` handler.
4. Sends periodic heartbeat pings.
5. Reconnects with exponential backoff on disconnect.

## RequestKind Enum (`internal/agentlink/kind.go`)

The `RequestKind` enum identifies the operation being proxied over the agent link. Each new `ClusterSource` method requires a new kind here (part of the six-places lockstep rule):

| RequestKind | Source method |
|---|---|
| `LIST_WORKLOADS` | `ClusterSource.ListWorkloads` |
| `EVENTS` | `ClusterSource.Events` |
| `QUERY_LOGS` | `LogSource.QueryLogs` |
| `QUERY_INSTANT` | `MetricSource.QueryInstant` |
| `QUERY_RANGE` | `MetricSource.QueryRange` |
| `CHANGE_EVENTS` | `DeploymentSource.ChangeEvents` |
| `LIST_PODS` | `ClusterSource.ListPods` (added in [[Feature Pod Inspection 2026-06-21]]) |
| `POD_LOGS` | `ClusterSource.PodLogs` (added in [[Feature Pod Inspection 2026-06-21]]) |
| `LIST_CONFIGMAPS` | `ClusterSource.ListConfigMaps` (added in [[Feature Kubernetes Resource Inspection 2026-06-22]]) |
| `GET_CONFIGMAP` | `ClusterSource.GetConfigMap` (added in [[Feature Kubernetes Resource Inspection 2026-06-22]]) |
| `LIST_SECRETS` | `ClusterSource.ListSecrets` (added in [[Feature Kubernetes Resource Inspection 2026-06-22]]) |
| `GET_SECRET` | `ClusterSource.GetSecret` (added in [[Feature Kubernetes Resource Inspection 2026-06-22]]) |
| `LIST_NODES` | `ClusterSource.ListNodes` (added in [[Feature Kubernetes Resource Inspection 2026-06-22]]) |

## Proto and Code Generation

- Source: `proto/lotsman.proto`.
- Config: `buf.yaml` + `buf.gen.yaml` at repo root.
- Generated output: `internal/agentlink/pb/`.
- Regen: see `proto/README.md`.

## ADR-0003 Boundary

grpc and pb imports are confined to `internal/agentlink`. `internal/engine`, `internal/api`, and `internal/model` remain transport-free. See [[Source-Agnostic Adapters]].

## Relationships & Context

- **Parent concepts:** [[Lotsman]]
- **Related topics:** [[Source-Agnostic Adapters]], [[Correlation Engine]]
- **Implementation reports:** [[Feature Platform Foundation 2026-06-21]], [[Feature Pod Inspection 2026-06-21]], [[Feature Kubernetes Resource Inspection 2026-06-22]], [[Feature Multi-Cluster and Mock Removal 2026-06-22]]
- **Improve pass report:** [[Improve Engine Hardening and CVE Remediation 2026-06-24]] (Registry memoization; mTLS deferral documented)
- **Backlog campaign report:** [[Backlog Improvement Campaign Waves 0-3 2026-07-11]] (fail-closed agent token enforcement; watch-event push path wired via poll-feed; mTLS still open)
- **Relevant skills:** `golang-grpc`, `golang-concurrency`, `golang-context` — see [[Development Skills]]
- **Sources:** `docs/adr/0001-topology-agent-control-plane.md`, `docs/adr/0002-agent-link-grpc.md`, `internal/agentlink/`
