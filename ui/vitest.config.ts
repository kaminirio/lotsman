import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// Vitest runs the pure lib/* logic and component render tests under jsdom with
// React 19. The single `@` alias mirrors the tsconfig `@/*` -> `./*` mapping so
// tests import modules exactly as the app does.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('.', import.meta.url)),
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./vitest.setup.ts'],
    include: ['{app,components,lib}/**/*.{test,spec}.{ts,tsx}'],
  },
})
