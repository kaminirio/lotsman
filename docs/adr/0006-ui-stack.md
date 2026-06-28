# ADR-0006 — UI stack: Next.js 15 + Tailwind v4

**Status:** Accepted

## Context
The UI must be excellent. The chosen stack is Next.js 15, React 19, TypeScript, Tailwind
v4 with a custom "Warm Operator" design system, plain `fetch` via an `apiFetch` helper,
React Context state, REST API, GitHub OAuth — all statically exported and embedded into
the Go binary as a single-binary deploy.

## Decision
**Adopt this stack.** Use the `globals.css` "Warm Operator" palette and design tokens,
the `apiFetch` and auth-context patterns. Static export embedded via `//go:embed` for
single-binary deploys. Domain types are hand-written (no codegen), mirroring the Go structs.

## Consequences
- Consistent, self-contained operator console; UI components, auth patterns, and design
  tokens all live in `ui/` with no external UI library dependency.
- Stateless frontend: all persistent state lives in Go/Postgres; the export is pure
  HTML+JS assets embedded in the binary.
- Deliberate tradeoffs accepted: no component library, manual API types — minimal
  dependency surface and full control over styling outweigh novelty.
- Future shared workspace (`securero/shared`) can extract `lib/styles`, auth context, and
  shared Go packages if multiple operator tools converge on the same stack.
