'use client'

import { useCallback, useEffect, useMemo, useState } from 'react'
import { useRouter } from 'next/navigation'
import { listWorkloads, getWorkloadHistory, type WorkloadRef } from '@/lib/api'
import { useCluster } from '@/lib/cluster-context'
import { ResourceToolbar } from '@/components/resource-toolbar'
import { ImageTags } from '@/components/image-tag'
import { LoadingState, ErrorState, EmptyState } from '@/components/view-states'
import { denseTableCls, denseThRowCls, denseThCls, denseTdCls, kindStyle } from '@/lib/styles'

const wkey = (w: WorkloadRef) => `${w.namespace ?? ''}/${w.kind}/${w.name}`

export default function WorkloadsPage() {
  const router = useRouter()
  const { cluster, namespace } = useCluster()
  const [workloads, setWorkloads] = useState<WorkloadRef[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [nonce, setNonce] = useState(0)
  // Current image(s) per workload, fetched lazily from each workload's history
  // (the live revision). Keyed by wkey(); absent = still loading.
  const [images, setImages] = useState<Record<string, string[]>>({})

  const load = useCallback(() => {
    if (!cluster) return undefined
    let cancelled = false
    setLoading(true)
    setError(null)
    listWorkloads(cluster, namespace)
      .then((w) => {
        if (!cancelled) setWorkloads(w)
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

  // After the workload list arrives, resolve each one's current image from its
  // history (one call per workload, concurrent). The live revision's images are
  // shown; failures leave the row's image blank rather than erroring the page.
  useEffect(() => {
    if (!cluster || workloads.length === 0) return
    let cancelled = false
    setImages({})
    workloads.forEach((w) => {
      if (!w.namespace) return
      getWorkloadHistory(cluster, w.namespace, w.kind, w.name)
        .then((revs) => {
          if (cancelled) return
          const current = revs.find((r) => r.current) ?? revs[0]
          setImages((prev) => ({ ...prev, [wkey(w)]: current?.images ?? [] }))
        })
        .catch(() => {
          if (!cancelled) setImages((prev) => ({ ...prev, [wkey(w)]: [] }))
        })
    })
    return () => {
      cancelled = true
    }
  }, [workloads, cluster])

  const discoveredNamespaces = useMemo(
    () => workloads.map((w) => w.namespace).filter((n): n is string => !!n),
    [workloads],
  )

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return workloads
    return workloads.filter(
      (w) =>
        w.name.toLowerCase().includes(q) ||
        w.kind.toLowerCase().includes(q) ||
        (w.namespace ?? '').toLowerCase().includes(q),
    )
  }, [workloads, search])

  const openWorkload = useCallback(
    (w: WorkloadRef) => {
      const c = w.cluster || cluster
      if (!c || !w.namespace) return
      const qs = new URLSearchParams({ cluster: c, namespace: w.namespace, kind: w.kind, name: w.name })
      router.push(`/workloads/detail?${qs.toString()}`)
    },
    [cluster, router],
  )

  return (
    <>
      <ResourceToolbar
        title="Workloads"
        discoveredNamespaces={discoveredNamespaces}
        search={search}
        onSearch={setSearch}
        searchPlaceholder="Filter workloads…"
        onRefresh={() => setNonce((n) => n + 1)}
        refreshing={loading}
      />

      <div className="flex-1 overflow-auto">
        {!cluster ? (
          <EmptyState label="Select a cluster to list workloads." />
        ) : loading && workloads.length === 0 ? (
          <LoadingState label="Loading workloads…" />
        ) : error ? (
          <ErrorState label="Failed to load workloads" error={error} />
        ) : filtered.length === 0 ? (
          <EmptyState label={search ? 'No workloads match the filter.' : 'No workloads in this namespace.'} />
        ) : (
          <table className={denseTableCls}>
            <thead>
              <tr className={denseThRowCls}>
                <th className={denseThCls}>Kind</th>
                <th className={denseThCls}>Name</th>
                <th className={denseThCls}>Image</th>
                <th className={denseThCls}>Namespace</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((w) => (
                <tr
                  key={`${w.namespace ?? ''}/${w.kind}/${w.name}`}
                  onClick={() => openWorkload(w)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault()
                      openWorkload(w)
                    }
                  }}
                  tabIndex={0}
                  role="link"
                  aria-label={`Open workload ${w.kind}/${w.name}`}
                  className="cursor-pointer border-b border-slate-800/50 transition-colors last:border-b-0 hover:bg-[var(--surface-hover)] focus-visible:bg-[var(--surface-hover)] focus-visible:outline-none"
                >
                  <td className={denseTdCls}>
                    <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${kindStyle(w.kind)}`}>
                      {w.kind}
                    </span>
                  </td>
                  <td className={`${denseTdCls} font-tech text-slate-200`}>{w.name}</td>
                  <td className={`${denseTdCls} max-w-[18rem]`}>
                    {images[wkey(w)] === undefined ? (
                      <span className="text-slate-600">…</span>
                    ) : (
                      <ImageTags images={images[wkey(w)]} />
                    )}
                  </td>
                  <td className={`${denseTdCls} whitespace-nowrap font-tech text-[12px] text-slate-400`}>{w.namespace || '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  )
}
