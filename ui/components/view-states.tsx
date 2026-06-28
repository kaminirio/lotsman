'use client'

// Shared loading / error / empty placeholders for the dense resource views.

export function LoadingState({ label = 'Loading…' }: { label?: string }) {
  return (
    <div className="flex items-center gap-2 px-4 py-6 text-[13px] text-slate-500" aria-busy="true">
      <span
        className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-slate-700 border-t-indigo-400"
        aria-hidden="true"
      />
      {label}
    </div>
  )
}

export function ErrorState({ label, error }: { label: string; error: string }) {
  return (
    <div
      role="alert"
      className="mx-4 my-4 rounded-lg border border-red-500/30 bg-red-500/5 px-4 py-3 text-[13px] text-red-300"
    >
      {label}: {error}
    </div>
  )
}

export function EmptyState({ label }: { label: string }) {
  return (
    <div className="px-4 py-10 text-center text-[13px] text-slate-500">{label}</div>
  )
}
