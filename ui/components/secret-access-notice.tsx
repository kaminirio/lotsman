'use client'

// Friendly note shown on the Secrets / Certificates views when the agent's
// secret reveal/RBAC is not enabled. The secrets routes return 502 in that case
// (SECRET_ACCESS_DISABLED_STATUS); the views detect it via ApiError and render
// this instead of a generic error — mirroring how the AI panel handles a 503.

export function SecretAccessNotice({ message }: { message?: string }) {
  return (
    <div className="mx-4 my-4 max-w-2xl rounded-lg border border-dashed border-slate-700 bg-[var(--surface-2)] px-4 py-3.5">
      <div className="flex items-center gap-2">
        <svg className="h-4 w-4 text-slate-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8} aria-hidden="true">
          <path strokeLinecap="round" strokeLinejoin="round" d="M16.5 10.5V6.75a4.5 4.5 0 10-9 0v3.75m-.75 0h10.5a2.25 2.25 0 012.25 2.25v6a2.25 2.25 0 01-2.25 2.25H6.75a2.25 2.25 0 01-2.25-2.25v-6a2.25 2.25 0 012.25-2.25z" />
        </svg>
        <span className="text-[13px] font-semibold text-slate-300">Secret access not enabled</span>
      </div>
      <p className="mt-1.5 text-[12px] leading-relaxed text-slate-500">
        The agent is not configured to read Secret data (reveal/RBAC is disabled). Enable secret
        access on the agent to browse Secrets and Certificates.
      </p>
      {message && <p className="mt-1.5 font-tech text-[11px] text-slate-600">{message}</p>}
    </div>
  )
}
