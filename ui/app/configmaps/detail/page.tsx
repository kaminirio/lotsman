'use client'

import { Suspense, useEffect, useMemo, useState } from 'react'
import Link from 'next/link'
import { useSearchParams } from 'next/navigation'
import { getConfigMap, type ConfigMapDetail } from '@/lib/api'
import { NamespaceLink } from '@/components/namespace-link'
import { LoadingState, ErrorState, EmptyState } from '@/components/view-states'
import { CopyButton } from '@/components/copy-button'
import { focusRingCls } from '@/lib/styles'

/**
 * ConfigMap detail — full-window page. Identifiers come from the URL query
 * (?cluster=&namespace=&name=) so a link always resolves; `useSearchParams`
 * needs a Suspense boundary under `output: 'export'`. An optional `?key=` query
 * highlights a specific data key (used when arriving from an env source chip).
 */
export default function ConfigMapDetailPage() {
  return (
    <Suspense fallback={<LoadingState label="Loading configmap…" />}>
      <ConfigMapDetailView />
    </Suspense>
  )
}

function ConfigMapDetailView() {
  const params = useSearchParams()
  const cluster = params.get('cluster') ?? ''
  const namespace = params.get('namespace') ?? ''
  const name = params.get('name') ?? ''
  const highlightKey = params.get('key') ?? ''

  const [cm, setCm] = useState<ConfigMapDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [view, setView] = useState<'table' | 'raw'>('table')

  useEffect(() => {
    if (!cluster || !namespace || !name) {
      setLoading(false)
      setError(null)
      setCm(null)
      return
    }
    let cancelled = false
    setLoading(true)
    setError(null)
    getConfigMap(cluster, namespace, name)
      .then((d) => {
        if (!cancelled) setCm(d)
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
  }, [cluster, namespace, name])

  const rows = useMemo(() => (cm ? Object.entries(cm.data) : []), [cm])
  const raw = useMemo(() => (cm ? toYaml(cm.data) : ''), [cm])

  return (
    <>
      <div className="border-b border-slate-800 px-6 py-4">
        <Link
          href="/configmaps"
          className={`inline-flex items-center gap-1.5 text-[12px] text-slate-500 transition-colors hover:text-slate-300 ${focusRingCls}`}
        >
          <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.9} aria-hidden="true">
            <path strokeLinecap="round" strokeLinejoin="round" d="M15 19l-7-7 7-7" />
          </svg>
          Back to ConfigMaps
        </Link>

        {cm && (
          <>
            <h1 className="mt-3 truncate font-tech text-lg font-semibold text-slate-100">{cm.name}</h1>
            <p className="mt-1 font-tech text-[12px] text-slate-500">
              {cluster} / <NamespaceLink cluster={cluster} namespace={cm.namespace} /> · {rows.length} key{rows.length === 1 ? '' : 's'}
            </p>
          </>
        )}
      </div>

      <div className="flex-1 overflow-auto px-6 py-5">
        {loading ? (
          <LoadingState label="Loading configmap…" />
        ) : error ? (
          <ErrorState label="Failed to load configmap" error={error} />
        ) : !cluster || !namespace || !name ? (
          <EmptyState label="Missing configmap identifiers in the URL." />
        ) : !cm ? (
          <EmptyState label={`ConfigMap "${name}" not found in ${cluster} / ${namespace}.`} />
        ) : rows.length === 0 ? (
          <EmptyState label="This configmap has no data." />
        ) : (
          <div className="max-w-5xl space-y-3">
            <div className="flex items-center justify-between">
              <div
                role="radiogroup"
                aria-label="ConfigMap view"
                className="inline-flex h-8 items-center rounded-md border border-slate-700 bg-[var(--surface-2)] p-0.5"
              >
                {(['table', 'raw'] as const).map((v) => (
                  <button
                    key={v}
                    type="button"
                    role="radio"
                    aria-checked={view === v}
                    onClick={() => setView(v)}
                    className={[
                      'rounded px-2.5 py-1 text-xs font-medium capitalize transition-colors',
                      view === v ? 'bg-[var(--accent)] text-white' : 'text-slate-400 hover:text-slate-200',
                      focusRingCls,
                    ].join(' ')}
                  >
                    {v}
                  </button>
                ))}
              </div>
              {view === 'raw' && <CopyButton value={raw} label="raw configmap" />}
            </div>

            {view === 'raw' ? (
              <pre className="overflow-auto rounded-lg border border-slate-800 bg-[var(--bg-deep)] p-3 font-mono text-[12px] leading-relaxed text-slate-300">
                {raw}
              </pre>
            ) : (
              <div className="overflow-hidden rounded-lg border border-slate-800">
                <table className="w-full text-[13px]">
              <thead>
                <tr className="border-b border-slate-800 bg-[var(--surface-2)]">
                  <th className="w-1/4 px-3 py-2 text-left text-[11px] font-semibold uppercase tracking-wider text-slate-500">
                    Key
                  </th>
                  <th className="px-3 py-2 text-left text-[11px] font-semibold uppercase tracking-wider text-slate-500">
                    Value
                  </th>
                </tr>
              </thead>
              <tbody>
                {rows.map(([key, value]) => {
                  const highlighted = highlightKey !== '' && key === highlightKey
                  return (
                    <tr
                      key={key}
                      className={[
                        'border-b border-slate-800/60 last:border-b-0',
                        highlighted ? 'bg-[var(--accent-soft)]' : '',
                      ].join(' ')}
                    >
                      <td className="px-3 py-2 align-top font-tech text-slate-200">{key}</td>
                      <td className="px-3 py-2 align-top">
                        <div className="flex items-start justify-between gap-2">
                          <pre className="min-w-0 flex-1 overflow-x-auto whitespace-pre-wrap break-words font-mono text-[12px] leading-relaxed text-slate-300">
                            {value}
                          </pre>
                          <CopyButton value={value} label={`${key} value`} />
                        </div>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
              </div>
            )}
          </div>
        )}
      </div>
    </>
  )
}

// toYaml renders a ConfigMap's data map as a kubectl-style `data:` YAML section:
// single-line values inline, multi-line values as block scalars (`key: |`).
function toYaml(data: Record<string, string>): string {
  const keys = Object.keys(data).sort()
  if (keys.length === 0) return 'data: {}'
  const lines = ['data:']
  for (const k of keys) {
    const v = data[k]
    if (v.includes('\n')) {
      lines.push(`  ${k}: |`)
      for (const ln of v.replace(/\n$/, '').split('\n')) lines.push(`    ${ln}`)
    } else {
      lines.push(`  ${k}: ${v}`)
    }
  }
  return lines.join('\n')
}
