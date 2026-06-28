'use client'

import { useCallback, useState } from 'react'
import { focusRingCls } from '@/lib/styles'

// Small copy-to-clipboard button used in ConfigMap / Secret value cells. Shows a
// transient "Copied" state. Degrades silently when the clipboard API is missing
// (older browsers / insecure contexts) — the button simply does nothing.
export function CopyButton({ value, label = 'value' }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false)

  const copy = useCallback(() => {
    if (typeof navigator === 'undefined' || !navigator.clipboard) return
    navigator.clipboard
      .writeText(value)
      .then(() => {
        setCopied(true)
        window.setTimeout(() => setCopied(false), 1500)
      })
      .catch(() => {
        /* clipboard write blocked — no-op */
      })
  }, [value])

  return (
    <button
      type="button"
      onClick={copy}
      aria-label={copied ? `Copied ${label}` : `Copy ${label}`}
      className={`inline-flex h-6 shrink-0 items-center gap-1 rounded border border-slate-700 bg-[var(--surface-2)] px-1.5 text-[10px] font-medium text-slate-400 transition-colors hover:bg-[var(--surface-hover)] hover:text-slate-200 ${focusRingCls}`}
    >
      {copied ? (
        <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.2} aria-hidden="true">
          <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
        </svg>
      ) : (
        <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8} aria-hidden="true">
          <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 17.25v3.375c0 .621-.504 1.125-1.125 1.125h-9.75a1.125 1.125 0 01-1.125-1.125V7.875c0-.621.504-1.125 1.125-1.125H6.75a9.06 9.06 0 011.5.124m7.5 10.376h3.375c.621 0 1.125-.504 1.125-1.125V11.25c0-4.46-3.243-8.161-7.5-8.876a9.06 9.06 0 00-1.5-.124H9.375c-.621 0-1.125.504-1.125 1.125v3.5m7.5 10.375H9.375a1.125 1.125 0 01-1.125-1.125v-9.25m11.25 6.375h-1.875a1.125 1.125 0 01-1.125-1.125v-1.875" />
        </svg>
      )}
      {copied ? 'Copied' : 'Copy'}
    </button>
  )
}
