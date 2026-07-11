// Flat ESLint config for the Next 16 / React 19 / TypeScript UI.
//
// Next 16 removed the built-in `next lint`, so we run ESLint directly
// (`eslint .`). We deliberately do NOT use `eslint-config-next` here: its
// `core-web-vitals` preset errors on the intentional plain-`<a>` sidebar nav
// (`no-html-link-for-pages`) used in layout-shell.tsx, which would make lint
// red-on-arrival. Instead we compose the language-agnostic JS recommended set,
// typescript-eslint (non-type-checked — fast, no project service), and the
// react-hooks rules that actually catch bugs in this codebase.
import js from '@eslint/js'
import tseslint from 'typescript-eslint'
import reactHooks from 'eslint-plugin-react-hooks'
import globals from 'globals'

export default tseslint.config(
  {
    ignores: ['node_modules/**', '.next/**', 'out/**', 'build/**', 'next-env.d.ts'],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ['**/*.{ts,tsx}'],
    // eslint-plugin-react-hooks v7 ships its preset in legacy (array-`plugins`)
    // shape, which ESLint 10 flat config rejects — so register the plugin and
    // enable its two rules explicitly.
    plugins: { 'react-hooks': reactHooks },
    languageOptions: {
      globals: {
        ...globals.browser,
        ...globals.node,
      },
    },
    rules: {
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'warn',
      // The `T | (string & {})` idiom (open string unions that keep editor
      // autocomplete) is used intentionally in lib/api.ts for forward-compatible
      // backend enums. Allow the empty-object type only inside intersections.
      '@typescript-eslint/no-empty-object-type': ['error', { allowWithName: '.*' }],
      // Allow intentionally-unused args/vars prefixed with `_` (e.g. error
      // boundary props we don't render).
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_', caughtErrors: 'none' },
      ],
    },
  },
  {
    // Test files run under Vitest globals (describe/it/expect/vi).
    files: ['**/*.{test,spec}.{ts,tsx}', 'vitest.setup.ts'],
    languageOptions: {
      globals: {
        ...globals.node,
      },
    },
  },
)
