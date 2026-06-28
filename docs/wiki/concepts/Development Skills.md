---
title: "Development Skills"
type: concept
tags: [concept, tooling, skills]
created: 2026-06-21 13:30:00
updated: 2026-06-21 13:30:00
status: current
aliases: ["Dev Toolkit", "Skills"]
---

# Development Skills

## 📌 Overview
Agent skills installed globally (via the `skills` CLI) to accelerate Lotsman development, mapped to the subsystems they serve. Discover/verify new ones with the `find-skills` skill; installation requires explicit approval (a harness hook gates `skills add`/`update`).

## ⚙️ Skill → subsystem map
* **Go backend (control plane + agent)** — the `samber/cc-skills-golang` collection (`golang-*`): code-style, error-handling, concurrency, context, data-structures, design-patterns, dependency-injection, testing, continuous-integration, documentation. Underpins all Go code, especially [[Correlation Engine]] and [[Agent Control Plane Topology]].
* **gRPC agent link** — `golang-grpc` for `internal/agentlink` + `proto/lotsman.proto`. See [[Agent Control Plane Topology]].
* **PostgreSQL** — `golang-database` (pgx access patterns) + `wshobson/agents@postgresql-table-design` (schema for incidents / change-history / clusters). Backs `internal/store`.
* **CLI** — `golang-cli`, `golang-spf13-cobra`, `golang-spf13-viper` for `cmd/lotsman`.
* **Loki / LogQL** — `grafana/skills@loki` (official Grafana) for the log adapter in [[Source-Agnostic Adapters]].
* **PromQL** — `grafana/skills@promql` (Prometheus-compatible, so it covers VictoriaMetrics) for the metric adapter in [[Source-Agnostic Adapters]].
* **Helm** — `wshobson/agents@helm-chart-scaffolding` for the `deploy/` chart.
* **Kubernetes** — `jeffallan/claude-skills@kubernetes-specialist` for manifests/ops; for `client-go` informer code prefer context7's live `k8s.io/client-go` docs + the `golang-*` skills.

## 🚫 Deliberately not covered
* **ArgoCD** — no high-quality skill exists (all low-install / unknown source); use context7 docs or the official ArgoCD REST API reference.
* **Frontend** — already covered by `frontend-design`, `frontend-dev-conventions`, and `ui-ux-pro-max`. See [[UI Design System]].

## 🔗 Relationships & Context
* **Parent concepts:** [[Lotsman]]
* **Related topics:** [[Agent Control Plane Topology]], [[Source-Agnostic Adapters]], [[Correlation Engine]], [[UI Design System]]
* **Sources:** installed skills (discover with the `find-skills` skill).
