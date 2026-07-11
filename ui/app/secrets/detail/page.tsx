'use client'

import { Suspense, useEffect, useState } from 'react'
import Link from 'next/link'
import { useSearchParams } from 'next/navigation'
import {
  getSecret,
  ApiError,
  SECRET_ACCESS_DISABLED_STATUS,
  type SecretDetail,
  type SecretEntry,
} from '@/lib/api'
import { NamespaceLink } from '@/components/namespace-link'
import { LoadingState, ErrorState, EmptyState } from '@/components/view-states'
import { SecretAccessNotice } from '@/components/secret-access-notice'
import { CertPanel } from '@/components/cert-panel'
import { CopyButton } from '@/components/copy-button'
import { focusRingCls, maskValue } from '@/lib/styles'

/**
 * Secret detail — full-window page. Identifiers come from the URL query
 * (?cluster=&namespace=&name=) so a link always resolves; `useSearchParams`
 * needs a Suspense boundary under `output: 'export'`. An optional `?key=`
 * highlights a specific data key (used when arriving from an env source chip).
 * When the agent's secret reveal/RBAC is disabled the route returns 502 and we
 * render the "secret access not enabled" notice rather than an error.
 */
export default function SecretDetailPage() {
  return (
    <Suspense fallback={<LoadingState label="Loading secret…" />}>
      <SecretDetailView />
    </Suspense>
  )
}

