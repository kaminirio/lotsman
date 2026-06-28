'use client'

import { useEffect, useRef, useState } from 'react'
import { useCluster } from '@/lib/cluster-context'
import { focusRingCls } from '@/lib/styles'

/**
 * ClusterSelector — dropdown in the sidebar listing every cluster from
 * listClusters(), each with a connection dot (green = connected). The selection
 * is held in ClusterContext and shared across all views.
 */
export function ClusterSelector() {
  const { clusters, clustersLoading, clustersError, cluster, setCluster } = useCluster()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    function onDown(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  const active = clusters.find((c) => c.name === cluster)

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="listbox"
        aria-expanded={open}
        className={[
          'flex w-full items-center gap-2 rounded-md border border-slate-700 bg-[var(--surface-2)] px-2.5 py-2 text-left transition-colors hover:border-slate-600',
          focusRingCls,
        ].join(' ')}
      >
        <ConnDot connected={active?.connected} />
        <span className="min-w-0 flex-1">
          <span className="block truncate font-tech text-[13px] text-slate-100">
            {clustersLoading ? 'Loading…' : (cluster || 'No cluster')}
          </span>
          {active?.mode && (
            <span className="block truncate text-[10px] text-slate-500">{active.mode}</span>
          )}
        </span>
        <svg
          className={`h-3.5 w-3.5 shrink-0 text-slate-500 transition-transform ${open ? 'rotate-180' : ''}`}
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          strokeWidth={2}
          aria-hidden="true"
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="M19.5 8.25l-7.5 7.5-7.5-7.5" />
        </svg>
      </button>

      {clustersError && (
        <p className="mt-1 px-1 text-[11px] text-red-400">Clusters unavailable</p>
      )}

      {open && (
        <ul
          role="listbox"
          aria-label="Clusters"
          className="absolute left-0 right-0 top-full z-30 mt-1 max-h-72 overflow-y-auto rounded-md border border-slate-700 bg-[var(--surface)] py-1 shadow-elevated"
        >
          {clusters.length === 0 && !clustersLoading && (
            <li className="px-3 py-2 text-[12px] text-slate-500">No clusters</li>
          )}
          {clusters.map((c) => {
            const selected = c.name === cluster
            return (
              <li key={c.name} role="option" aria-selected={selected}>
                <button
                  type="button"
                  onClick={() => {
                    setCluster(c.name)
                    setOpen(false)
                  }}
                  className={[
                    'flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-[13px] transition-colors',
                    selected ? 'bg-[var(--accent-soft)] text-slate-100' : 'text-slate-300 hover:bg-[var(--surface-hover)]',
                    focusRingCls,
                  ].join(' ')}
                >
                  <ConnDot connected={c.connected} />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-tech">{c.name}</span>
                    {(c.env || c.region) && (
                      <span className="block truncate text-[10px] text-slate-500">
                        {[c.env, c.region].filter(Boolean).join(' · ')}
                      </span>
                    )}
                  </span>
                  {selected && (
                    <svg className="h-3.5 w-3.5 shrink-0 text-[var(--accent-hover)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.2} aria-hidden="true">
                      <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
                    </svg>
                  )}
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

function ConnDot({ connected }: { connected?: boolean }) {
  return (
    <span
      className={[
        'h-2 w-2 shrink-0 rounded-full',
        connected ? 'bg-emerald-400 shadow-[0_0_6px_-1px_rgba(52,211,153,0.8)]' : 'bg-slate-600',
      ].join(' ')}
      title={connected ? 'connected' : 'disconnected'}
      aria-hidden="true"
    />
  )
}
