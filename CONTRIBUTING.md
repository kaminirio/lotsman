# Contributing to Lotsman

Thanks for your interest in Lotsman! This project is an early-stage, self-hosted
Kubernetes monitoring and incident-investigation platform. Contributions of all
kinds are welcome — bug reports, docs, tests, and code.

## Project status

Lotsman is pre-1.0 but the core is built and tested: the correlation engine, the
concrete adapters (Loki / VictoriaMetrics / ArgoCD / Kubernetes), the gRPC agent
transport, the Postgres store, the detector scheduler, and auth are all implemented.
Genuinely remaining work — agentlink mTLS, the watch-event push path, metrics in the
timeline, the CLI, a Helm chart, richer ranker heuristics — is tracked in
`docs/ARCHITECTURE.md` (§12).

## Development setup

Requirements: **Go 1.26+**, **Node 22+** (for the UI), and Docker (for the local
stack and image builds).

```sh
go build ./...                 # must always pass
go vet ./... && gofmt -l .     # gofmt -l must print nothing
go test ./...

make run-server                # control plane, direct mode, in-memory store + seed data
make ui-dev                    # Next.js dev server on :3000 -> API on :8080
```

Run the full local stack (control plane + Loki + VictoriaMetrics + demo data):

```sh
make local-up
make local-investigate
```

## Architecture rules that PRs must respect

These are load-bearing design invariants (see `docs/adr/`):

- **The source-agnostic seam (ADR-0003).** The four interfaces in `internal/sources`
  are the *only* way the engine reads cluster data. **Never import a backend client
  (loki / victoriametrics / argocd / kubernetes) outside its adapter package**, and
  never let a backend type appear in `internal/engine`, `internal/api`, or
  `internal/model`.
- **The engine stays backend-free and well-tested** (`internal/engine`, ADR-0008).
  The ranker is change-first; keep its behavior covered by tests.
- **Persistence is query-through, not a data lake** (ADR-0004/0005). Only derived
  state (incidents, change history, clusters, config) is persisted.

## Pull request checklist

Before opening a PR, make sure:

- [ ] `go build ./...` passes.
- [ ] `go vet ./...` is clean and `gofmt -l .` prints nothing.
- [ ] `go test ./...` passes; new behavior has tests.
- [ ] No secrets, credentials, PII, or private infrastructure details are added.
- [ ] Docs/ADRs are updated when you change how a subsystem works.

Keep commits focused and write clear messages. Add new dependencies only when a
change genuinely needs them, and keep them behind the source-agnostic seam above.

## Reporting bugs and requesting features

Open a GitHub issue with clear reproduction steps (for bugs) or motivation and a
proposed approach (for features). For security issues, **do not** open a public
issue — see [SECURITY.md](SECURITY.md).

## License

By contributing, you agree that your contributions will be licensed under the
[Apache License 2.0](LICENSE).
