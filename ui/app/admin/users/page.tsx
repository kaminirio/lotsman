'use client'

// Admin Users view: manage first-party accounts and inspect SSO-provisioned ones.
// Lists users, creates username/password accounts, and per-row toggles admin /
// active, resets passwords, and deletes. Admin-gated like the RBAC / Clusters
// views (401 unauthenticated, 403 non-admin). The control plane enforces
// last-admin / self-lockout guards and returns 409 with an actionable message,
// which we surface inline.

import { useCallback, useEffect, useState } from 'react'
import {
  ApiError,
  createUser,
  deleteUser,
  listUsers,
  updateUser,
  type LotsmanUser,
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
  formatDate,
  focusRingCls,
} from '@/lib/styles'

function ForbiddenState() {
  return (
    <div className="px-4 py-10 text-center">
      <p className="text-[13px] font-semibold text-slate-300">Admin only</p>
      <p className="mt-1 text-[12px] text-slate-500">
        You need administrator access to manage users.
      </p>
    </div>
  )
}

const adminBadgeCls = 'bg-indigo-500/15 text-indigo-300 ring-1 ring-indigo-500/30'
const activeBadgeCls = 'bg-emerald-500/10 text-emerald-400 ring-1 ring-emerald-500/20'
const disabledBadgeCls = 'bg-slate-500/10 text-slate-500 ring-1 ring-slate-700/40'

// Small row-action button; `danger` tints it red for destructive actions.
function RowButton({
  onClick,
  disabled,
  danger,
  children,
}: {
  onClick: () => void
  disabled?: boolean
  danger?: boolean
  children: React.ReactNode
}) {
  const base =
    'inline-flex h-7 items-center rounded-md border px-2 text-[12px] transition-colors disabled:opacity-50'
  const tint = danger
    ? 'border-red-500/30 bg-red-500/5 text-red-300 hover:bg-red-500/10'
    : 'border-slate-700 bg-[var(--surface-2)] text-slate-300 hover:bg-[var(--surface-hover)] hover:text-slate-100'
  return (
    <button type="button" onClick={onClick} disabled={disabled} className={`${base} ${tint} ${focusRingCls}`}>
      {children}
    </button>
  )
}

function CreateUserForm({ onCreated }: { onCreated: () => void }) {
  const [username, setUsername] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [isAdmin, setIsAdmin] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const create = useCallback(() => {
    const u = username.trim()
    const e = email.trim()
    if (u === '' || password === '') return
    setSubmitting(true)
    setError(null)
    createUser({ username: u, email: e, password, is_admin: isAdmin })
      .then(() => {
        setUsername('')
        setEmail('')
        setPassword('')
        setIsAdmin(false)
        onCreated()
      })
      .catch((err) => {
        // 409 = duplicate username; surface the control plane's message verbatim.
        setError(err instanceof Error ? err.message : String(err))
      })
      .finally(() => setSubmitting(false))
  }, [username, email, password, isAdmin, onCreated])

  return (
    <section className="space-y-3">
      <div>
        <h2 className="text-[13px] font-semibold tracking-tight text-slate-300">Add a user</h2>
        <p className="mt-1 text-[12px] text-slate-500">
          Create a first-party account with a username and password. SSO users are provisioned
          automatically on first sign-in.
        </p>
      </div>

      <form
        className="flex flex-wrap items-end gap-3"
        onSubmit={(e) => {
          e.preventDefault()
          create()
        }}
      >
        <label className="flex flex-col gap-1">
          <span className="text-[11px] uppercase tracking-wider text-slate-600">Username</span>
          <input
            type="text"
            aria-label="Username"
            autoComplete="off"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            placeholder="username…"
            className={`${toolbarInputCls} w-48`}
          />
        </label>

        <label className="flex flex-col gap-1">
          <span className="text-[11px] uppercase tracking-wider text-slate-600">Email</span>
          <input
            type="email"
            aria-label="Email"
            autoComplete="off"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="email…"
            className={`${toolbarInputCls} w-56`}
          />
        </label>

        <label className="flex flex-col gap-1">
          <span className="text-[11px] uppercase tracking-wider text-slate-600">Password</span>
          <input
            type="password"
            aria-label="Password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="password…"
            className={`${toolbarInputCls} w-48`}
          />
        </label>

        <label className="flex h-8 items-center gap-2 text-[13px] text-slate-300">
          <input
            type="checkbox"
            checked={isAdmin}
            onChange={(e) => setIsAdmin(e.target.checked)}
            className={`h-4 w-4 rounded border-slate-700 bg-[var(--surface-2)] text-indigo-500 ${focusRingCls}`}
          />
          Admin
        </label>

        <button
          type="submit"
          disabled={submitting || username.trim() === '' || password === ''}
          className={toolbarBtnCls}
        >
          {submitting ? 'Creating…' : 'Create user'}
        </button>
      </form>

      {error && <ErrorState label="Failed to create user" error={error} />}
    </section>
  )
}

