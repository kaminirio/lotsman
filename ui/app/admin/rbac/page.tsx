'use client'

// Access / strong-RBAC admin view (read-only). Bindings are configured at deploy
// time via LOTSMAN_SSO_CONFIG, so there are no create/edit controls — this page
// inspects the live config and resolves effective permissions for a given user.

import { useCallback, useEffect, useState } from 'react'
import {
  ApiError,
  getRbacConfig,
  getRbacEffective,
  type RbacConfig,
  type RbacEffective,
} from '@/lib/api'
import { useAuth } from '@/lib/auth-context'
import { LoadingState, ErrorState, EmptyState } from '@/components/view-states'
import {
  denseTableCls,
  denseThRowCls,
  denseThCls,
  denseTdCls,
  denseRowCls,
  toolbarInputCls,
  toolbarBtnCls,
} from '@/lib/styles'

// "all" sentinel for an empty cluster/namespace scope.
function scopeLabel(value: string): string {
  return value.trim() === '' ? 'all' : value
}

function ForbiddenState() {
  return (
    <div className="px-4 py-10 text-center">
      <p className="text-[13px] font-semibold text-slate-300">Admin only</p>
      <p className="mt-1 text-[12px] text-slate-500">
        You need administrator access to view RBAC configuration.
      </p>
    </div>
  )
}

