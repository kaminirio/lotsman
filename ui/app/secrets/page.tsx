'use client'

import { useCallback, useEffect, useMemo, useState } from 'react'
import { useRouter } from 'next/navigation'
import { listSecrets, ApiError, SECRET_ACCESS_DISABLED_STATUS, type SecretRef } from '@/lib/api'
import { useCluster } from '@/lib/cluster-context'
import { ResourceToolbar } from '@/components/resource-toolbar'
import { LoadingState, ErrorState, EmptyState } from '@/components/view-states'
import { SecretAccessNotice } from '@/components/secret-access-notice'
import { denseTableCls, denseThRowCls, denseThCls, denseTdCls } from '@/lib/styles'

export default function SecretsPage() {
  const router = useRouter()
  const { cluster, namespace } = useCluster()
  const [items, setItems] = useState<SecretRef[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [disabled, setDisabled] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [nonce, setNonce] = useState(0)

  const load = useCallback(() => {
    if (!cluster) return undefined
    let cancelled = false
    setLoading(true)
    setError(null)
    setDisabled(null)
    listSecrets(cluster, namespace)
      .then((s) => {
        if (!cancelled) setItems(s)
      })
      .catch((e) => {
        if (cancelled) return
        if (e instanceof ApiError && e.status === SECRET_ACCESS_DISABLED_STATUS) {
          setDisabled(e.message)
          setItems([])
        } else {
          setError(e instanceof Error ? e.message : String(e))
        }
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

  const discoveredNamespaces = useMemo(() => items.map((s) => s.namespace), [items])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return items
    return items.filter(
      (s) =>
        s.name.toLowerCase().includes(q) ||
        s.namespace.toLowerCase().includes(q) ||
        s.type.toLowerCase().includes(q),
    )
  }, [items, search])

  const open = useCallback(
    (s: SecretRef) => {
      if (!cluster) return
      const qs = new URLSearchParams({ cluster, namespace: s.namespace, name: s.name })
      router.push(`/secrets/detail?${qs.toString()}`)
    },
    [cluster, router],
  )

  return (
    <>
      <ResourceToolbar
        title="Secrets"
        discoveredNamespaces={discoveredNamespaces}
        search={search}
        onSearch={setSearch}
        searchPlaceholder="Filter secrets…"
        onRefresh={() => setNonce((n) => n + 1)}
        refreshing={loading}
      />

      <div className="flex-1 overflow-auto">
        {!cluster ? (
          <EmptyState label="Select a cluster to list secrets." />
        ) : loading && items.length === 0 && !disabled ? (
          <LoadingState label="Loading secrets…" />
        ) : disabled ? (
          <SecretAccessNotice message={disabled} />
        ) : error ? (
          <ErrorState label="Failed to load secrets" error={error} />
        ) : filtered.length === 0 ? (
          <EmptyState label={search ? 'No secrets match the filter.' : 'No secrets in this namespace.'} />
        ) : (
          <table className={denseTableCls}>
            <thead>
              <tr className={denseThRowCls}>
                <th className={denseThCls}>Name</th>
                <th className={denseThCls}>Namespace</th>
                <th className={denseThCls}>Type</th>
                <th className={denseThCls}>Keys</th>
                <th className={denseThCls}>TLS</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((s) => (
                <tr
                  key={`${s.namespace}/${s.name}`}
                  onClick={() => open(s)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault()
                      open(s)
                    }
                  }}
                  tabIndex={0}
                  role="link"
                  aria-label={`Open secret ${s.name}`}
                  className="cursor-pointer border-b border-slate-800/50 transition-colors last:border-b-0 hover:bg-[var(--surface-hover)] focus-visible:bg-[var(--surface-hover)] focus-visible:outline-none"
                >
                  <td className={`${denseTdCls} font-tech text-slate-200`}>{s.name}</td>
                  <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>{s.namespace}</td>
                  <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>{s.type}</td>
                  <td className={`${denseTdCls} font-tech text-slate-400`}>{s.keys.length}</td>
                  <td className={denseTdCls}>
                    {s.is_tls ? (
                      <span className="rounded px-1.5 py-0.5 text-[11px] font-medium bg-sky-500/10 text-sky-400 ring-1 ring-sky-500/20">
                        TLS
                      </span>
                    ) : (
                      <span className="text-slate-600">—</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  )
}