function UsersTable({
  users,
  busyId,
  onToggleAdmin,
  onToggleActive,
  onResetPassword,
  onDelete,
}: {
  users: LotsmanUser[]
  busyId: string | null
  onToggleAdmin: (u: LotsmanUser) => void
  onToggleActive: (u: LotsmanUser) => void
  onResetPassword: (u: LotsmanUser) => void
  onDelete: (u: LotsmanUser) => void
}) {
  return (
    <table className={denseTableCls}>
      <thead>
        <tr className={denseThRowCls}>
          <th className={denseThCls}>Username</th>
          <th className={denseThCls}>Email</th>
          <th className={denseThCls}>Role</th>
          <th className={denseThCls}>Status</th>
          <th className={denseThCls}>SSO</th>
          <th className={denseThCls}>Created</th>
          <th className={denseThCls}>
            <span className="sr-only">Actions</span>
          </th>
        </tr>
      </thead>
      <tbody>
        {users.map((u) => {
          const busy = busyId === u.id
          return (
            <tr key={u.id} className={`${denseRowCls} ${u.active ? '' : 'opacity-60'}`}>
              <td className={`${denseTdCls} font-tech text-[12px] text-slate-100`}>{u.username}</td>
              <td className={`${denseTdCls} text-[12px] text-slate-400`}>{u.email || '—'}</td>
              <td className={denseTdCls}>
                {u.is_admin ? (
                  <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${adminBadgeCls}`}>
                    admin
                  </span>
                ) : (
                  <span className="text-[12px] text-slate-500">member</span>
                )}
              </td>
              <td className={denseTdCls}>
                <span
                  className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${
                    u.active ? activeBadgeCls : disabledBadgeCls
                  }`}
                >
                  {u.active ? 'active' : 'disabled'}
                </span>
              </td>
              <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>
                {u.sso_provider || '—'}
              </td>
              <td className={`${denseTdCls} font-tech text-[12px] text-slate-400`}>
                {formatDate(u.created_at)}
              </td>
              <td className={`${denseTdCls} text-right`}>
                <div className="flex justify-end gap-1.5">
                  <RowButton onClick={() => onToggleAdmin(u)} disabled={busy}>
                    {u.is_admin ? 'Revoke admin' : 'Make admin'}
                  </RowButton>
                  <RowButton onClick={() => onToggleActive(u)} disabled={busy}>
                    {u.active ? 'Disable' : 'Enable'}
                  </RowButton>
                  {/* Password reset only applies to first-party accounts. */}
                  {u.sso_provider === '' && (
                    <RowButton onClick={() => onResetPassword(u)} disabled={busy}>
                      Reset password
                    </RowButton>
                  )}
                  <RowButton onClick={() => onDelete(u)} disabled={busy} danger>
                    Delete
                  </RowButton>
                </div>
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}

export default function UsersAdminPage() {
  const { isAdmin, loading: authLoading } = useAuth()
  const [users, setUsers] = useState<LotsmanUser[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  // Non-fatal action failures (toggle/reset/delete, incl. the 409 last-admin
  // guard) surface inline without hiding the users table.
  const [actionError, setActionError] = useState<string | null>(null)
  const [forbidden, setForbidden] = useState(false)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [nonce, setNonce] = useState(0)

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
    listUsers()
      .then((us) => {
        if (!cancelled) setUsers(us)
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

  const refetch = useCallback(() => setNonce((n) => n + 1), [])

  // Shared mutation runner: marks the row busy, clears prior action errors, and
  // on failure surfaces the server message (including the 409 guard copy).
  const runMutation = useCallback(
    (id: string, op: () => Promise<unknown>) => {
      setBusyId(id)
      setActionError(null)
      op()
        .then(() => refetch())
        .catch((e) => setActionError(e instanceof Error ? e.message : String(e)))
        .finally(() => setBusyId(null))
    },
    [refetch],
  )

  const handleToggleAdmin = useCallback(
    (u: LotsmanUser) => runMutation(u.id, () => updateUser(u.id, { is_admin: !u.is_admin })),
    [runMutation],
  )

  const handleToggleActive = useCallback(
    (u: LotsmanUser) => runMutation(u.id, () => updateUser(u.id, { active: !u.active })),
    [runMutation],
  )

  const handleResetPassword = useCallback(
    (u: LotsmanUser) => {
      if (typeof window === 'undefined') return
      const pw = window.prompt(`Set a new password for "${u.username}":`)
      if (pw === null) return // cancelled
      if (pw === '') {
        setActionError('Password cannot be empty.')
        return
      }
      runMutation(u.id, () => updateUser(u.id, { password: pw }))
    },
    [runMutation],
  )

  const handleDelete = useCallback(
    (u: LotsmanUser) => {
      if (
        typeof window !== 'undefined' &&
        !window.confirm(`Delete user "${u.username}"? This cannot be undone.`)
      ) {
        return
      }
      runMutation(u.id, () => deleteUser(u.id))
    },
    [runMutation],
  )

  return (
    <>
      {/* Header chrome matching the resource toolbar (this view isn't cluster-scoped). */}
      <div className="flex flex-wrap items-center gap-3 border-b border-slate-800 bg-[var(--surface)] px-4 py-2.5">
        <div className="flex min-w-0 items-baseline gap-2">
          <h1 className="truncate text-sm font-semibold tracking-tight text-slate-100">Users</h1>
          <span className="font-tech text-[11px] text-slate-600">accounts &amp; access · admin</span>
        </div>
      </div>

      <div className="flex-1 overflow-auto p-5">
        {authLoading || (loading && !forbidden) ? (
          <LoadingState label="Loading users…" />
        ) : !isAdmin || forbidden ? (
          <ForbiddenState />
        ) : (
          <div className="space-y-8">
            {/* 1. Users list */}
            <section className="space-y-3">
              <h2 className="text-[13px] font-semibold tracking-tight text-slate-300">Users</h2>
              {actionError && (
                <div
                  role="alert"
                  className="flex items-start justify-between gap-3 rounded-lg border border-red-500/30 bg-red-500/5 px-4 py-3 text-[13px] text-red-300"
                >
                  <span>{actionError}</span>
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
                <ErrorState label="Failed to load users" error={error} />
              ) : users.length === 0 ? (
                <EmptyState label="No users yet — add one below." />
              ) : (
                <UsersTable
                  users={users}
                  busyId={busyId}
                  onToggleAdmin={handleToggleAdmin}
                  onToggleActive={handleToggleActive}
                  onResetPassword={handleResetPassword}
                  onDelete={handleDelete}
                />
              )}
            </section>

            {/* 2. Add a user */}
            <CreateUserForm onCreated={refetch} />
          </div>
        )}
      </div>
    </>
  )
}
