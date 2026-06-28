'use client'

import { useCallback, useEffect, useMemo, useState } from 'react'
import { listEvents, type Signal } from '@/lib/api'
import { useCluster } from '@/lib/cluster-context'
import { ResourceToolbar } from '@/components/resource-toolbar'
import { LoadingState, ErrorState, EmptyState } from '@/components/view-states'
import {
  denseTableCls,
  denseThRowCls,
  denseThCls,
  denseTdCls,
  denseRowCls,
  eventSeverityStyle,
  relativeTime,
} from '@/lib/styles'

export default function EventsPage() {
  const { cluster, namespace } = useCluster()
  const [events, setEvents] = useState<Signal[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [nonce, setNonce] = useState(0)

  const load = useCallback(() => {
    if (!cluster) return undefined
    let cancelled = false
    setLoading(true)
    setError(null)
    listEvents(cluster, namespace)
      .then((e) => {
        if (!cancelled) setEvents(e)
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
  }, [cluster, namespace, nonce])

  useEffect(() => {
    const cancel = load()
    return cancel
  }, [load])

  const discoveredNamespaces = useMemo(
    () => events.map((e) => e.resource.namespace).filter((n): n is string => !!n),
    [events],
  )

  // Newest first, then client-side text filter.
  const rows = useMemo(() => {
    const sorted = [...events].sort(
      (a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime(),
    )
    const q = search.trim().toLowerCase()
    if (!q) return sorted
    return sorted.filter(
      (e) =>
        e.title.toLowerCase().includes(q) ||
        (e.message ?? '').toLowerCase().includes(q) ||
        (e.resource.name ?? '').toLowerCase().includes(q) ||
        (e.resource.kind ?? '').toLowerCase().includes(q) ||
        (e.resource.namespace ?? '').toLowerCase().includes(q),
    )
  }, [events, search])

  return (
    <>
      <ResourceToolbar
        title="Events"
        discoveredNamespaces={discoveredNamespaces}
        search={search}
        onSearch={setSearch}
        searchPlaceholder="Filter events…"
        onRefresh={() => setNonce((n) => n + 1)}
        refreshing={loading}
      />

      <div className="flex-1 overflow-auto">
        {!cluster ? (
          <EmptyState label="Select a cluster to view events." />
        ) : loading && events.length === 0 ? (
          <LoadingState label="Loading events…" />
        ) : error ? (
          <ErrorState label="Failed to load events" error={error} />
        ) : rows.length === 0 ? (
          <EmptyState label={search ? 'No events match the filter.' : 'No events in the selected window.'} />
        ) : (
          <table className={denseTableCls}>
            <thead>
              <tr className={denseThRowCls}>
                <th className={denseThCls}>Time</th>
                <th className={denseThCls}>Severity</th>
                <th className={denseThCls}>Reason</th>
                <th className={denseThCls}>Object</th>
                <th className={denseThCls}>Message</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((e, i) => {
                const sev = eventSeverityStyle(e.severity)
                return (
                  <tr key={e.id || `${e.timestamp}-${i}`} className={denseRowCls}>
                    <td
                      className={`${denseTdCls} whitespace-nowrap font-tech text-[12px] text-slate-500`}
                      title={new Date(e.timestamp).toLocaleString()}
                    >
                      {relativeTime(e.timestamp)}
                    </td>
                    <td className={denseTdCls}>
                      <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${sev.cls}`}>
                        {sev.label}
                      </span>
                    </td>
                    <td className={`${denseTdCls} text-slate-200`}>{e.title}</td>
                    <td className={denseTdCls}>
                      <span className="font-tech text-[12px] text-slate-400">
                        {e.resource.kind && <span className="text-slate-500">{e.resource.kind}/</span>}
                        {e.resource.name || '—'}
                        {e.resource.pod && e.resource.pod !== e.resource.name && (
                          <span className="text-slate-600"> · {e.resource.pod}</span>
                        )}
                      </span>
                    </td>
                    <td className={`${denseTdCls} max-w-md truncate text-[12px] text-slate-400`} title={e.message}>
                      {e.message || '—'}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </div>
    </>
  )
}
