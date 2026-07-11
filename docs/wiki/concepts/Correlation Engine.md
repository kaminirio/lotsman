---
title: "Correlation Engine"
type: concept
tags: [concept, architecture, investigation, scheduler, sse]
created: 2026-06-21 13:30:00
updated: 2026-07-11 17:58:00
status: current
aliases: ["Investigation Engine", "Change-First Ranking"]
---

# Correlation Engine

## Overview

Lotsman's defensible core (`internal/engine`): joins logs, metrics, change events, and Kubernetes events on `(ResourceRef, time)` to build an incident timeline and rank probable causes. Backend- and location-agnostic (ADR-0008). The engine now runs continuously via a scheduler; incidents are persisted to Postgres and streamed live over SSE.

## Core Principles & Mechanisms

- **Detectors → Correlator → Ranker.** Detectors open candidates (error-level k8s events, a PromQL threshold breach, a log-error burst); the correlator gathers all sources into one time-sorted timeline (tolerant of per-source failure); the ranker scores causes.
- **Change-first heuristic** — a deploy/rollout shortly before an incident is the top hypothesis ("what changed?", à la Komodor). `internal/engine/ranker.go`.
- **One identity** — `model.ResourceRef` (cluster→namespace→workload→pod) + timestamp; `model.ResourceFromLabels` normalizes Prometheus/Loki label sets onto it so signals from different systems line up.
- Reads everything through [[Source-Agnostic Adapters]]; providers are resolved via [[Agent Control Plane Topology]].

## Detector Scheduler (`internal/controlplane/scheduler.go`)

Runs continuously in the control plane (not just on demand):

1. Every `LOTSMAN_SCAN_INTERVAL` (default 30 s): calls `engine.ScanAndInvestigate(ctx, cluster)` — a single method that resolves the provider once, detects once, and investigates each candidate in one pass. Result incidents are upserted and published to the bus.
2. A bounded dedupe map prevents re-publishing an unchanged incident within the same scan window.
3. `handleInvestigate` (manual trigger via `POST /api/v1/incidents/:id/investigate`) uses the separate public `Scan` / `Investigate` methods (on-demand path) and also publishes to the bus so the SSE stream reflects on-demand triggers immediately.

### `Engine.ScanAndInvestigate` (hot path)

`ScanAndInvestigate(ctx, cluster) ([]model.Incident, error)` is the unified scheduler entry point introduced in the 2026-06-24 improve pass. It differs from calling `Scan` + `Investigate` in sequence:

- The source provider is resolved **once** per tick (not twice).
- Detectors run **once** against the resolved provider.
- Each candidate is investigated against the **same** provider snapshot.
- On `ctx` cancellation it returns `nil, ctx.Err()` — no partial incidents. The next tick re-scans from scratch; this is safe under the query-through persistence model (ADR-0004).

The public `Scan` and `Investigate` methods are preserved as API surface for the on-demand investigation path and tests.

## SSE Incident Bus (`internal/events/bus.go`, `internal/api/sse.go`)

- `bus.go`: in-process pub/sub over a buffered channel. `Publish` snapshots the subscriber list under the lock and releases it before sending — a blocked subscriber goroutine cannot stall detection. A per-subscriber mutex + closed-flag gate prevents send-on-closed-channel panics on concurrent disconnect.
- `sse.go` (`GET /api/v1/stream`): subscribes to the bus, writes `data: {json}\n\n` frames, sends `: heartbeat` comments every 15 s, and unsubscribes cleanly on client disconnect.

## Metrics in the Timeline (2026-07-11 campaign)

Metrics are now gathered into the correlated timeline: `Correlator.Timeline` queries `MetricSource` alongside logs, Kubernetes events, and change events (previously the metric source was never queried, so metric anomalies never reached the ranker). `ranker.go` gained a metric-anomaly hypothesis alongside the existing change-first and OOM/evicted heuristics, and (from Wave 3) a log-burst hypothesis. See [[Backlog Improvement Campaign Waves 0-3 2026-07-11]].

## Per-Source Timeout

`Correlator.Timeline` (`internal/engine/correlator.go`) now wraps each source call in its own `context.WithTimeout`, complementing the adapter-level HTTP timeouts in [[Source-Agnostic Adapters]] — one slow source can no longer stall the whole gather.

## Examples & Code

`internal/engine/{engine,correlator,ranker,normalizer}.go`, detectors in `internal/engine/detector/`, `internal/controlplane/scheduler.go`, `internal/events/bus.go`, `internal/api/sse.go`.

## Relationships & Context

- **Parent concepts:** [[Lotsman]]
- **Related topics:** [[Source-Agnostic Adapters]], [[Agent Control Plane Topology]], [[Persistence and State]]
- **Implementation report:** [[Feature Platform Foundation 2026-06-21]]
- **Improve pass report:** [[Improve Engine Hardening and CVE Remediation 2026-06-24]] (ScanAndInvestigate hot path, bus snapshot fix, ranker boundary tests)
- **Backlog campaign report:** [[Backlog Improvement Campaign Waves 0-3 2026-07-11]] (metrics wired into the timeline; per-source timeout; log-burst ranker hypothesis; watch-event push feeds the bus outside the scan tick — see [[Agent Control Plane Topology]])
- **Related concept:** [[LLM Incident Explainer]] (optional assistive layer; the engine's ranker output feeds the explainer prompt)
- **Relevant skills:** `golang-design-patterns`, `golang-concurrency`, `golang-testing` — see [[Development Skills]]
- **Sources:** `internal/engine/`, `internal/controlplane/scheduler.go`, `internal/events/bus.go`, `docs/adr/0008-correlation-change-first.md`