function RoleBindingsSection({ config }: { config: RbacConfig }) {
  return (
    <section className="space-y-4">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-[13px] font-semibold tracking-tight text-slate-300">Role bindings</h2>
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-[11px] uppercase tracking-wider text-slate-600">Roles</span>
          {config.roles.length === 0 ? (
            <span className="text-[12px] text-slate-500">none</span>
          ) : (
            config.roles.map((r) => (
              <span
                key={r}
                className="rounded-md border border-slate-700 bg-[var(--surface-2)] px-1.5 py-0.5 font-tech text-[11px] text-slate-300"
              >
                {r}
              </span>
            ))
          )}
        </div>
      </div>

      <div className="space-y-2">
        <h3 className="text-[11px] font-semibold uppercase tracking-wider text-slate-500">
          User bindings
        </h3>
        {config.bindings.length === 0 ? (
          <EmptyState label="No user bindings configured." />
        ) : (
          <table className={denseTableCls}>
            <thead>
              <tr className={denseThRowCls}>
                <th className={denseThCls}>Subject</th>
                <th className={denseThCls}>Role</th>
                <th className={denseThCls}>Cluster</th>
                <th className={denseThCls}>Namespace</th>
              </tr>
            </thead>
            <tbody>
              {config.bindings.map((b, i) => (
                <tr key={`${b.subject}-${b.role}-${b.cluster}-${b.namespace}-${i}`} className={denseRowCls}>
                  <td className={`${denseTdCls} font-tech text-[12px] text-slate-200`}>{b.subject}</td>
                  <td className={denseTdCls}>
                    <span className="font-tech text-[12px] text-indigo-300">{b.role}</span>
                  </td>
                  <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>
                    {scopeLabel(b.cluster)}
                  </td>
                  <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>
                    {scopeLabel(b.namespace)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="space-y-2">
        <h3 className="text-[11px] font-semibold uppercase tracking-wider text-slate-500">
          Group bindings
        </h3>
        {config.group_bindings.length === 0 ? (
          <EmptyState label="No group bindings configured." />
        ) : (
          <table className={denseTableCls}>
            <thead>
              <tr className={denseThRowCls}>
                <th className={denseThCls}>Group</th>
                <th className={denseThCls}>Role</th>
                <th className={denseThCls}>Cluster</th>
                <th className={denseThCls}>Namespace</th>
              </tr>
            </thead>
            <tbody>
              {config.group_bindings.map((b, i) => (
                <tr key={`${b.group}-${b.role}-${b.cluster}-${b.namespace}-${i}`} className={denseRowCls}>
                  <td className={`${denseTdCls} font-tech text-[12px] text-slate-200`}>{b.group}</td>
                  <td className={denseTdCls}>
                    <span className="font-tech text-[12px] text-indigo-300">{b.role}</span>
                  </td>
                  <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>
                    {scopeLabel(b.cluster)}
                  </td>
                  <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>
                    {scopeLabel(b.namespace)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </section>
  )
}

function EffectivePermissionsSection() {
  const [input, setInput] = useState('')
  const [result, setResult] = useState<RbacEffective | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // The login whose result is currently shown, for the "no permissions" copy.
  const [queried, setQueried] = useState<string | null>(null)

  const lookup = useCallback(() => {
    const user = input.trim()
    if (!user) return
    setLoading(true)
    setError(null)
    setResult(null)
    getRbacEffective(user)
      .then((r) => {
        setResult(r)
        setQueried(user)
      })
      .catch((e) => {
        setError(e instanceof Error ? e.message : String(e))
        setQueried(user)
      })
      .finally(() => setLoading(false))
  }, [input])

  return (
    <section className="space-y-3">
      <div>
        <h2 className="text-[13px] font-semibold tracking-tight text-slate-300">Effective permissions</h2>
        <p className="mt-1 text-[12px] text-slate-500">
          Resolve the bindings a GitHub user receives (directly and via groups). Deny-by-default:
          a user with no matching bindings has no access.
        </p>
      </div>

      <form
        className="flex flex-wrap items-center gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          lookup()
        }}
      >
        <input
          type="text"
          aria-label="GitHub login"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="GitHub login…"
          className={`${toolbarInputCls} w-56`}
        />
        <button type="submit" disabled={loading || input.trim() === ''} className={toolbarBtnCls}>
          {loading ? 'Looking up…' : 'Look up'}
        </button>
      </form>

      {loading ? (
        <LoadingState label="Resolving permissions…" />
      ) : error ? (
        <ErrorState label="Failed to resolve permissions" error={error} />
      ) : result ? (
        <div className="space-y-3 rounded-2xl border border-slate-800 bg-[var(--surface)] p-4 shadow-card">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-tech text-[13px] text-slate-200">{result.user}</span>
            {result.is_admin && (
              <span className="rounded px-1.5 py-0.5 text-[11px] font-medium bg-indigo-500/15 text-indigo-300 ring-1 ring-indigo-500/30">
                admin
              </span>
            )}
          </div>

          {result.bindings.length === 0 ? (
            <EmptyState
              label={
                result.is_admin
                  ? `${result.user} is an admin (full access); no scoped role bindings.`
                  : `No permissions — ${queried ?? result.user} has no matching bindings (deny by default).`
              }
            />
          ) : (
            <table className={denseTableCls}>
              <thead>
                <tr className={denseThRowCls}>
                  <th className={denseThCls}>Role</th>
                  <th className={denseThCls}>Cluster</th>
                  <th className={denseThCls}>Namespace</th>
                </tr>
              </thead>
              <tbody>
                {result.bindings.map((b, i) => (
                  <tr key={`${b.role}-${b.cluster}-${b.namespace}-${i}`} className={denseRowCls}>
                    <td className={denseTdCls}>
                      <span className="font-tech text-[12px] text-indigo-300">{b.role}</span>
                    </td>
                    <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>
                      {scopeLabel(b.cluster)}
                    </td>
                    <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>
                      {scopeLabel(b.namespace)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      ) : null}
    </section>
  )
}

export default function RbacAdminPage() {
  const { isAdmin, loading: authLoading } = useAuth()
  const [config, setConfig] = useState<RbacConfig | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [forbidden, setForbidden] = useState(false)

  useEffect(() => {
    // Wait for auth to settle, and skip the request entirely for non-admins.
    if (authLoading) return
    if (!isAdmin) {
      setLoading(false)
      return
    }
    let cancelled = false
    setLoading(true)
    setError(null)
    setForbidden(false)
    getRbacConfig()
      .then((c) => {
        if (!cancelled) setConfig(c)
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
  }, [authLoading, isAdmin])

  return (
    <>
      {/* Header chrome matching the resource toolbar (this view isn't cluster-scoped). */}
      <div className="flex flex-wrap items-center gap-3 border-b border-slate-800 bg-[var(--surface)] px-4 py-2.5">
        <div className="flex min-w-0 items-baseline gap-2">
          <h1 className="truncate text-sm font-semibold tracking-tight text-slate-100">Access</h1>
          <span className="font-tech text-[11px] text-slate-600">strong RBAC · read-only</span>
        </div>
      </div>

      <div className="flex-1 overflow-auto p-5">
        {authLoading || loading ? (
          <LoadingState label="Loading RBAC configuration…" />
        ) : !isAdmin || forbidden ? (
          <ForbiddenState />
        ) : error ? (
          <ErrorState label="Failed to load RBAC configuration" error={error} />
        ) : config ? (
          <div className="space-y-8">
            <p className="text-[12px] text-slate-500">
              Bindings are configured via <span className="font-tech text-slate-400">LOTSMAN_SSO_CONFIG</span>{' '}
              and applied at deploy time. This view is read-only — runtime changes are not supported.
            </p>
            <RoleBindingsSection config={config} />
            <EffectivePermissionsSection />
          </div>
        ) : null}
      </div>
    </>
  )
}
