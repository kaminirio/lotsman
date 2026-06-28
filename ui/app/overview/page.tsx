'use client'

import { useCallback, useEffect, useState } from 'react'
import {
  listPods,
  listWorkloads,
  listEvents,
  listIncidents,
  type Pod,
  type WorkloadRef,
  type Signal,
  type Incident,
} from '@/lib/api'
import { useCluster } from '@/lib/cluster-context'
import { ResourceToolbar } from '@/components/resource-toolbar'
import { EmptyState } from '@/components/view-states'
import { eventSeverityStyle, podPhaseDot } from '@/lib/styles'

interface Counts {
  pods: number
  podsRunning: number
  workloads: number
  events: number
  eventsWarn: number
  incidents: number
}

export default function OverviewPage() {
  const { cluster, namespace } = useCluster()
  const [counts, setCounts] = useState<Counts | null>(null)
  const [recentEvents, setRecentEvents] = useState<Signal[]>([])
  const [pods, setPods] = useState<Pod[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [nonce, setNonce] = useState(0)

  const load = useCallback(() => {
    if (!cluster) return undefined
    let cancelled = false
    setLoading(true)
    setError(null)
    Promise.allSettled([
      listPods(cluster, namespace),
      listWorkloads(cluster, namespace),
      listEvents(cluster, namespace),
      listIncidents(),
    ])
      .then(([podsR, wlR, evR, incR]) => {
        if (cancelled) return
        const podList = podsR.status === 'fulfilled' ? podsR.value : []
        const wlList: WorkloadRef[] = wlR.status === 'fulfilled' ? wlR.value : []
        const evList: Signal[] = evR.status === 'fulfilled' ? evR.value : []
        const incList: Incident[] = incR.status === 'fulfilled' ? incR.value : []
        // Surface the first hard failure (if every call failed) as the error banner.
        if (
          podsR.status === 'rejected' &&
          wlR.status === 'rejected' &&
          evR.status === 'rejected' &&
          incR.status === 'rejected'
        ) {
          const e = podsR.reason
          setError(e instanceof Error ? e.message : String(e))
        }
        setPods(podList)
        setRecentEvents(
          [...evList].sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime()).slice(0, 6),
        )
        setCounts({
          pods: podList.length,
          podsRunning: podList.filter((p) => p.phase === 'Running').length,
          workloads: wlList.length,
          events: evList.length,
          eventsWarn: evList.filter((e) => e.severity >= 1).length,
          incidents: incList.filter((i) => i.status === 'open' || i.status === 'investigating').length,
        })
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

  const unhealthy = pods.filter((p) => p.phase !== 'Running' && p.phase !== 'Succeeded').slice(0, 6)

  return (
    <>
      <ResourceToolbar
        title="Overview"
        discoveredNamespaces={pods.map((p) => p.namespace)}
        onRefresh={() => setNonce((n) => n + 1)}
        refreshing={loading}
      />

      <div className="flex-1 overflow-auto p-5">
        {!cluster ? (
          <EmptyState label="Select a cluster to see its overview." />
        ) : error ? (
          <div role="alert" className="rounded-lg border border-red-500/30 bg-red-500/5 px-4 py-3 text-[13px] text-red-300">
            Failed to load overview: {error}
          </div>
        ) : (
          <div className="space-y-5">
            {/* Summary cards */}
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <StatCard
                label="Pods"
                value={counts?.pods}
                sub={counts ? `${counts.podsRunning} running` : undefined}
                loading={loading && !counts}
              />
              <StatCard label="Workloads" value={counts?.workloads} loading={loading && !counts} />
              <StatCard
                label="Recent events"
                value={counts?.events}
                sub={counts && counts.eventsWarn > 0 ? `${counts.eventsWarn} warning+` : undefined}
                subTone={counts && counts.eventsWarn > 0 ? 'warn' : undefined}
                loading={loading && !counts}
              />
              <StatCard
                label="Open incidents"
                value={counts?.incidents}
                subTone={counts && counts.incidents > 0 ? 'bad' : undefined}
                loading={loading && !counts}
              />
            </div>

            <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
              {/* Unhealthy pods */}
              <Panel title="Pods needing attention">
                {unhealthy.length === 0 ? (
                  <p className="px-1 py-3 text-[13px] text-emerald-400/80">All pods healthy.</p>
                ) : (
                  <ul className="divide-y divide-slate-800/50">
                    {unhealthy.map((p) => (
                      <li key={`${p.namespace}/${p.name}`} className="flex items-center gap-2 py-2">
                        <span className={`h-2 w-2 shrink-0 rounded-full ${podPhaseDot(p.phase)}`} aria-hidden="true" />
                        <span className="min-w-0 flex-1 truncate font-tech text-[12px] text-slate-300">{p.name}</span>
                        <span className="font-tech text-[11px] text-slate-500">{p.phase}</span>
                      </li>
                    ))}
                  </ul>
                )}
              </Panel>

              {/* Recent events */}
              <Panel title="Recent events">
                {recentEvents.length === 0 ? (
                  <p className="px-1 py-3 text-[13px] text-slate-500">No recent events.</p>
                ) : (
                  <ul className="divide-y divide-slate-800/50">
                    {recentEvents.map((e, i) => {
                      const sev = eventSeverityStyle(e.severity)
                      return (
                        <li key={e.id || `${e.timestamp}-${i}`} className="flex items-center gap-2 py-2">
                          <span className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${sev.cls}`}>{sev.label}</span>
                          <span className="min-w-0 flex-1 truncate text-[12px] text-slate-300">{e.title}</span>
                          <span className="truncate font-tech text-[11px] text-slate-500">{e.resource.name}</span>
                        </li>
                      )
                    })}
                  </ul>
                )}
              </Panel>
            </div>
          </div>
        )}
      </div>
    </>
  )
}

function StatCard({
  label,
  value,
  sub,
  subTone,
  loading,
}: {
  label: string
  value?: number
  sub?: string
  subTone?: 'warn' | 'bad'
  loading?: boolean
}) {
  const subCls = subTone === 'bad' ? 'text-red-400' : subTone === 'warn' ? 'text-amber-400' : 'text-slate-500'
  return (
    <div className="rounded-xl border border-slate-800 bg-[var(--surface)] px-4 py-3 shadow-card">
      <div className="text-[11px] font-semibold uppercase tracking-wider text-slate-500">{label}</div>
      <div className="mt-1 font-tech text-2xl font-semibold tabular-nums text-slate-100">
        {loading ? <span className="text-slate-600">—</span> : (value ?? 0)}
      </div>
      {sub && <div className={`mt-0.5 text-[11px] ${subCls}`}>{sub}</div>}
    </div>
  )
}

function Panel({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="rounded-xl border border-slate-800 bg-[var(--surface)] shadow-card">
      <div className="border-b border-slate-800 px-4 py-2.5 text-[13px] font-semibold text-slate-200">{title}</div>
      <div className="px-4 py-1">{children}</div>
    </section>
  )
}
