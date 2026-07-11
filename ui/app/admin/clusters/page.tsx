'use client'

// Admin Clusters view: manage clusters and onboard new ones. Lists known
// clusters, enrolls a new one by minting a per-cluster token and assembling a
// ready-to-run `helm install` command (public OCI chart, token baked in), and
// lists / revokes existing enrollment tokens. Consolidates the former
// /admin/enrollment page. Admin-gated like the RBAC view (401 unauthenticated,
// 403 non-admin); the control plane returns the plaintext token only once on
// create, never on list.

import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  ApiError,
  createEnrollmentToken,
  getEnrollmentDefaults,
  listEnrollmentTokens,
  revokeEnrollmentToken,
  type Cluster,
  type EnrollmentDefaults,
  type EnrollmentToken,
  type EnrollmentTokenCreated,
} from '@/lib/api'
import { useAuth } from '@/lib/auth-context'
import { useCluster } from '@/lib/cluster-context'
import { CopyButton } from '@/components/copy-button'
import { LoadingState, ErrorState, EmptyState } from '@/components/view-states'
import {
  denseTableCls,
  denseThRowCls,
  denseThCls,
  denseTdCls,
  denseRowCls,
  toolbarInputCls,
  toolbarBtnCls,
  formatDate,
  focusRingCls,
  envBadgeStyle,
} from '@/lib/styles'

// TTL options offered in the enroll panel. Value is ttl_hours; 0 = never.
const TTL_OPTIONS: { label: string; hours: number }[] = [
  { label: 'Never', hours: 0 },
  { label: '24 hours', hours: 24 },
  { label: '7 days', hours: 168 },
  { label: '30 days', hours: 720 },
]

// Placeholder used when the control plane doesn't know its externally reachable
// agent-gateway address; the operator edits it before running the command.
const GATEWAY_PLACEHOLDER = 'CONTROL_PLANE_HOST:9090'

// Cluster name must be DNS-label-ish so it's safe to interpolate unquoted into
// the generated `helm --set config.cluster=…` command: Helm treats `,` as a
// value separator, and spaces / `=` / `[` would break the command. Restricting
// to lowercase letters, digits, `-` and `.` keeps the copy-paste command intact.
const CLUSTER_NAME_RE = /^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$/
const CLUSTER_NAME_HINT = "Use lowercase letters, digits, '-' and '.' (e.g. prod-eu)."

// Message shown when enrollment is unavailable because the control plane has no
// durable store (matches the backend 503 on mint).
const NO_DURABLE_MSG =
  'Agent enrollment is disabled: the control plane has no durable store. Set LOTSMAN_DATABASE_URL.'

function ForbiddenState() {
  return (
    <div className="px-4 py-10 text-center">
      <p className="text-[13px] font-semibold text-slate-300">Admin only</p>
      <p className="mt-1 text-[12px] text-slate-500">
        You need administrator access to manage clusters.
      </p>
    </div>
  )
}

// Assemble the copy-pasteable `helm install` command for onboarding an agent.
// Omits --version when the chart version pin is empty (chart "latest").
function buildHelmCommand(
  defaults: EnrollmentDefaults,
  cluster: string,
  gateway: string,
  token: string,
): string {
  const chartRef = defaults.chart_version
    ? `${defaults.chart} --version ${defaults.chart_version}`
    : defaults.chart
  return [
    `helm install lotsman-agent ${chartRef} \\`,
    `  -n ${defaults.namespace} --create-namespace \\`,
    `  --set config.cluster=${cluster} \\`,
    `  --set config.controlPlaneAddr=${gateway} \\`,
    `  --set agentToken.value=${token}`,
  ].join('\n')
}

// Resolved display status for a cluster row: connected vs offline.
function clusterStatus(c: Cluster): { label: string; cls: string } {
  return c.connected
    ? { label: 'connected', cls: 'bg-emerald-500/10 text-emerald-400 ring-1 ring-emerald-500/20' }
    : { label: 'offline', cls: 'bg-slate-500/10 text-slate-400 ring-1 ring-slate-700/40' }
}

