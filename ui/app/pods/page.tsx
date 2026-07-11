'use client'

import { useCallback, useEffect, useMemo, useState } from 'react'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { listPods, type Pod } from '@/lib/api'
import { useCluster } from '@/lib/cluster-context'
import { ResourceToolbar } from '@/components/resource-toolbar'
import { ContainerSquares } from '@/components/container-squares'
import { ImageTags } from '@/components/image-tag'
import { LoadingState, ErrorState, EmptyState } from '@/components/view-states'
import { useLivePoll } from '@/lib/use-live'
import { denseTableCls, denseThRowCls, denseThCls, denseTdCls, kindStyle, podPhaseStyle, podPhaseDot, focusRingCls } from '@/lib/styles'

export default function PodsPage() {
  const router = useRouter()
  const { cluster, namespace } = useCluster()
  const [pods, setPods] = useState<Pod[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [nonce, setNonce] = useState(0)

  const load = useCallback(() => {
    if (!cluster) return undefined
    let cancelled = false
    setLoading(true)
    setError(null)
    listPods(cluster, namespace)
      .then((p) => {
        if (!cancelled) setPods(p)
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

  // Pods have no SSE stream; auto-refresh on a visibility-aware interval.
  useLivePoll(() => setNonce((n) => n + 1), { enabled: !!cluster })

  const discoveredNamespaces = useMemo(() => pods.map((p) => p.namespace), [pods])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return pods
    return pods.filter(
      (p) =>
        p.name.toLowerCase().includes(q) ||
        p.namespace.toLowerCase().includes(q) ||
        (p.owner?.name ?? '').toLowerCase().includes(q) ||
        (p.node ?? '').toLowerCase().includes(q),
    )
  }, [pods, search])

  const openPod = useCallback(
    (p: Pod) => {
      if (!cluster) return
      const qs = new URLSearchParams({ cluster, namespace: p.namespace, pod: p.name })
      router.push(`/pods/detail?${qs.toString()}`)
    },
    [cluster, router],
  )

  return (
    <>
      <ResourceToolbar
        title="Pods"
        discoveredNamespaces={discoveredNamespaces}
        search={search}
        onSearch={setSearch}
        searchPlaceholder="Filter pods…"
        onRefresh={() => setNonce((n) => n + 1)}
        refreshing={loading}
      />

      <div className="flex-1 overflow-auto">
        {!cluster ? (
          <EmptyState label="Select a cluster to list pods." />
        ) : loading && pods.length === 0 ? (
          <LoadingState label="Loading pods…" />
        ) : error ? (
          <ErrorState label="Failed to load pods" error={error} />
        ) : filtered.length === 0 ? (
          <EmptyState label={search ? 'No pods match the filter.' : 'No pods in this namespace.'} />
        ) : (
          <table className={denseTableCls}>
            <thead>
              <tr className={denseThRowCls}>
                <th className={denseThCls}>Name</th>
                <th className={denseThCls}>Owner</th>
                <th className={denseThCls}>Namespace</th>
                <th className={denseThCls}>Containers</th>
                <th className={denseThCls}>Image</th>
                <th className={denseThCls}>Phase</th>
                <th className={denseThCls}>Ready</th>
                <th className={denseThCls}>Restarts</th>
                <th className={denseThCls}>Node</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((p) => (
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
                    {p.owner ? (
                      <Link
                        href={`/workloads/detail?${new URLSearchParams({
                          cluster: cluster ?? '',
                          namespace: p.namespace,
                          kind: p.owner.kind,
                          name: p.owner.name,
                        }).toString()}`}
                        onClick={(e) => e.stopPropagation()}
                        title={`${p.owner.kind}/${p.owner.name}`}
                        aria-label={`Open workload ${p.owner.kind}/${p.owner.name}`}
                        className={`inline-flex items-center transition-opacity hover:opacity-80 ${focusRingCls}`}
                      >
                        <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${kindStyle(p.owner.kind)}`}>
                          {p.owner.kind}
                        </span>
                      </Link>
                    ) : (
                      <span className="text-slate-600">—</span>
                    )}
                  </td>
                  <td className={`${denseTdCls} whitespace-nowrap font-tech text-[12px] text-slate-400`}>{p.namespace}</td>
                  <td className={denseTdCls}>
                    <ContainerSquares containers={p.containers} />
                  </td>
                  <td className={`${denseTdCls} max-w-[18rem]`}>
                    <ImageTags images={p.containers.map((c) => c.image)} />
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
      </div>
    </>
  )
}
