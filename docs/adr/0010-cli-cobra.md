# ADR-0010 — CLI: cobra command tree over the REST API

**Status:** Accepted — implemented (`cmd/lotsman`)

## Context
`cmd/lotsman` began as a hand-rolled `flag`-based scaffold that only printed its
version; running an investigation was curl-only. Operators need a real terminal
client for the everyday flows — check health, run an investigation, inspect
incidents and clusters — that talks to the same control-plane REST API the UI
uses, with no privileged backend access of its own.

## Decision
**Build the CLI as a [cobra](https://github.com/spf13/cobra) command tree that is
a thin client over the control-plane REST API.**

- Root `lotsman` with subcommands `version`, `health`, `investigate`,
  `incidents`, `clusters` (each in its own file under `cmd/lotsman/`).
- **Persistent flags with env fallbacks**, resolved once in the root
  `PersistentPreRunE`: `--server` (`LOTSMAN_SERVER`), `--token`
  (`LOTSMAN_TOKEN`), `--output/-o` (`LOTSMAN_OUTPUT`), `--timeout`
  (`LOTSMAN_TIMEOUT`). An explicit flag always overrides the env var.
- **`-o table|json`** output: human tables by default, `json` for scripting.
- A small `Client` wraps the REST calls; the CLI holds no direct engine/store/
  Kubernetes access — it is exactly as privileged as the caller's session token.

## Consequences
- Consistent subcommand UX, flag parsing, help, and shell completion for free;
  the tree extends cleanly as new endpoints land.
- The CLI reuses the server's authz — a session token scopes it to the same RBAC
  bindings as the UI; there is no second, more-trusted access path to secure.
- Adds a `github.com/spf13/cobra` dependency (previously std-lib `flag` only) —
  accepted for the ergonomics and to stop reimplementing flag/env/output plumbing.
- `table`/`json` are the two supported shapes; richer formats (yaml, wide) can be
  added to the `render` layer without touching command wiring.
