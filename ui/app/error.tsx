'use client'

// Route-segment error boundary (UI-3). Catches render-time exceptions in any
// page under the console chrome and shows a styled fallback with a retry, so a
// thrown error never white-screens the app. Rendered inside the root layout, so
// the sidebar/nav stay intact.

import { useEffect } from 'react'
import { ErrorState } from '@/components/view-states'
import { toolbarBtnCls } from '@/lib/styles'

export default function RouteError({
  error,
  reset,
}: {
  error: Error & { digest?: string }
  reset: () => void
}) {
  useEffect(() => {
    // Log for diagnostics; the boundary itself already prevents the white screen.
    console.error(error)
  }, [error])

  return (
    <div className="flex flex-1 items-center justify-center p-8">
      <div className="w-full max-w-md space-y-4 rounded-2xl border border-slate-800 bg-[var(--surface)] p-6 shadow-card">
        <div className="space-y-1">
          <h1 className="text-sm font-semibold tracking-tight text-slate-100">Something went wrong</h1>
          <p className="text-[13px] text-slate-500">This view hit an unexpected error and couldn’t render.</p>
        </div>
        <ErrorState label="Error" error={error.message || 'Unknown error'} />
        {error.digest && <p className="font-tech text-[11px] text-slate-600">digest: {error.digest}</p>}
        <button type="button" onClick={() => reset()} className={toolbarBtnCls}>
          Try again
        </button>
      </div>
    </div>
  )
}
