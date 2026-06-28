import type { NextConfig } from 'next'

// Static export for production builds (embedded into the Go binary, which serves
// the SPA fallback for client-routed paths). In `next dev` we omit it so the
// catch-all route renders dynamic paths on demand.
const isProd = process.env.NODE_ENV === 'production'

// Dev-only: proxy API calls to the control plane so the browser talks to the Next
// dev server same-origin (no CORS — the Go API emits no CORS headers). Leave
// NEXT_PUBLIC_API_URL unset in dev so the client uses relative paths that land
// here. Target is overridable via LOTSMAN_API_PROXY (default :8080). Rewrites are
// inert under `output: 'export'`, so this is gated to non-production builds.
const config: NextConfig = {
  output: isProd ? 'export' : undefined,
  images: { unoptimized: true },
  async rewrites() {
    if (isProd) return []
    const target = process.env.LOTSMAN_API_PROXY || 'http://localhost:8080'
    return [
      { source: '/api/:path*', destination: `${target}/api/:path*` },
      { source: '/auth/:path*', destination: `${target}/auth/:path*` },
      { source: '/healthz', destination: `${target}/healthz` },
    ]
  },
}

export default config
