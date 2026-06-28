---
title: "Feature LLM Incident Explainer 2026-06-22"
type: report
tags: [report, feature, llm, ollama, incidents, analyze]
created: 2026-06-22 14:31:17
updated: 2026-06-22 14:31:17
status: final
flow: feature
---

# Feature LLM Incident Explainer 2026-06-22

## Summary

An optional LLM-powered incident explanation layer was added (`internal/analyze`) — off by default, requiring explicit Ollama configuration. When enabled, `POST /api/v1/incidents/{id}/explain` calls a self-hosted language model (gemma3:4b via Ollama, deployed in-cluster) to produce a plain-English summary, category, confidence label, and model name. The deterministic correlation ranker remains authoritative; the LLM output is strictly assistive and labelled as such in the UI. No data leaves the cluster. See [[LLM Incident Explainer]] for the standing concept note.

## Details

### Package `internal/analyze`

| Symbol | Purpose |
|---|---|
| `Explainer` interface | `Explain(ctx, incident model.Incident) (*Explanation, error)` |
| `OllamaExplainer` struct | HTTP client for Ollama `/api/generate` endpoint |
| `Explanation` struct | `Summary string`, `Category string`, `Confidence string`, `Model string` |
| `NoopExplainer` | Returns `ErrNotConfigured`; used when `LOTSMAN_OLLAMA_URL` is unset |

`OllamaExplainer` is constructed via `NewOllamaExplainer(url, model string)`. It posts to `<url>/api/generate` with `format: "json"` and `stream: false`, then JSON-decodes the response text into `Explanation`.

### Prompt construction

The prompt is grounded in the incident's data to prevent hallucination about unrelated clusters:

- Incident resource ref (cluster/namespace/workload).
- All `Hypothesis` entries from `incident.Hypotheses` — each including its `Kind`, `Score`, and human-readable `Description`.
- All signals in `incident.Timeline` — kind, severity, message, and timestamp.
- A JSON schema hint in the system prompt so the model produces `{summary, category, confidence, model}`.

The model is instructed to treat the provided data as the sole source and not to infer events not present in the timeline.

### REST endpoint

```
POST /api/v1/incidents/{id}/explain
-> 200 {summary, category, confidence, model}
-> 503 {"error": "explainer not configured"} when LOTSMAN_OLLAMA_URL is unset
-> 404 if incident not found
-> 500 on Ollama call failure
```

RBAC: gated by `CanView` on the incident's cluster/namespace (same gate as `handleGetIncident`).

### Configuration

| Env var | Purpose | Default |
|---|---|---|
| `LOTSMAN_OLLAMA_URL` | Base URL of the Ollama server | unset (feature disabled) |
| `LOTSMAN_OLLAMA_MODEL` | Model name | `gemma3:4b` |

When `LOTSMAN_OLLAMA_URL` is unset, `NoopExplainer` is wired into `api.Config.Explainer` and every call returns 503.

### Model and deployment

- Model: **gemma3:4b** — chosen for CPU-viable inference on commodity hardware; fits in ~4 GB RAM.
- Deployed in-cluster as an Ollama Pod in the `lotsman` namespace; service `ollama.lotsman.svc.cluster.local:11434`.
- Control plane connects at startup; fails open (503) if Ollama is unreachable.
- No data is sent to external APIs; the model runs entirely in-cluster.

### UI

An "AI explanation (assistive)" panel appears on the incident detail page:

- Rendered below the correlation timeline and ranked hypotheses.
- Shows Summary, Category, Confidence label, and the model name.
- Panel is absent when the explainer is unconfigured (503 response hides the panel client-side).
- Labelled explicitly as assistive, with a note that the deterministic ranker is authoritative.

### Validation

Tested with a seeded incident (ArgoCD deploy ranked as probable cause) against the in-cluster gemma3:4b model:

- Response latency ~4-8 s on CPU-only nodes.
- Explanation correctly identified the deploy signal as likely cause; phrased it in plain English.
- The category and confidence fields populated correctly from model JSON output.

## Caveats & open items

- Model quality is CPU-constrained; gemma3:4b produces plausible but occasionally vague summaries. A GPU-accelerated or larger model would improve quality.
- Prompt token budget is not yet enforced — a very long incident timeline could exceed context window.
- No streaming; the endpoint blocks until the model responds. A future SSE variant would improve UX for long responses.
- Explanation is not cached — repeated requests to the same incident re-invoke the model each time.
- The JSON format instruction is best-effort; malformed model responses return 500 rather than a degraded explanation.

## Related

- New concept: [[LLM Incident Explainer]]
- Affected concepts: [[Correlation Engine]], [[Authentication and RBAC]]
- Project: [[Lotsman]]
