'use client'

import { Suspense, useCallback, useEffect, useState } from 'react'
import Link from 'next/link'
import { useRouter, useSearchParams } from 'next/navigation'
import { listPods, getWorkloadHistory, type Pod, type WorkloadRevision } from '@/lib/api'
import { NamespaceLink } from '@/components/namespace-link'
import { ContainerSquares } from '@/components/container-squares'
import { ImageTags } from '@/components/image-tag'
import { LoadingState, ErrorState, EmptyState } from '@/components/view-states'
import {
  denseTableCls,
  denseThRowCls,
  denseThCls,
  denseTdCls,
  kindStyle,
  podPhaseStyle,
  podPhaseDot,
  focusRingCls,
  relativeTime,
} from '@/lib/styles'

/**
 * Workload detail — the destination for a pod's "Owner" chip and a Workloads
 * list row. Identifiers come from the URL query (?cluster=&namespace=&kind=
 * &name=) so the link is shareable and independent of the cross-view cluster
 * context. The body lists the workload's pods (server-side narrowed via the
 * `?workload=` pods filter), each row linking on to the pod detail.
 * `useSearchParams` needs a Suspense boundary under `output: 'export'`.
 */
export default function WorkloadDetailPage() {
  return (
    <Suspense fallback={<LoadingState label="Loading workload…" />}>
      <WorkloadDetail />
    </Suspense>
  )
}

