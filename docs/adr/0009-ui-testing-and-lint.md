# ADR-0009 — UI testing & lint: Vitest + ESLint flat config

**Status:** Accepted — implemented (`ui/vitest.config.ts`, `ui/eslint.config.mjs`)

## Context
The UI (`ui/`, ADR-0006) shipped with no tests and a `lint` script that pointed
at `next lint`, which **Next.js 16 removed** — so nothing actually ran. The
non-trivial logic (`lib/{styles,logparse,api}.ts`), the container-status and
view-state components, and the live-refresh hook all needed regression coverage,
and CI needed a real lint gate rather than a no-op.

## Decision
**Test with Vitest + React Testing Library; lint with ESLint's flat config,
directly (`eslint .`).**

- **Vitest** runs the pure `lib/*` logic and component render tests under `jsdom`
  with React 19 (`@vitejs/plugin-react`). The `@` alias mirrors the tsconfig
  `@/*` → `./*` mapping so tests import modules exactly as the app does. Scripts:
  `test` (watch) and `test:run` (CI one-shot).
- **ESLint flat config** composes `@eslint/js` recommended, `typescript-eslint`
  (non-type-checked — fast, no project service), and `eslint-plugin-react-hooks`.
  We deliberately do **not** use `eslint-config-next`: its `core-web-vitals`
  preset errors on the intentional plain-`<a>` sidebar nav, which would make lint
  red-on-arrival.
- Both are gated in the CI `ui` job (`npm run lint`, `npm run test:run`).

## Consequences
- Lint and tests run identically locally and in CI; a broken lint script can no
  longer pass silently.
- Vitest reuses the Vite/React toolchain the app already implies — fast, no
  separate Jest/babel config, and jsdom covers render-level component tests.
- Composing the ESLint ruleset by hand (rather than adopting a framework preset)
  means new rules are opt-in and intentional, at the cost of maintaining the
  config ourselves.
- Type-checked linting is deferred (the non-project-service mode trades some rule
  depth for speed); the TypeScript compiler still type-checks during `next build`.
