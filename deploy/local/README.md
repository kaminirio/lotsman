# Running Lotsman locally on Docker

Two ways to bring the stack up with Docker Compose, from the repo root.

## Core (fast) — UI + seeded incident

```sh
docker compose up --build          # or: make local-core
open http://localhost:8080
curl -s localhost:8080/api/v1/incidents | jq
```

Runs the control plane (DIRECT mode, embedded Next.js UI) + Postgres. The
adapters have no live backends, so this shows the **seeded** sample incident
(an ArgoCD deploy ranked as probable cause). Boots in seconds.

## Full telemetry — a live, correlated, ranked incident

```sh
docker compose --profile full up --build   # or: make local-up
```

Adds:

| Service           | Port | Role                                                            |
|-------------------|------|-----------------------------------------------------------------|
| `control-plane`   | 8080 | REST API + embedded UI (DIRECT mode)                            |
| `postgres`        | 5432 | control-plane state (store still in-memory until the pgx store) |
| `loki`            | 3100 | real log backend — the Loki adapter queries it                  |
| `victoriametrics` | 8428 | real metric backend                                             |
| `demo`            | 8081 | mock ArgoCD API + Loki/VictoriaMetrics data generator           |

The `demo` service continuously feeds error logs and rising metrics for
`demo/checkout`, and serves a mock ArgoCD Application whose sync history is
timestamped ~4 minutes before *now*. Run a live investigation:

```sh
make local-investigate
# or:
curl -s -XPOST localhost:8080/api/v1/investigate \
  -d '{"cluster":"local","namespace":"demo","kind":"Deployment","name":"checkout"}' | jq
```

You should see a timeline of real Loki error logs plus the ArgoCD change, with
the **top hypothesis** being the recent deploy (change-first ranking, ADR-0008).

Poke the backends directly:

```sh
curl -s 'localhost:8081/api/v1/applications' | jq               # mock ArgoCD
curl -sG 'localhost:3100/loki/api/v1/query_range' \
  --data-urlencode 'query={namespace="demo",app="checkout"}' | jq '.data.result | length'
curl -sG 'localhost:8428/api/v1/query' \
  --data-urlencode 'query=container_memory_usage_bytes' | jq '.data.result[0].value'
```

## Teardown

```sh
docker compose --profile full down -v       # or: make local-down
```

## What is *not* lit up locally

- **Kubernetes adapter** — needs a real cluster; no backend in compose, so its
  calls fail gracefully and the engine continues (Loki/ArgoCD still correlate).
- **Metrics in the incident timeline** — `Investigate` correlates logs +
  changes + k8s events; metrics are only consumed by detectors, which have no
  live scheduler/endpoint yet (pending work). VictoriaMetrics data is real and
  directly queryable, just not yet surfaced in the timeline.
- **Postgres-backed persistence** — the store is still in-memory + seed data;
  Postgres runs but isn't read from until the pgx store lands.

For all four adapters end-to-end (including ArgoCD + Kubernetes for real), use a
local `k3d`/`kind` cluster with ArgoCD installed and run the in-cluster agent.