function WorkloadDetail() {
  const router = useRouter()
  const params = useSearchParams()
  const cluster = params.get('cluster') ?? ''
  const namespace = params.get('namespace') ?? ''
  const kind = params.get('kind') ?? ''
  const name = params.get('name') ?? ''

  const [pods, setPods] = useState<Pod[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [history, setHistory] = useState<WorkloadRevision[]>([])

  useEffect(() => {
    if (!cluster || !namespace || !name) {
      setLoading(false)
      setError(null)
      setPods([])
      setHistory([])
      return
    }
    let cancelled = false
    setLoading(true)
    setError(null)
    listPods(cluster, namespace, name)
      .then((p) => {
        if (!cancelled) setPods(p)
      })
      .catch((e) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    // Image/tag history is best-effort: a failure leaves the section empty
    // rather than failing the whole page.
    getWorkloadHistory(cluster, namespace, kind, name)
      .then((h) => {
        if (!cancelled) setHistory(h)
      })
      .catch(() => {
        if (!cancelled) setHistory([])
      })
    return () => {
      cancelled = true
    }
  }, [cluster, namespace, kind, name])

  const current = history.find((r) => r.current) ?? history[0]

  const openPod = useCallback(
    (p: Pod) => {
      const qs = new URLSearchParams({ cluster, namespace: p.namespace, pod: p.name })
      router.push(`/pods/detail?${qs.toString()}`)
    },
    [cluster, router],
  )

  return (
    <>
      {/* Header */}
      <div className="border-b border-slate-800 px-6 py-4">
        <Link
          href="/workloads"
          className={`inline-flex items-center gap-1.5 text-[12px] text-slate-500 transition-colors hover:text-slate-300 ${focusRingCls}`}
        >
          <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.9} aria-hidden="true">
            <path strokeLinecap="round" strokeLinejoin="round" d="M15 19l-7-7 7-7" />
          </svg>
          Back to Workloads
        </Link>

        <div className="mt-3 flex items-center gap-2.5">
          {kind && (
            <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${kindStyle(kind)}`}>{kind}</span>
          )}
          <h1 className="truncate font-tech text-lg font-semibold text-slate-100">{name}</h1>
        </div>
        {namespace && (
          <p className="mt-1 font-tech text-[12px] text-slate-500">
            {cluster} / <NamespaceLink cluster={cluster} namespace={namespace} />
          </p>
        )}
        {current && current.images.length > 0 && (
          <div className="mt-2 flex items-center gap-2">
            <span className="text-[11px] font-semibold uppercase tracking-wider text-slate-600">Image</span>
            <ImageTags images={current.images} />
          </div>
        )}
      </div>

      {/* Body: image/tag history, then the workload's pods */}
      <div className="flex-1 space-y-8 overflow-auto px-6 py-5">
        {/* Image history (tag replacements over revisions) */}
        {history.length > 0 && (
          <section className="space-y-2">
            <h2 className="text-[11px] font-semibold uppercase tracking-wider text-slate-500">
              Image history
            </h2>
            <div className="overflow-hidden rounded-lg border border-slate-800">
              <table className="w-full text-[13px]">
                <thead>
                  <tr className={denseThRowCls}>
                    <th className={`${denseThCls} w-20`}>Revision</th>
                    <th className={denseThCls}>Image</th>
                    <th className={denseThCls}>Rolled out</th>
                    <th className={denseThCls}>Change cause</th>
                  </tr>
                </thead>
                <tbody>
                  {history.map((rev) => (
                    <tr
                      key={rev.revision}
                      className={`border-b border-slate-800/60 last:border-b-0 ${rev.current ? 'bg-emerald-500/[0.04]' : ''}`}
                    >
                      <td className={`${denseTdCls} font-tech text-slate-300`}>
                        <span className="inline-flex items-center gap-1.5">
                          #{rev.revision}
                          {rev.current && (
                            <span className="rounded bg-emerald-500/10 px-1 py-0.5 text-[10px] font-medium text-emerald-400 ring-1 ring-emerald-500/20">
                              current
                            </span>
                          )}
                        </span>
                      </td>
                      <td className={`${denseTdCls} max-w-[24rem]`}>
                        <ImageTags images={rev.images} />
                      </td>
                      <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>
                        {rev.created_at ? relativeTime(rev.created_at) : '—'}
                      </td>
                      <td className={`${denseTdCls} text-[12px] text-slate-400`}>
                        {rev.change_cause || <span className="text-slate-600">—</span>}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        )}

        {/* Pods */}
        <section className="space-y-2">
          <h2 className="text-[11px] font-semibold uppercase tracking-wider text-slate-500">Pods</h2>
          {loading ? (
            <LoadingState label="Loading pods…" />
          ) : error ? (
            <ErrorState label="Failed to load pods" error={error} />
          ) : !cluster || !namespace || !name ? (
            <EmptyState label="Missing workload identifiers in the URL." />
          ) : pods.length === 0 ? (
            <EmptyState label={`No pods for ${kind}/${name}.`} />
          ) : (
            <table className={denseTableCls}>
              <thead>
                <tr className={denseThRowCls}>
                  <th className={denseThCls}>Pod</th>
                  <th className={denseThCls}>Containers</th>
                  <th className={denseThCls}>Phase</th>
                  <th className={denseThCls}>Ready</th>
                  <th className={denseThCls}>Restarts</th>
                  <th className={denseThCls}>Node</th>
                </tr>
              </thead>
              <tbody>
                {pods.map((p) => (
                  <tr
                    key={`${p.namespace}/${p.name}`}
                    onClick={() => openPod(p)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault()
                        openPod(p)
                      }
                    }}
                    tabIndex={0}
                    role="link"
                    aria-label={`Open pod ${p.name}`}
                    className="cursor-pointer border-b border-slate-800/50 transition-colors last:border-b-0 hover:bg-[var(--surface-hover)] focus-visible:bg-[var(--surface-hover)] focus-visible:outline-none"
                  >
                    <td className={`${denseTdCls} font-tech text-slate-200`}>{p.name}</td>
                    <td className={denseTdCls}>
                      <ContainerSquares containers={p.containers} />
                    </td>
                    <td className={denseTdCls}>
                      <span className="inline-flex items-center gap-1.5">
                        <span className={`h-2 w-2 rounded-full ${podPhaseDot(p.phase)}`} aria-hidden="true" />
                        <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${podPhaseStyle(p.phase)}`}>
                          {p.phase}
                        </span>
                      </span>
                    </td>
                    <td className={denseTdCls}>
                      <span className={p.ready ? 'text-emerald-400' : 'text-slate-500'}>
                        {p.ready ? 'Ready' : 'Not ready'}
                      </span>
                    </td>
                    <td className={`${denseTdCls} font-tech text-slate-400`}>{p.restarts}</td>
                    <td className={`${denseTdCls} font-tech text-[12px] text-slate-500`}>{p.node || '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </section>
      </div>
    </>
  )
}
