'use client'

import { useCallback, useEffect, useMemo, useState } from 'react'
import { useRouter } from 'next/navigation'
import { listSecrets, ApiError, SECRET_ACCESS_DISABLED_STATUS, type SecretRef } from '@/lib/api'
import { useCluster } from '@/lib/cluster-context'
import { ResourceToolbar } from '@/components/resource-toolbar'
import { LoadingState, ErrorState, EmptyState } from '@/components/view-states'
import { SecretAccessNotice } from '@/components/secret-access-notice'
import {
  denseTableCls,
  denseThRowCls,
  denseThCls,
  denseTdCls,
  certExpiryStatus,
  formatDate,
} from '@/lib/styles'

// A TLS secret carrying parsed cert data — the Certificates view operates on
// these, so we narrow SecretRef to a variant with a guaranteed `cert`.
type CertSecret = SecretRef & { cert: NonNullable<SecretRef['cert']> }

export default function CertificatesPage() {
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

  // Keep only TLS secrets with a parsed cert, then sort soonest-expiry first
  // (expired certs bubble to the very top via their negative remaining days).
  const certs = useMemo<CertSecret[]>(() => {
    const withCert = items.filter((s): s is CertSecret => Boolean(s.is_tls && s.cert))
    const q = search.trim().toLowerCase()
    const filtered = q
      ? withCert.filter(
          (s) =>
            s.name.toLowerCase().includes(q) ||
            s.namespace.toLowerCase().includes(q) ||
            s.cert.subject_cn.toLowerCase().includes(q) ||
            s.cert.issuer_cn.toLowerCase().includes(q),
        )
      : withCert
    return [...filtered].sort((a, b) => a.cert.expires_in_days - b.cert.expires_in_days)
  }, [items, search])

  const open = useCallback(
    (s: CertSecret) => {
      if (!cluster) return
      const qs = new URLSearchParams({ cluster, namespace: s.namespace, name: s.name })
      router.push(`/secrets/detail?${qs.toString()}`)
    },
    [cluster, router],
  )

  return (
    <>
      <ResourceToolbar
        title="Certificates"
        discoveredNamespaces={discoveredNamespaces}
        search={search}
        onSearch={setSearch}
        searchPlaceholder="Filter certificates…"
        onRefresh={() => setNonce((n) => n + 1)}
        refreshing={loading}
      />

      <div className="flex-1 overflow-auto">
        {!cluster ? (
          <EmptyState label="Select a cluster to list certificates." />
        ) : loading && items.length === 0 && !disabled ? (
          <LoadingState label="Loading certificates…" />
        ) : disabled ? (
          <SecretAccessNotice message={disabled} />
        ) : error ? (
          <ErrorState label="Failed to load certificates" error={error} />
        ) : certs.length === 0 ? (
          <EmptyState label={search ? 'No certificates match the filter.' : 'No TLS certificates in this namespace.'} />
        ) : (
          <table className={denseTableCls}>
            <thead>
              <tr className={denseThRowCls}>
                <th className={denseThCls}>Secret</th>
                <th className={denseThCls}>Namespace</th>
                <th className={denseThCls}>Subject CN</th>
                <th className={denseThCls}>Issuer</th>
                <th className={denseThCls}>Expires</th>
              </tr>
            </thead>
            <tbody>
              {certs.map((s) => {
                const status = certExpiryStatus(s.cert)
                return (
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
                    aria-label={`Open certificate ${s.name}`}
                    className="cursor-pointer border-b border-slate-800/50 transition-colors last:border-b-0 hover:bg-[var(--surface-hover)] focus-visible:bg-[var(--surface-hover)] focus-visible:outline-none"
                  >
                    <td className={`${denseTdCls} font-tech text-slate-200`}>{s.name}</td>
                    <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>{s.namespace}</td>
                    <td className={`${denseTdCls} font-tech text-[12px] text-slate-300`}>{s.cert.subject_cn || '—'}</td>
                    <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>{s.cert.issuer_cn || '—'}</td>
                    <td className={denseTdCls}>
                      <span className="inline-flex items-center gap-2">
                        <span className="font-tech text-[12px] text-slate-400">{formatDate(s.cert.not_after)}</span>
                        <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${status.cls}`}>
                          {status.label}
                        </span>
                      </span>
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
