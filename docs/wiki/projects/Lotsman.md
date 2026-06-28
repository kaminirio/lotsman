---
title: "Lotsman"
type: project
tags: [project, active, kubernetes, observability]
created: 2026-06-21 13:30:00
updated: 2026-06-24 16:14:46
status: active
timeline: [2026-06-21 -> ]
---

# Project: Lotsman

## Objectives

Self-hosted Kubernetes monitoring **and incident investigation** — a competitor to BetterStack and Komodor. The differentiator is *investigation*: correlate logs, metrics, and deploy/change events on one resource timeline and rank the **probable cause**, not just draw dashboards. Cloud- and environment-agnostic; first-target stack: Kubernetes + Loki + VictoriaMetrics + ArgoCD.

## Tech Stack & Architecture

- **Backend:** Go 1.26; all planned deps in use: `k8s.io/client-go`, `pgx/v5`, `grpc`, `oauth2`, `jwt/v5`.
- **UI:** Next.js 15 + React 19 + Tailwind v4 — Lens/Freelens-style dark console using the "Warm Operator" design system. See [[UI Design System]].
- **Architectural concepts:** [[Agent Control Plane Topology]] · [[Source-Agnostic Adapters]] · [[Correlation Engine]] · [[Persistence and State]] · [[Authentication and RBAC]] · [[LLM Incident Explainer]]
- **Source of truth:** `docs/ARCHITECTURE.md`, decision records under `docs/adr/`.

## Action Items

- [x] Implement concrete adapters (kubernetes/client-go, victoriametrics/PromQL, loki/LogQL, argocd) — direct mode live; 66 tests green (see [[Feature Source Adapter Implementation 2026-06-21]])
- [x] Postgres store (pgx) behind `store.Store` — 5 incidents persisted live; migrations embedded (see [[Feature Platform Foundation 2026-06-21]])
- [x] gRPC transport for `agentlink` (gateway/dialer) from `proto/lotsman.proto` — two-process agent↔server verified live
- [x] Detector scheduler + SSE incident bus — continuous scan every 30 s; live `/api/v1/stream` SSE verified
- [x] Auth: GitHub OAuth + JWT + RBAC — anonymous=admin default preserves dev UX
- [x] Pod inspection (list pods, container logs, env vars with reveal) — live on k3d via agent gRPC; env-var reveal opt-in with default-deny masking (see [[Feature Pod Inspection 2026-06-21]])
- [x] Full Kubernetes resource browser (ConfigMaps, Secrets, Nodes + extended Pod model with owner-chain) — all through source seam, validated on k3d (see [[Feature Kubernetes Resource Inspection 2026-06-22]])
- [x] x509 TLS certificate inspection — cert metadata always public; CertInfo parsed from kubernetes.io/tls secrets; Valid/Expiring/Expired badge
- [x] Lens/Freelens UI redesign — dark sidebar console, resource tables, detail pages with tabs, Pretty log viewer, Secret masking UX, Certificate panel; embedded-UI route-resolution bug fixed (see [[Feature Lens UI Redesign 2026-06-22]])
- [x] LLM incident explainer — optional Ollama/gemma3:4b layer in `internal/analyze`; off by default; `POST /api/v1/incidents/{id}/explain`; no data leaves cluster (see [[Feature LLM Incident Explainer 2026-06-22]])
- [x] Multi-cluster validated — two k3d clusters (local + local-2) simultaneous; `GET /api/v1/clusters` from live registry; `LOTSMAN_SEED=false` removes mock data in deployed envs (see [[Feature Multi-Cluster and Mock Removal 2026-06-22]])
- [x] Improve pass: engine test coverage, API hardening, hot-path perf, CVE remediation — 239 tests pass `-race`; `npm audit` 0 issues (see [[Improve Engine Hardening and CVE Remediation 2026-06-24]])
- [x] Config-driven strong RBAC: subject + group bindings in `LOTSMAN_SSO_CONFIG`; deny-by-default; cluster-wide vs namespace-scope fix; admin inspector API; UI admin page — 293 tests pass `-race` (see [[Feature Strong RBAC Config-Driven 2026-06-24]])
- [ ] Persist RBAC bindings to Postgres (`bindings` table) + runtime grant/revoke admin API
- [ ] Metric detector signals in investigation timeline (PromQL/VictoriaMetrics)
- [ ] mTLS for gRPC agent transport (production-ready)
- [ ] Pod log content scrubbing (secret-pattern redaction)
- [ ] Pagination on resource list pages (cursor/limit — large clusters exhaust single-call responses)
- [ ] Cluster health status endpoint (agent version, last-seen, health) in cluster selector UI

## Security Posture (as of 2026-06-24)

The Secrets/Certificates browser and admin env-reveal require the agent to read secrets cluster-wide. This is gated behind:

1. Opt-in RBAC overlay `deploy/local/k8s/21-agent-rbac-reveal.yaml` (not applied by default).
2. `LOTSMAN_ALLOW_ENV_REVEAL=1` in agent environment (default off).

Access control is now **deny-by-default**: authenticated users with no binding are denied on every API call. Bindings are declared in `LOTSMAN_SSO_CONFIG` as subject (GitHub login) or group (org/team slug) entries with explicit cluster and namespace scope.

Remaining open issues before any shared/production deployment:

- Pod logs are unscrubbed — `CanView` gates access but content is not sanitized.
- UI value masking is shoulder-surf protection only — the API has already delivered the value.
- Group membership snapshot in the JWT is stale until re-login (~8 h).
- Bindings are config-file-only — no runtime mutation without a server restart (deferred to Postgres store).

## Decisions & Notes

- In-cluster agent + control plane, agent dials out (ADR-0001, ADR-0002).
- Query-through telemetry, not a data lake; persist only derived state (ADR-0004, ADR-0005).
- Change-first root-cause ranking (ADR-0008).
- `LOTSMAN_SEED` flag (default `true`) gates seeded sample data; set `false` in deployed envs — supersedes the prior "remove store.Seed once Postgres is active" note.
- Status: **full-featured platform (multi-cluster, live on k3d)** — 293 tests across 24+ packages (`-race` clean); Go build/vet/gofmt clean; UI builds and exports clean; `npm audit` 0 issues.

## Relationships & Links

- **Architectural concepts:** [[Agent Control Plane Topology]], [[Source-Agnostic Adapters]], [[Correlation Engine]], [[UI Design System]], [[Persistence and State]], [[Authentication and RBAC]], [[LLM Incident Explainer]]
- **Dev toolkit:** [[Development Skills]] — installed agent skills mapped to each subsystem
- **Feature reports:** [[Feature Source Adapter Implementation 2026-06-21]], [[Feature Platform Foundation 2026-06-21]], [[Feature Pod Inspection 2026-06-21]], [[Feature Kubernetes Resource Inspection 2026-06-22]], [[Feature Lens UI Redesign 2026-06-22]], [[Feature LLM Incident Explainer 2026-06-22]], [[Feature Multi-Cluster and Mock Removal 2026-06-22]], [[Feature Strong RBAC Config-Driven 2026-06-24]]
- **Improve pass report:** [[Improve Engine Hardening and CVE Remediation 2026-06-24]]
- **Sources:** `docs/ARCHITECTURE.md`, `docs/adr/`
