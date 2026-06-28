# ADR-0008 — Change-first correlation as the core heuristic

**Status:** Accepted

## Context
The product's value is explaining *why* something broke, not drawing charts. Across
incident causes, the single highest-precision signal is **"what changed?"** — most
production incidents follow a deploy, rollout, or config change. This is Komodor's
"change intelligence", and it is what differentiates Lotsman from a dashboard.

## Decision
Make the engine **change-first**. Every signal is normalized to
`(ResourceRef, timestamp)`. The correlator builds a per-resource timeline across all
sources; the ranker then scores **deploy/rollout changes shortly before the incident**
as the top hypotheses (confidence decaying with time distance), followed by resource-
pressure (OOM/eviction) and dominant log-error patterns. ArgoCD change events are
therefore first-class and persisted ([ADR-0004](0004-query-through-telemetry.md)).

## Consequences
- High-signal root-cause suggestions out of the box; the "deploy X caused this" story
  is the default, not an afterthought.
- Depends on a reliable change feed (ArgoCD adapter + durable change history).
- The ranker is intentionally simple and rule-based first; it is the natural place to
  add statistical/ML scoring later, behind the same `Hypothesis` output.
