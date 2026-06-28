'use client'

import { useCallback, useEffect, useMemo, useState } from 'react'
import { useRouter } from 'next/navigation'
import { listConfigMaps, type ConfigMapRef } from '@/lib/api'
import { useCluster } from '@/lib/cluster-context'
import { ResourceToolbar } from '@/components/resource-toolbar'
import { LoadingState, ErrorState, EmptyState } from '@/components/view-states'
import { denseTableCls, denseThRowCls, denseThCls, denseTdCls } from '@/lib/styles'

export default function ConfigMapsPage() {
  const router = useRouter()
  const { cluster, namespace } = useCluster()
  const [items, setItems] = useState<ConfigMapRef[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [nonce, setNonce] = useState(0)

  const load = useCallback(() => {
    if (!cluster) return undefined
    let cancelled = false
    setLoading(true)
    setError(null)
    listConfigMaps(cluster, namespace)
      .then((c) => {
        if (!cancelled) setItems(c)
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

  const discoveredNamespaces = useMemo(() => items.map((c) => c.namespace), [items])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return items
    return items.filter(
      (c) => c.name.toLowerCase().includes(q) || c.namespace.toLowerCase().includes(q),
    )
  }, [items, search])

  const open = useCallback(
    (c: ConfigMapRef) => {
      if (!cluster) return
      const qs = new URLSearchParams({ cluster, namespace: c.namespace, name: c.name })
      router.push(`/configmaps/detail?${qs.toString()}`)
    },
    [cluster, router],
  )

  return (
    <>
      <ResourceToolbar
        title="ConfigMaps"
        discoveredNamespaces={discoveredNamespaces}
        search={search}
        onSearch={setSearch}
        searchPlaceholder="Filter configmaps…"
        onRefresh={() => setNonce((n) => n + 1)}
        refreshing={loading}
      />

      <div className="flex-1 overflow-auto">
        {!cluster ? (
          <EmptyState label="Select a cluster to list configmaps." />
        ) : loading && items.length === 0 ? (
          <LoadingState label="Loading configmaps…" />
        ) : error ? (
          <ErrorState label="Failed to load configmaps" error={error} />
        ) : filtered.length === 0 ? (
          <EmptyState label={search ? 'No configmaps match the filter.' : 'No configmaps in this namespace.'} />
        ) : (
          <table className={denseTableCls}>
            <thead>
              <tr className={denseThRowCls}>
                <th className={denseThCls}>Name</th>
                <th className={denseThCls}>Namespace</th>
                <th className={denseThCls}>Keys</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((c) => (
                <tr
                  key={`${c.namespace}/${c.name}`}
                  onClick={() => open(c)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault()
                      open(c)
                    }
                  }}
                  tabIndex={0}
                  role="link"
                  aria-label={`Open configmap ${c.name}`}
                  className="cursor-pointer border-b border-slate-800/50 transition-colors last:border-b-0 hover:bg-[var(--surface-hover)] focus-visible:bg-[var(--surface-hover)] focus-visible:outline-none"
                >
                  <td className={`${denseTdCls} font-tech text-slate-200`}>{c.name}</td>
                  <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>{c.namespace}</td>
                  <td className={`${denseTdCls} font-tech text-slate-400`}>{c.keys.length}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  )
}
