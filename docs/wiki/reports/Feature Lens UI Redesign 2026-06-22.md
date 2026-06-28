---
title: "Feature Lens UI Redesign 2026-06-22"
type: report
tags: [report, feature, ui, frontend, nextjs, kubernetes]
created: 2026-06-22 14:31:13
updated: 2026-06-22 14:31:13
status: final
flow: feature
---

# Feature Lens UI Redesign 2026-06-22

## Summary

The Lotsman UI was redesigned from the earlier minimal workloads browser into a Lens/Freelens-style dark console: a persistent left sidebar with cluster selector and grouped navigation, a namespace filter toolbar, dense resource tables for every newly available resource type (pods, configmaps, secrets, nodes), full-window detail pages with tabs, and a structured log viewer. A long-standing bug where all routes served `index.html` (making every page look identical) was also fixed. The UI builds and exports cleanly and is validated against live k3d data.

## Details

### Shell layout (`ui/`)

- **Left sidebar** — fixed-width dark panel, always visible. Top section: cluster selector (populated from `GET /api/v1/clusters`). Navigation groups:
  - **CLUSTER**: Overview, Nodes
  - **WORKLOADS**: Pods, Workloads
  - **CONFIG**: ConfigMaps, Secrets, Certificates
  - **EVENTS**: Events
  - **LOTSMAN**: Incidents
- **Namespace filter toolbar** — appears below the top bar on resource-list pages; namespace options populated per-cluster; `_all` triggers cross-namespace queries.
- **Main content area** — dense resource tables and full-window detail pages.

### Resource list pages

Each list page is a dense table, consistent with the Lens design language:

| Page | Key columns |
|---|---|
| Pods | Name, Namespace, Status (phase + ready chip), Restarts, Node, Age |
| Workloads | Name, Kind (Deployment/StatefulSet/DaemonSet), Namespace, Ready, Age |
| Nodes | Name, Status, Roles, Version, OS/Arch, Internal IP, CPU/Mem capacity |
| ConfigMaps | Name, Namespace, Keys count, Age |
| Secrets | Name, Namespace, Type, Age |
| Certificates | Filtered view of TLS secrets — CN, Issuer, Expiry, Status badge |
| Events | Namespace, Reason, Object, Message, Age; Warning events highlighted |

### Detail pages (full-window, query-param routes)

Static-export-compatible routing: detail pages use query params (e.g. `?pod=checkout&ns=default`) rather than dynamic segments, because Next.js static export does not support `[slug]` params.

#### Pod detail

Three tabs:

- **Overview** — phase, node, containers list with image, restart count.
- **Env** — table of env vars with source chip: `literal` / `secretKeyRef` / `configMapKeyRef`. Secret and ConfigMap chips are **clickable** — navigate directly to the secret or configmap detail page. Masking follows the permission-driven reveal model (admin sees values; non-admin sees masked placeholder).
- **Logs** — Raw/Pretty switcher:
  - **Raw**: plain text, monospace, each line verbatim.
  - **Pretty** (structlog-for-humans): tries JSON parse on each line; if successful, renders a colored level chip (DEBUG/INFO/WARN/ERROR), a prominent message field, and remaining key=value pairs in dim text — one line per entry. Non-JSON lines fall back to raw display.

#### ConfigMap detail

Two views:
- **Table** — key/value pairs as a two-column table.
- **Raw (YAML)** — full YAML dump of the configmap data.

#### Secret detail

- Values are masked by default; each row has a per-row eye toggle to unmask individually.
- **Reveal all** button unmasks all rows simultaneously.
- **Copy** button copies the raw value to clipboard without displaying it (works even while masked).
- **Partial masking** — for longer values, a few leading and trailing characters are visible to aid identification; short values are fully masked.
- **Certificate panel** — if the secret type is `kubernetes.io/tls`, a Certificate panel appears above the data table showing CN, Issuer, NotBefore/NotAfter, DNS SANs, and a `Valid` / `Expiring` / `Expired` badge. This section is always rendered regardless of reveal state (cert metadata is public).

### Navigation enhancements

- **Namespace in detail breadcrumbs is clickable** — clicking the namespace in a pod/configmap/secret breadcrumb jumps to the namespace-scoped Pods list with that namespace pre-selected.
- **Back navigation** uses `router.back()` within the same cluster/namespace context.

### Bug fix: embedded UI route resolution (`internal/ui/ui.go`)

The embedded UI handler was serving `index.html` for every route, making all pages (pods, configmaps, secrets, etc.) render identically — page-specific data fetching was never reached because the URL was effectively reset to `/`. Fixed by resolving Next.js per-route exports first:

```
/pods -> ui/dist/pods.html         (exact file)
/pods -> ui/dist/pods/index.html   (Next.js directory layout)
/pods -> ui/dist/index.html        (SPA fallback)
```

The handler in `internal/ui/ui.go` now walks `<route>.html` and `<route>/index.html` before falling back to `index.html`. All navigation links in the sidebar now render distinct pages.

### Build and embedding

- `make ui-build` runs `next build` + export then copies into `internal/ui/dist/`.
- `//go:embed all:dist` in `internal/ui/ui.go` picks up the full export.
- `ui/dist/` must contain at least `index.html` for the Go build to pass.

### Verification

- Sidebar cluster selector lists `local` and `local-2` from live k3d registry.
- All list pages render real data fetched from the API.
- Pod detail Env tab shows source chips; clicking a `secretKeyRef` chip navigates to the secret detail.
- Pretty log viewer renders structured JSON lines from the checkout container with colored level chips.
- Certificate panel renders TLS secret cert info (CN, SANs, expiry badge) without reveal enabled.
- Route fix confirmed: `/pods`, `/configmaps`, `/secrets`, `/nodes` all render distinct pages.

## Caveats & open items

- Pretty log parser is best-effort; mixed structured/unstructured logs in the same container render inconsistently (non-JSON lines fall back to raw).
- No pagination on list pages — all resources returned in a single API call; large clusters will need cursor/limit support.
- Certificates page is a client-side filter of the secrets list; relies on `type == kubernetes.io/tls`, which excludes non-standard TLS secrets.
- No live-refresh on detail pages; logs require manual navigation to reload.
- Shared workspace extraction (`lib/styles`, auth context) not yet executed; remains a future item.

## Related

- Affected concepts: [[UI Design System]], [[Authentication and RBAC]], [[Source-Agnostic Adapters]]
- Project: [[Lotsman]]
- Feature context: [[Feature Kubernetes Resource Inspection 2026-06-22]]