function SecretDetailView() {
  const params = useSearchParams()
  const cluster = params.get('cluster') ?? ''
  const namespace = params.get('namespace') ?? ''
  const name = params.get('name') ?? ''
  const highlightKey = params.get('key') ?? ''

  const [secret, setSecret] = useState<SecretDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [disabled, setDisabled] = useState<string | null>(null)
  // Values are masked in the UI by default (even when the API returned them for
  // an admin) — shoulder-surf protection. `revealed` holds the keys the user has
  // un-masked via the per-row eye or the "Reveal all" button.
  const [revealed, setRevealed] = useState<Set<string>>(new Set())

  useEffect(() => {
    if (!cluster || !namespace || !name) {
      setLoading(false)
      setError(null)
      setSecret(null)
      return
    }
    let cancelled = false
    setLoading(true)
    setError(null)
    setDisabled(null)
    setRevealed(new Set())
    getSecret(cluster, namespace, name)
      .then((d) => {
        if (!cancelled) setSecret(d)
      })
      .catch((e) => {
        if (cancelled) return
        if (e instanceof ApiError && e.status === SECRET_ACCESS_DISABLED_STATUS) {
          setDisabled(e.message)
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
  }, [cluster, namespace, name])

  return (
    <>
      <div className="border-b border-slate-800 px-6 py-4">
        <Link
          href="/secrets"
          className={`inline-flex items-center gap-1.5 text-[12px] text-slate-500 transition-colors hover:text-slate-300 ${focusRingCls}`}
        >
          <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.9} aria-hidden="true">
            <path strokeLinecap="round" strokeLinejoin="round" d="M15 19l-7-7 7-7" />
          </svg>
          Back to Secrets
        </Link>

        {secret && (
          <>
            <div className="mt-3 flex items-center gap-2.5">
              <h1 className="truncate font-tech text-lg font-semibold text-slate-100">{secret.name}</h1>
              {secret.cert && (
                <span className="rounded px-1.5 py-0.5 text-[11px] font-medium bg-sky-500/10 text-sky-400 ring-1 ring-sky-500/20">
                  TLS
                </span>
              )}
            </div>
            <p className="mt-1 font-tech text-[12px] text-slate-500">
              {cluster} / <NamespaceLink cluster={cluster} namespace={secret.namespace} /> · {secret.type}
            </p>
          </>
        )}
      </div>

      <div className="flex-1 overflow-auto px-6 py-5">
        {loading ? (
          <LoadingState label="Loading secret…" />
        ) : disabled ? (
          <SecretAccessNotice message={disabled} />
        ) : error ? (
          <ErrorState label="Failed to load secret" error={error} />
        ) : !cluster || !namespace || !name ? (
          <EmptyState label="Missing secret identifiers in the URL." />
        ) : !secret ? (
          <EmptyState label={`Secret "${name}" not found in ${cluster} / ${namespace}.`} />
        ) : (
          <div className="max-w-5xl space-y-6">
            {secret.cert && <CertPanel cert={secret.cert} />}

            {secret.entries.length === 0 ? (
              <EmptyState label="This secret has no data." />
            ) : (
              (() => {
                const revealable = secret.entries
                  .filter((e) => e.value !== undefined && !e.masked)
                  .map((e) => e.key)
                const allRevealed =
                  revealable.length > 0 && revealable.every((k) => revealed.has(k))
                const toggleAll = () =>
                  setRevealed(allRevealed ? new Set() : new Set(revealable))
                const toggleOne = (key: string) =>
                  setRevealed((prev) => {
                    const next = new Set(prev)
                    if (next.has(key)) next.delete(key)
                    else next.add(key)
                    return next
                  })
                return (
                  <div className="space-y-2">
                    <div className="flex items-center justify-between">
                      <span className="text-[11px] font-semibold uppercase tracking-wider text-slate-500">
                        Data ({secret.entries.length})
                      </span>
                      {revealable.length > 0 && (
                        <button
                          type="button"
                          onClick={toggleAll}
                          className={`inline-flex items-center gap-1.5 rounded-md border border-slate-700 bg-[var(--surface-2)] px-2.5 py-1 text-[12px] text-slate-300 transition-colors hover:bg-[var(--surface-hover)] ${focusRingCls}`}
                        >
                          {allRevealed ? <EyeOffIcon /> : <EyeIcon />}
                          {allRevealed ? 'Hide all' : 'Reveal all'}
                        </button>
                      )}
                    </div>
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
                            <th className="px-3 py-2 text-left text-[11px] font-semibold uppercase tracking-wider text-slate-500">
                              Flags
                            </th>
                          </tr>
                        </thead>
                        <tbody>
                          {secret.entries.map((e) => (
                            <SecretEntryRow
                              key={e.key}
                              entry={e}
                              highlighted={highlightKey !== '' && e.key === highlightKey}
                              revealed={revealed.has(e.key)}
                              onToggle={() => toggleOne(e.key)}
                            />
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </div>
                )
              })()
            )}
          </div>
        )}
      </div>
    </>
  )
}

function SecretEntryRow({
  entry,
  highlighted,
  revealed,
  onToggle,
}: {
  entry: SecretEntry
  highlighted: boolean
  revealed: boolean
  onToggle: () => void
}) {
  const hasValue = entry.value !== undefined && !entry.masked
  return (
    <tr
      className={[
        'border-b border-slate-800/60 last:border-b-0',
        highlighted ? 'bg-[var(--accent-soft)]' : entry.masked ? 'bg-amber-500/[0.03]' : '',
      ].join(' ')}
    >
      <td className="px-3 py-2 align-top font-tech text-slate-200">{entry.key}</td>
      <td className="px-3 py-2 align-top">
        {entry.masked ? (
          // Server-masked (RBAC): no value was sent — nothing to reveal.
          <span className="inline-flex items-center gap-1.5 text-slate-500">
            <LockIcon />
            <span className="font-tech">•••••• (masked)</span>
          </span>
        ) : hasValue ? (
          <div className="flex items-start justify-between gap-2">
            {revealed ? (
              <pre className="min-w-0 flex-1 overflow-x-auto whitespace-pre-wrap break-words font-mono text-[12px] leading-relaxed text-slate-300">
                {entry.value}
              </pre>
            ) : (
              <span className="min-w-0 flex-1 select-none font-mono text-[12px] text-slate-500">
                {maskValue(entry.value!)}
              </span>
            )}
            <div className="flex shrink-0 items-center gap-1">
              <button
                type="button"
                onClick={onToggle}
                aria-label={revealed ? `Hide ${entry.key}` : `Reveal ${entry.key}`}
                aria-pressed={revealed}
                className={`rounded p-1 text-slate-500 transition-colors hover:bg-[var(--surface-hover)] hover:text-slate-300 ${focusRingCls}`}
              >
                {revealed ? <EyeOffIcon /> : <EyeIcon />}
              </button>
              {/* Copy is always available — copies the value to the clipboard
                  without ever displaying it on screen. */}
              <CopyButton value={entry.value!} label={`${entry.key} value`} />
            </div>
          </div>
        ) : (
          <span className="text-slate-600">—</span>
        )}
      </td>
      <td className="px-3 py-2 align-top">
        {entry.is_cert ? (
          <span className="rounded px-1.5 py-0.5 text-[10px] font-medium bg-sky-500/10 text-sky-400 ring-1 ring-sky-500/20">
            certificate
          </span>
        ) : (
          <span className="text-slate-600">—</span>
        )}
      </td>
    </tr>
  )
}

function LockIcon() {
  return (
    <svg
      className="h-3.5 w-3.5 text-slate-500"
      fill="none"
      viewBox="0 0 24 24"
      stroke="currentColor"
      strokeWidth={1.8}
      aria-hidden="true"
    >
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        d="M16.5 10.5V6.75a4.5 4.5 0 10-9 0v3.75m-.75 0h10.5a2.25 2.25 0 012.25 2.25v6a2.25 2.25 0 01-2.25 2.25H6.75a2.25 2.25 0 01-2.25-2.25v-6a2.25 2.25 0 012.25-2.25z"
      />
    </svg>
  )
}

function EyeIcon() {
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8} aria-hidden="true">
      <path strokeLinecap="round" strokeLinejoin="round" d="M2.036 12.322a1.012 1.012 0 010-.639C3.423 7.51 7.36 4.5 12 4.5c4.638 0 8.573 3.007 9.963 7.178.07.207.07.431 0 .639C20.577 16.49 16.64 19.5 12 19.5c-4.638 0-8.573-3.007-9.963-7.178z" />
      <path strokeLinecap="round" strokeLinejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
    </svg>
  )
}

function EyeOffIcon() {
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8} aria-hidden="true">
      <path strokeLinecap="round" strokeLinejoin="round" d="M3.98 8.223A10.477 10.477 0 001.934 12C3.226 16.338 7.244 19.5 12 19.5c.993 0 1.953-.138 2.863-.395M6.228 6.228A10.45 10.45 0 0112 4.5c4.756 0 8.773 3.162 10.065 7.498a10.523 10.523 0 01-4.293 5.774M6.228 6.228L3 3m3.228 3.228l3.65 3.65m7.894 7.894L21 21m-3.228-3.228l-3.65-3.65m0 0a3 3 0 10-4.243-4.243m4.243 4.243L9.88 9.88" />
    </svg>
  )
}
