---
title: "LLM Incident Explainer"
type: concept
tags: [concept, llm, ollama, incidents, analyze]
created: 2026-06-22 14:31:21
updated: 2026-06-22 14:31:21
status: current
aliases: ["Explainer", "Ollama Explainer", "analyze"]
---

# LLM Incident Explainer

## Overview

An optional, off-by-default layer in `internal/analyze` that calls a self-hosted language model (gemma3:4b via Ollama, in-cluster) to produce a plain-English explanation of a detected incident. The [[Correlation Engine]] and its deterministic ranker remain authoritative; the LLM output is strictly assistive and explicitly labelled as such. No data leaves the cluster.

## Design Principles

- **Off by default** — feature is disabled unless `LOTSMAN_OLLAMA_URL` is set; returns 503 when unconfigured. Never on the detection hot path.
- **Grounded prompt** — the prompt is built exclusively from the incident's own hypotheses and timeline signals; the model is instructed not to infer events absent from the data. Prevents hallucination about unrelated cluster state.
- **Never authoritative** — the deterministic ranker's output drives alerting, SLO tracking, and investigations. The LLM explanation is a human-readable gloss, labelled "assistive" in the UI.
- **In-cluster only** — Ollama runs as a Pod in the `lotsman` namespace; no external API calls; data-sovereignty preserving.

## Components (`internal/analyze`)

| Symbol | Purpose |
|---|---|
| `Explainer` interface | `Explain(ctx, incident model.Incident) (*Explanation, error)` |
| `OllamaExplainer` | HTTP client for Ollama `/api/generate`; posts `format:"json"`, `stream:false` |
| `Explanation` | `Summary`, `Category`, `Confidence`, `Model` string fields |
| `NoopExplainer` | Returns `ErrNotConfigured`; wired when `LOTSMAN_OLLAMA_URL` is unset |

## REST Endpoint

```
POST /api/v1/incidents/{id}/explain
-> 200 {summary, category, confidence, model}
-> 503 when unconfigured
```

RBAC-gated by `CanView` on the incident's cluster/namespace (same as `handleGetIncident`).

## Configuration

| Env var | Default | Notes |
|---|---|---|
| `LOTSMAN_OLLAMA_URL` | unset | Feature disabled when absent |
| `LOTSMAN_OLLAMA_MODEL` | `gemma3:4b` | Any Ollama-compatible model name |

## Model and Deployment

- **gemma3:4b** — CPU-viable on commodity hardware (fits in ~4 GB RAM); deployed as an Ollama Pod, service `ollama.lotsman.svc.cluster.local:11434`.
- Typical latency: 4-8 s on CPU-only nodes.
- Fails open (503) if Ollama is unreachable at request time.

## Known Limitations

- No prompt token budget enforcement; very long incident timelines may exceed the model's context window.
- No response streaming; endpoint blocks until model completes.
- Explanations are not cached; every request re-invokes the model.
- Malformed model JSON returns 500 rather than a degraded fallback.

## Relationships & Context

- **Parent concept:** [[Lotsman]]
- **Related concepts:** [[Correlation Engine]] (authoritative ranker), [[Authentication and RBAC]] (endpoint gating)
- **Implementation report:** [[Feature LLM Incident Explainer 2026-06-22]]
- **Sources:** `internal/analyze/`, `internal/api/` (`handleExplainIncident`)
