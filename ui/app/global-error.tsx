'use client'

// Global error boundary (UI-3). Unlike error.tsx, this catches errors thrown in
// the root layout itself, so it must render its own <html>/<body>. It re-imports
// the global stylesheet to keep the Warm Operator tokens/fonts available even
// when the layout failed.

import './globals.css'
import { ErrorState } from '@/components/view-states'
import { toolbarBtnCls } from '@/lib/styles'

export default function GlobalError({
  error,
  reset,
}: {
  error: Error & { digest?: string }
  reset: () => void
}) {
  return (
    <html lang="en" className="dark">
      <body
        className="flex h-screen items-center justify-center p-8 text-slate-50"
        style={{ backgroundColor: 'var(--bg-base)' }}
      >
        <div className="w-full max-w-md space-y-4 rounded-2xl border border-slate-800 bg-[var(--surface)] p-6 shadow-card">
          <div className="space-y-1">
            <h1 className="text-sm font-semibold tracking-tight text-slate-100">Lotsman crashed</h1>
            <p className="text-[13px] text-slate-500">A fatal error prevented the app from rendering.</p>
          </div>
          <ErrorState label="Error" error={error.message || 'Unknown error'} />
          {error.digest && <p className="font-tech text-[11px] text-slate-600">digest: {error.digest}</p>}
          <button type="button" onClick={() => reset()} className={toolbarBtnCls}>
            Reload
          </button>
        </div>
      </body>
    </html>
  )
}
