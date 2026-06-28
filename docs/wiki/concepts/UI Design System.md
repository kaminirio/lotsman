---
title: "UI Design System"
type: concept
tags: [concept, frontend, ui, nextjs, lens]
created: 2026-06-21 13:30:00
updated: 2026-06-28 00:00:00
status: current
aliases: ["Warm Operator", "Lens UI", "UI Stack"]
---

# UI Design System

## Overview

Lotsman's UI (Next.js 15 + React 19 + TypeScript + Tailwind v4) is built as a **Lens/Freelens-style dark console** using the "Warm Operator" design system. The static export is embedded into the Go binary via `//go:embed` (ADR-0006, ADR-0007). See [[Feature Lens UI Redesign 2026-06-22]] for the full implementation report.

## Current Shell Layout

- **Left sidebar** (always visible): cluster selector at top, then grouped nav — CLUSTER (Overview, Nodes), WORKLOADS (Pods, Workloads), CONFIG (ConfigMaps, Secrets, Certificates), EVENTS, LOTSMAN (Incidents).
- **Namespace filter toolbar**: per-page, above resource tables; `_all` sentinel for cross-namespace queries.
- **Main content**: dense resource tables on list pages, full-window detail pages with tabs.

## Resource Pages

| Page | Route | Detail |
|---|---|---|
| Pods | `/pods` | Overview / Env / Logs tabs; clickable env source chips |
| Workloads | `/workloads` | Deployments, StatefulSets, DaemonSets |
| Nodes | `/nodes` | Capacity/Allocatable columns |
| ConfigMaps | `/configmaps` | Table / Raw (YAML) toggle on detail |
| Secrets | `/secrets` | Masked values; per-row eye toggle; Reveal all; Copy; Certificate panel for TLS secrets |
| Certificates | `/certificates` | Client-side filter of TLS secrets; CN/Issuer/Expiry/Status badge |
| Events | `/events` | Warning events highlighted |
| Incidents | `/incidents` | Correlation timeline; ranked hypotheses; AI explanation panel |

## Static-Export Routing

Next.js static export does not support `[slug]` dynamic segments. Detail pages use query params instead (e.g. `?pod=checkout&ns=default`). This required fixing the embedded UI handler (`internal/ui/ui.go`) which previously served `index.html` for every route — it now resolves `<route>.html` → `<route>/index.html` → `index.html` in order, so each page renders its own HTML.

## Log Viewer (Pretty Mode)

The pod logs tab has a Raw/Pretty switcher:
- **Raw**: verbatim lines, monospace.
- **Pretty** (structlog-for-humans): JSON lines are parsed and rendered as a colored level chip + prominent message + dim key=value pairs. Non-JSON lines fall back to raw.

## Secret and Certificate UX

- Values masked by default; per-row eye toggle; **Reveal all**; **Copy** (clipboard, no display).
- Partial masking: leading/trailing chars visible for long values.
- `kubernetes.io/tls` secrets show a **Certificate panel** (CN, Issuer, validity, SANs, Valid/Expiring/Expired badge) regardless of reveal state — cert metadata is always public.

## Core Principles

- **Warm Operator design system** — `globals.css` palette, the `apiFetch` client pattern, and auth-context; hand-written API types (no codegen).
- **Single-binary deploy** — `internal/ui` embeds the `ui/` static export via `//go:embed all:dist`.
- **Auth** — GitHub OAuth + HttpOnly JWT session cookies + RBAC with a structured SSO config (ADR-0007).

## Future Shared Workspace

The `securero/shared` workspace pattern (extracting `lib/styles`, auth context, and shared Go packages) remains a future option to reduce duplication across multiple operator tools on this stack.

## Relationships & Context

- **Parent concepts:** [[Lotsman]]
- **Related topics:** [[Correlation Engine]], [[Authentication and RBAC]], [[Source-Agnostic Adapters]]
- **Relevant skills:** `frontend-design`, `frontend-dev-conventions`, `ui-ux-pro-max` — see [[Development Skills]]
- **Implementation reports:** [[Feature Lens UI Redesign 2026-06-22]], [[Feature Pod Inspection 2026-06-21]]
- **Sources:** `ui/`, `internal/ui/ui.go`, `docs/adr/0006-ui-stack.md`, `docs/adr/0007-auth-github-oauth.md`
