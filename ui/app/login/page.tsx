'use client'

// Login page — the unauthenticated entry point. Renders a first-party
// username/password form (always shown, `local` is always true) plus a button
// per configured SSO provider. This route is deliberately NOT gated by the auth
// context: the shell renders it without console chrome, and the context only
// redirects AWAY from /login once a session exists, so there is no redirect loop.

import { Suspense, useCallback, useEffect, useState } from 'react'
import { useRouter, useSearchParams } from 'next/navigation'
import { ApiError } from '@/lib/api'
import { getProviders, login, type Providers, type SsoProvider } from '@/lib/auth'
import { focusRingCls } from '@/lib/styles'

// Human-readable copy for the `?error=` codes the backend appends when an SSO
// round-trip fails. Unknown codes fall back to a generic message.
const ERROR_MESSAGES: Record<string, string> = {
  no_account: 'No Lotsman account is linked to that identity. Ask an administrator to add you.',
  access_denied: 'Sign-in was cancelled or denied by the provider.',
  invalid_state: 'The sign-in request expired. Please try again.',
  sso_failed: 'Single sign-on failed. Please try again or use your username and password.',
}

function errorMessage(code: string | null): string | null {
  if (!code) return null
  return ERROR_MESSAGES[code] ?? 'Sign-in failed. Please try again.'
}

// SSO providers rendered in this order when enabled.
const SSO_PROVIDERS: { key: SsoProvider; label: string; icon: React.ReactNode }[] = [
  { key: 'github', label: 'GitHub', icon: <IconGitHub /> },
  { key: 'google', label: 'Google', icon: <IconGoogle /> },
  { key: 'azure', label: 'Microsoft', icon: <IconAzure /> },
]

function IconGitHub() {
  return (
    <svg className="h-4 w-4" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M12 2C6.48 2 2 6.58 2 12.25c0 4.53 2.87 8.37 6.84 9.73.5.1.68-.22.68-.49 0-.24-.01-.87-.01-1.71-2.78.62-3.37-1.37-3.37-1.37-.46-1.19-1.11-1.5-1.11-1.5-.91-.64.07-.62.07-.62 1 .07 1.53 1.06 1.53 1.06.89 1.57 2.34 1.12 2.91.85.09-.66.35-1.12.63-1.38-2.22-.26-4.56-1.14-4.56-5.06 0-1.12.39-2.03 1.03-2.75-.1-.26-.45-1.3.1-2.71 0 0 .84-.28 2.75 1.05a9.32 9.32 0 015 0c1.91-1.33 2.75-1.05 2.75-1.05.55 1.41.2 2.45.1 2.71.64.72 1.03 1.63 1.03 2.75 0 3.93-2.34 4.79-4.57 5.05.36.32.68.94.68 1.9 0 1.37-.01 2.48-.01 2.82 0 .27.18.6.69.49A10.02 10.02 0 0022 12.25C22 6.58 17.52 2 12 2z" />
    </svg>
  )
}

function IconGoogle() {
  return (
    <svg className="h-4 w-4" viewBox="0 0 24 24" aria-hidden="true">
      <path fill="#4285F4" d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92a5.06 5.06 0 01-2.2 3.32v2.76h3.56c2.08-1.92 3.28-4.74 3.28-8.09z" />
      <path fill="#34A853" d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.56-2.76c-.98.66-2.23 1.06-3.72 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84A11 11 0 0012 23z" />
      <path fill="#FBBC05" d="M5.84 14.11a6.6 6.6 0 010-4.22V7.05H2.18a11 11 0 000 9.9l3.66-2.84z" />
      <path fill="#EA4335" d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1a11 11 0 00-9.82 6.05l3.66 2.84C6.71 7.31 9.14 5.38 12 5.38z" />
    </svg>
  )
}

function IconAzure() {
  return (
    <svg className="h-4 w-4" viewBox="0 0 24 24" aria-hidden="true">
      <path fill="#F25022" d="M2 2h9.5v9.5H2z" />
      <path fill="#7FBA00" d="M12.5 2H22v9.5h-9.5z" />
      <path fill="#00A4EF" d="M2 12.5h9.5V22H2z" />
      <path fill="#FFB900" d="M12.5 12.5H22V22h-9.5z" />
    </svg>
  )
}

const fieldCls =
  'h-10 w-full rounded-md border border-slate-700 bg-[var(--surface-2)] px-3 text-[13px] text-slate-100 placeholder:text-slate-600 ' +
  focusRingCls

