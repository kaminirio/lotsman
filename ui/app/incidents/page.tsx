'use client'

import { useCallback, useEffect, useMemo, useState } from 'react'
import { listIncidents, SEVERITY_LABELS, type Incident } from '@/lib/api'
import { ResourceToolbar } from '@/components/resource-toolbar'
import { LoadingState, ErrorState, EmptyState } from '@/components/view-states'
import { AiExplanation } from '@/components/ai-explanation'
import { severityStyle, signalKindStyle, relativeTime } from '@/lib/styles'

export default function IncidentsPage() {
  const [incidents, setIncidents] = useState<Incident[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [search, setSearch] = useState('')
  const [nonce, setNonce] = useState(0)

  const load = useCallback(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    listIncidents()
      .then((i) => {
        if (!cancelled) setIncidents(i)
      })
      .catch((e) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [nonce])

  useEffect(() => {
    const cancel = load()
    return cancel
  }, [load])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return incidents
    return incidents.filter(
      (inc) =>
        inc.title.toLowerCase().includes(q) ||
        (inc.resource.name ?? '').toLowerCase().includes(q) ||
        (inc.resource.namespace ?? '').toLowerCase().includes(q),
    )
  }, [incidents, search])

  return (
    <>
      <ResourceToolbar
        title="Incidents"
        showNamespace={false}
        search={search}
        onSearch={setSearch}
        searchPlaceholder="Filter incidents…"
        onRefresh={() => setNonce((n) => n + 1)}
        refreshing={loading}
      />

      <div className="flex-1 overflow-auto p-5">
        {loading && incidents.length === 0 ? (
          <LoadingState label="Loading incidents…" />
        ) : error ? (
          <ErrorState label="Failed to load incidents" error={error} />
        ) : filtered.length === 0 ? (
          <EmptyState label={search ? 'No incidents match the filter.' : 'No incidents. 🎉'} />
        ) : (
          <div className="space-y-3">
            <p className="text-[12px] text-slate-500">
              Correlated across logs, metrics, and deployments — ranked by probable cause.
            </p>
            {filtered.map((inc) => {
              const top = inc.hypotheses?.[0]
              return (
                <article
                  key={inc.id}
                  className="rounded-xl border border-slate-800 bg-[var(--surface)] p-4 shadow-card"
                >
                  <div className="flex items-start justify-between gap-4">
                    <div className="min-w-0 space-y-1">
                      <div className="flex items-center gap-2">
                        <span
                          className={`rounded px-1.5 py-0.5 text-[10px] font-medium uppercase ${severityStyle(inc.severity)}`}
                        >
                          {SEVERITY_LABELS[inc.severity]}
                        </span>
                        <h2 className="truncate text-[13px] font-semibold text-slate-100">{inc.title}</h2>
                        <span className="rounded border border-slate-700 px-1.5 py-0.5 text-[10px] uppercase text-slate-500">
                          {inc.status}
                        </span>
                      </div>
                      <p className="font-tech text-[11px] text-slate-500">
                        {inc.resource.cluster} / {inc.resource.namespace} / {inc.resource.name}
                      </p>
                    </div>
                    <span
                      className="shrink-0 font-tech text-[11px] text-slate-600"
                      title={new Date(inc.opened_at).toLocaleString()}
                    >
                      {relativeTime(inc.opened_at)} ago
                    </span>
                  </div>

                  {top && (
                    <div className="mt-3 rounded-lg border border-indigo-500/20 bg-[var(--accent-soft)] px-3 py-2.5">
                      <div className="flex items-center justify-between">
                        <span className="text-[11px] font-semibold uppercase tracking-wider text-indigo-300/80">
                          Probable cause
                        </span>
                        <span className="font-tech text-[11px] text-indigo-300">
                          {Math.round(top.confidence * 100)}% confidence
                        </span>
                      </div>
                      <p className="mt-1 text-[13px] text-slate-200">{top.summary}</p>
                    </div>
                  )}

                  <div className="mt-3 flex flex-wrap gap-1.5">
                    {inc.timeline.map((s) => {
                      const k = signalKindStyle(s.kind)
                      return (
                        <span
                          key={s.id}
                          className="inline-flex items-center gap-1.5 rounded-md border border-slate-800 bg-[var(--surface-2)] px-2 py-1 text-[11px] text-slate-400"
                        >
                          <span className={`h-1.5 w-1.5 rounded-full ${k.dot}`} />
                          <span className={k.text}>{k.label}</span>
                          <span className="text-slate-500">{s.title}</span>
                        </span>
                      )
                    })}
                  </div>

                  <AiExplanation incidentId={inc.id} />
                </article>
              )
            })}
          </div>
        )}
      </div>
    </>
  )
}