// Resolved display status for a token row: revoked > expired > active.
function tokenStatus(t: EnrollmentToken): { label: string; cls: string; revoked: boolean } {
  if (t.revoked) {
    return { label: 'revoked', cls: 'bg-slate-500/10 text-slate-500 ring-1 ring-slate-700/40', revoked: true }
  }
  if (t.expires_at && new Date(t.expires_at).getTime() <= Date.now()) {
    return { label: 'expired', cls: 'bg-amber-500/10 text-amber-400 ring-1 ring-amber-500/20', revoked: false }
  }
  return { label: 'active', cls: 'bg-emerald-500/10 text-emerald-400 ring-1 ring-emerald-500/20', revoked: false }
}

function ClustersTable({ clusters }: { clusters: Cluster[] }) {
  return (
    <table className={denseTableCls}>
      <thead>
        <tr className={denseThRowCls}>
          <th className={denseThCls}>Name</th>
          <th className={denseThCls}>Status</th>
          <th className={denseThCls}>Mode</th>
          <th className={denseThCls}>Agent version</th>
          <th className={denseThCls}>Env</th>
          <th className={denseThCls}>Region</th>
        </tr>
      </thead>
      <tbody>
        {clusters.map((c) => {
          const status = clusterStatus(c)
          return (
            <tr key={c.name} className={`${denseRowCls} ${c.connected ? 'bg-emerald-500/[0.02]' : ''}`}>
              <td
                className={`${denseTdCls} font-tech text-[12px] ${
                  c.connected ? 'text-slate-100' : 'text-slate-300'
                }`}
              >
                {c.name}
              </td>
              <td className={denseTdCls}>
                <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${status.cls}`}>
                  {status.label}
                </span>
              </td>
              <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>{c.mode || '—'}</td>
              <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>
                {c.agent_version || '—'}
              </td>
              <td className={denseTdCls}>
                {c.env ? (
                  <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${envBadgeStyle(c.env)}`}>
                    {c.env}
                  </span>
                ) : (
                  <span className="text-[12px] text-slate-600">—</span>
                )}
              </td>
              <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>{c.region || '—'}</td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}

// One-time reveal of a freshly enrolled agent: the assembled Helm command (token
// baked in) plus the raw token. Held in local component state only; once
// dismissed the plaintext is gone and can never be retrieved again.
function RevealPanel({
  created,
  command,
  onDismiss,
}: {
  created: EnrollmentTokenCreated
  command: string
  onDismiss: () => void
}) {
  return (
    <div className="space-y-3 rounded-2xl border border-amber-500/30 bg-amber-500/[0.04] p-4 shadow-card">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-[13px] font-semibold tracking-tight text-amber-300">
          Enroll <span className="font-tech text-amber-200">{created.cluster}</span>
        </h2>
        <button type="button" onClick={onDismiss} className={toolbarBtnCls}>
          Done
        </button>
      </div>

      <p className="text-[12px] text-amber-200/90">
        Run this command <span className="font-semibold">in the target cluster</span> to install the
        agent. The token is shown <span className="font-semibold">only once</span> and cannot be
        retrieved again — copy it now, then dismiss this panel once it is stored safely.
      </p>

      <div className="space-y-1.5">
        <div className="flex items-center justify-between gap-2">
          <span className="text-[11px] uppercase tracking-wider text-slate-500">Helm install</span>
          <CopyButton value={command} label="helm command" />
        </div>
        <pre className="overflow-x-auto rounded-md border border-slate-700 bg-[var(--surface-2)] p-3 font-tech text-[12px] leading-relaxed text-slate-200">
          {command}
        </pre>
      </div>

      <div className="space-y-1.5">
        <span className="text-[11px] uppercase tracking-wider text-slate-500">
          Token (for non-Helm onboarding)
        </span>
        <div className="flex items-center gap-2">
          <input
            type="text"
            readOnly
            aria-label="Enrollment token"
            value={created.token}
            onFocus={(e) => e.currentTarget.select()}
            className={`${toolbarInputCls} h-9 w-full flex-1 font-tech text-[12px]`}
          />
          <CopyButton value={created.token} label="enrollment token" />
        </div>
      </div>
    </div>
  )
}

function EnrollPanel({
  defaults,
  knownClusters,
  onEnrolled,
}: {
  defaults: EnrollmentDefaults
  knownClusters: string[]
  onEnrolled: (created: EnrollmentTokenCreated, command: string) => void
}) {
  const [cluster, setCluster] = useState('')
  const [ttlHours, setTtlHours] = useState(0)
  const [gateway, setGateway] = useState(defaults.gateway_addr || GATEWAY_PLACEHOLDER)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const disabled = !defaults.durable

  // Trimmed name and its validity drive both the inline hint and whether the
  // generate button is enabled, guarding the assembled command from a name that
  // would silently break `helm --set`.
  const trimmedName = cluster.trim()
  const nameValid = CLUSTER_NAME_RE.test(trimmedName)

  const enroll = useCallback(() => {
    const name = cluster.trim()
    const gw = gateway.trim()
    if (!CLUSTER_NAME_RE.test(name) || disabled) return
    setSubmitting(true)
    setError(null)
    createEnrollmentToken(name, ttlHours)
      .then((created) => {
        onEnrolled(created, buildHelmCommand(defaults, name, gw, created.token))
        setCluster('')
        setTtlHours(0)
      })
      .catch((e) => {
        // The control plane returns 503 when it has no durable store to persist
        // the minted token; surface the actionable message rather than a raw error.
        if (e instanceof ApiError && e.status === 503) {
          setError(NO_DURABLE_MSG)
        } else {
          setError(e instanceof Error ? e.message : String(e))
        }
      })
      .finally(() => setSubmitting(false))
  }, [cluster, gateway, ttlHours, defaults, disabled, onEnrolled])

  return (
    <section className="space-y-3">
      <div>
        <h2 className="text-[13px] font-semibold tracking-tight text-slate-300">Enroll a cluster</h2>
        <p className="mt-1 text-[12px] text-slate-500">
          Mint an onboarding token and generate a ready-to-run{' '}
          <span className="font-tech text-slate-400">helm install</span> for the lotsman-agent.
          Clusters are enrolled before they appear in the list, so any name is allowed — known
          clusters are offered as hints.
        </p>
      </div>

      {disabled && (
        <div
          role="alert"
          className="rounded-lg border border-amber-500/30 bg-amber-500/5 px-4 py-3 text-[12px] text-amber-300"
        >
          {NO_DURABLE_MSG}
        </div>
      )}

      <form
        className="flex flex-wrap items-end gap-3"
        onSubmit={(e) => {
          e.preventDefault()
          enroll()
        }}
      >
        <label className="flex flex-col gap-1">
          <span className="text-[11px] uppercase tracking-wider text-slate-600">Cluster</span>
          <input
            type="text"
            aria-label="Cluster name"
            value={cluster}
            onChange={(e) => setCluster(e.target.value)}
            placeholder="cluster name…"
            list="clusters-known"
            disabled={disabled}
            className={`${toolbarInputCls} w-56 disabled:opacity-50`}
          />
          <datalist id="clusters-known">
            {knownClusters.map((c) => (
              <option key={c} value={c} />
            ))}
          </datalist>
          {trimmedName !== '' && !nameValid && (
            <span className="text-[11px] text-amber-400">{CLUSTER_NAME_HINT}</span>
          )}
        </label>

        <label className="flex flex-col gap-1">
          <span className="text-[11px] uppercase tracking-wider text-slate-600">Expires</span>
          <select
            aria-label="Token expiry"
            value={ttlHours}
            onChange={(e) => setTtlHours(Number(e.target.value))}
            disabled={disabled}
            className={`${toolbarInputCls} w-40 disabled:opacity-50`}
          >
            {TTL_OPTIONS.map((o) => (
              <option key={o.hours} value={o.hours}>
                {o.label}
              </option>
            ))}
          </select>
        </label>

        <label className="flex flex-col gap-1">
          <span className="text-[11px] uppercase tracking-wider text-slate-600">Gateway address</span>
          <input
            type="text"
            aria-label="Control-plane gateway address"
            value={gateway}
            onChange={(e) => setGateway(e.target.value)}
            placeholder={GATEWAY_PLACEHOLDER}
            disabled={disabled}
            className={`${toolbarInputCls} w-72 font-tech text-[12px] disabled:opacity-50`}
          />
        </label>

        <button
          type="submit"
          disabled={disabled || submitting || !nameValid}
          className={toolbarBtnCls}
        >
          {submitting ? 'Generating…' : 'Generate enroll command'}
        </button>
      </form>

      {error && <ErrorState label="Failed to enroll cluster" error={error} />}
    </section>
  )
}

function TokensTable({
  tokens,
  revokingId,
  onRevoke,
}: {
  tokens: EnrollmentToken[]
  revokingId: string | null
  onRevoke: (t: EnrollmentToken) => void
}) {
  return (
    <table className={denseTableCls}>
      <thead>
        <tr className={denseThRowCls}>
          <th className={denseThCls}>Cluster</th>
          <th className={denseThCls}>Created</th>
          <th className={denseThCls}>Expires</th>
          <th className={denseThCls}>Status</th>
          <th className={denseThCls}>
            <span className="sr-only">Actions</span>
          </th>
        </tr>
      </thead>
      <tbody>
        {tokens.map((t) => {
          const status = tokenStatus(t)
          return (
            <tr key={t.id} className={`${denseRowCls} ${status.revoked ? 'opacity-50' : ''}`}>
              <td
                className={`${denseTdCls} font-tech text-[12px] ${
                  status.revoked ? 'text-slate-500 line-through' : 'text-slate-200'
                }`}
              >
                {t.cluster}
              </td>
              <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>
                {formatDate(t.created_at)}
              </td>
              <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>
                {t.expires_at ? formatDate(t.expires_at) : 'never'}
              </td>
              <td className={denseTdCls}>
                <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${status.cls}`}>
                  {status.label}
                </span>
              </td>
              <td className={`${denseTdCls} text-right`}>
                {!t.revoked && (
                  <button
                    type="button"
                    onClick={() => onRevoke(t)}
                    disabled={revokingId === t.id}
                    className={`inline-flex h-7 items-center rounded-md border border-red-500/30 bg-red-500/5 px-2 text-[12px] text-red-300 transition-colors hover:bg-red-500/10 disabled:opacity-50 ${focusRingCls}`}
                  >
                    {revokingId === t.id ? 'Revoking…' : 'Revoke'}
                  </button>
                )}
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}

interface Revealed {
  created: EnrollmentTokenCreated
  command: string
}

export default function ClustersAdminPage() {
  const { isAdmin, loading: authLoading } = useAuth()
  const { clusters } = useCluster()
  const [clusterList, setClusterList] = useState<Cluster[]>([])
  const [defaults, setDefaults] = useState<EnrollmentDefaults | null>(null)
  const [tokens, setTokens] = useState<EnrollmentToken[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  // Non-fatal action failures (e.g. a failed revoke) are kept separate from the
  // initial-load `error` so they surface inline without hiding the token table.
  const [actionError, setActionError] = useState<string | null>(null)
  const [forbidden, setForbidden] = useState(false)
  const [revealed, setRevealed] = useState<Revealed | null>(null)
  const [revokingId, setRevokingId] = useState<string | null>(null)
  const [nonce, setNonce] = useState(0)

  // Datalist hints: union of the cross-view cluster context and this view's list.
  const knownClusters = useMemo(() => {
    const names = new Set<string>()
    clusters.forEach((c) => names.add(c.name))
    clusterList.forEach((c) => names.add(c.name))
    return Array.from(names)
  }, [clusters, clusterList])

  useEffect(() => {
    // Wait for auth to settle, and skip the requests entirely for non-admins.
    if (authLoading) return
    if (!isAdmin) {
      setLoading(false)
      return
    }
    let cancelled = false
    setLoading(true)
    setError(null)
    setForbidden(false)
    Promise.all([listEnrollmentTokens(), getEnrollmentDefaults()])
      .then(([ts, d]) => {
        if (cancelled) return
        setTokens(ts)
        setDefaults(d)
      })
      .catch((e) => {
        if (cancelled) return
        if (e instanceof ApiError && e.status === 403) {
          setForbidden(true)
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
  }, [authLoading, isAdmin, nonce])

  // The cluster list comes from the shared cluster context; mirror it locally so
  // the table reflects context refreshes without an extra request.
  useEffect(() => {
    setClusterList(clusters)
  }, [clusters])

  const refetch = useCallback(() => setNonce((n) => n + 1), [])

  const handleEnrolled = useCallback(
    (created: EnrollmentTokenCreated, command: string) => {
      setRevealed({ created, command })
      refetch()
    },
    [refetch],
  )

  const handleRevoke = useCallback(
    (t: EnrollmentToken) => {
      if (
        typeof window !== 'undefined' &&
        !window.confirm(
          `Revoke the enrollment token for "${t.cluster}"? The agent using it will no longer be able to enroll.`,
        )
      ) {
        return
      }
      setRevokingId(t.id)
      setActionError(null)
      revokeEnrollmentToken(t.id)
        .then(() => refetch())
        .catch((e) => setActionError(e instanceof Error ? e.message : String(e)))
        .finally(() => setRevokingId(null))
    },
    [refetch],
  )

  return (
    <>
      {/* Header chrome matching the resource toolbar (this view isn't cluster-scoped). */}
      <div className="flex flex-wrap items-center gap-3 border-b border-slate-800 bg-[var(--surface)] px-4 py-2.5">
        <div className="flex min-w-0 items-baseline gap-2">
          <h1 className="truncate text-sm font-semibold tracking-tight text-slate-100">Clusters</h1>
          <span className="font-tech text-[11px] text-slate-600">fleet &amp; enrollment · admin</span>
        </div>
      </div>

      <div className="flex-1 overflow-auto p-5">
        {authLoading || (loading && !forbidden && defaults === null) ? (
          <LoadingState label="Loading clusters…" />
        ) : !isAdmin || forbidden ? (
          <ForbiddenState />
        ) : (
          <div className="space-y-8">
            {/* 1. Clusters list */}
            <section className="space-y-3">
              <h2 className="text-[13px] font-semibold tracking-tight text-slate-300">Clusters</h2>
              {clusterList.length === 0 ? (
                <EmptyState label="No clusters yet — enroll one below." />
              ) : (
                <ClustersTable clusters={clusterList} />
              )}
            </section>

            {/* 2. Enroll a cluster */}
            {revealed && (
              <RevealPanel
                created={revealed.created}
                command={revealed.command}
                onDismiss={() => setRevealed(null)}
              />
            )}
            {defaults && (
              <EnrollPanel
                defaults={defaults}
                knownClusters={knownClusters}
                onEnrolled={handleEnrolled}
              />
            )}

            {/* 3. Enrollment tokens */}
            <section className="space-y-3">
              <h2 className="text-[13px] font-semibold tracking-tight text-slate-300">
                Enrollment tokens
              </h2>
              {actionError && (
                <div
                  role="alert"
                  className="flex items-start justify-between gap-3 rounded-lg border border-red-500/30 bg-red-500/5 px-4 py-3 text-[13px] text-red-300"
                >
                  <span>Failed to revoke token: {actionError}</span>
                  <button
                    type="button"
                    onClick={() => setActionError(null)}
                    className={`shrink-0 text-[12px] text-red-300/80 hover:text-red-200 ${focusRingCls}`}
                  >
                    Dismiss
                  </button>
                </div>
              )}
              {error ? (
                <ErrorState label="Failed to load enrollment data" error={error} />
              ) : tokens.length === 0 ? (
                <EmptyState label="No enrollment tokens yet. Enroll a cluster above to mint one." />
              ) : (
                <TokensTable tokens={tokens} revokingId={revokingId} onRevoke={handleRevoke} />
              )}
            </section>
          </div>
        )}
      </div>
    </>
  )
}