function LoginForm() {
  const router = useRouter()
  const params = useSearchParams()
  const next = params.get('next')
  const urlError = errorMessage(params.get('error'))

  const [providers, setProviders] = useState<Providers | null>(null)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    getProviders()
      .then((p) => {
        if (!cancelled) setProviders(p)
      })
      .catch(() => {
        // Providers endpoint unreachable: the username/password form is always
        // available, so fall back to local-only rather than blocking sign-in.
        if (!cancelled) setProviders({ local: true, github: false, google: false, azure: false })
      })
    return () => {
      cancelled = true
    }
  }, [])

  const submit = useCallback(
    (e: React.FormEvent) => {
      e.preventDefault()
      if (submitting) return
      setSubmitting(true)
      setError(null)
      login(username.trim(), password)
        .then(() => {
          // Full navigation so the auth context re-fetches /auth/me with the new
          // session cookie. Only honor a same-origin relative `next` target.
          const dest = next && next.startsWith('/') && !next.startsWith('//') ? next : '/'
          router.replace(dest)
        })
        .catch((err) => {
          setError(
            err instanceof ApiError && err.status === 401
              ? 'Invalid username or password.'
              : err instanceof Error
                ? err.message
                : 'Sign-in failed. Please try again.',
          )
          setSubmitting(false)
        })
    },
    [username, password, next, submitting, router],
  )

  const ssoButtons = SSO_PROVIDERS.filter((p) => providers?.[p.key])

  return (
    <div className="flex h-full w-full items-center justify-center px-4">
      <div className="w-full max-w-sm animate-fade-up">
        {/* Wordmark */}
        <div className="mb-8 flex items-center justify-center gap-2.5">
          <div className="flex h-7 w-7 items-center justify-center rounded-md bg-[var(--accent-soft)] ring-1 ring-indigo-500/20 shadow-[0_0_16px_-4px_var(--accent-glow)]">
            <span className="block h-3 w-3 rounded-sm bg-[var(--accent)]" />
          </div>
          <span className="text-lg font-bold tracking-tight text-slate-100">Lotsman</span>
        </div>

        <div className="rounded-2xl border border-slate-800 bg-[var(--surface)] p-6 shadow-elevated">
          <h1 className="text-[15px] font-semibold tracking-tight text-slate-100">Sign in</h1>
          <p className="mt-1 text-[12px] text-slate-500">
            Enter your credentials to access the console.
          </p>

          {urlError && (
            <div
              role="alert"
              className="mt-4 rounded-lg border border-amber-500/30 bg-amber-500/5 px-3 py-2.5 text-[12px] text-amber-300"
            >
              {urlError}
            </div>
          )}

          <form className="mt-5 space-y-3" onSubmit={submit}>
            <label className="flex flex-col gap-1.5">
              <span className="text-[11px] uppercase tracking-wider text-slate-600">Username</span>
              <input
                type="text"
                autoComplete="username"
                autoFocus
                required
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="username"
                className={fieldCls}
              />
            </label>

            <label className="flex flex-col gap-1.5">
              <span className="text-[11px] uppercase tracking-wider text-slate-600">Password</span>
              <input
                type="password"
                autoComplete="current-password"
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
                className={fieldCls}
              />
            </label>

            {error && (
              <div role="alert" className="text-[12px] text-red-400">
                {error}
              </div>
            )}

            <button
              type="submit"
              disabled={submitting || username.trim() === '' || password === ''}
              className={`flex h-10 w-full items-center justify-center rounded-md bg-[var(--accent)] px-3 text-[13px] font-semibold text-white transition-colors hover:bg-[var(--accent-hover)] disabled:opacity-50 ${focusRingCls}`}
            >
              {submitting ? 'Signing in…' : 'Sign in'}
            </button>
          </form>

          {ssoButtons.length > 0 && (
            <>
              <div className="my-5 flex items-center gap-3">
                <span className="h-px flex-1 bg-slate-800" />
                <span className="text-[11px] uppercase tracking-wider text-slate-600">
                  or continue with
                </span>
                <span className="h-px flex-1 bg-slate-800" />
              </div>

              <div className="space-y-2">
                {ssoButtons.map((p) => (
                  <button
                    key={p.key}
                    type="button"
                    onClick={() => {
                      // 302 redirect flow — a full navigation, not a fetch.
                      window.location.href = `/auth/login/${p.key}`
                    }}
                    className={`flex h-10 w-full items-center justify-center gap-2.5 rounded-md border border-slate-700 bg-[var(--surface-2)] px-3 text-[13px] font-medium text-slate-200 transition-colors hover:bg-[var(--surface-hover)] hover:text-slate-100 ${focusRingCls}`}
                  >
                    {p.icon}
                    <span>Continue with {p.label}</span>
                  </button>
                ))}
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}

export default function LoginPage() {
  return (
    <Suspense fallback={null}>
      <LoginForm />
    </Suspense>
  )
}
