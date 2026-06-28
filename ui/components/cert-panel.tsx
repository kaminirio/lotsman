'use client'

// Shared certificate panel. Renders parsed X.509 data (CN, issuer, validity
// window with an expiry status badge, SANs, serial, key algorithm, CA flag) in
// the same key/value layout the pod Overview tab uses. Used by the Secret detail
// page (when a TLS secret carries a cert) and reachable from the Certificates view.

import type { CertInfo } from '@/lib/api'
import { certExpiryStatus, formatDate } from '@/lib/styles'

function CertRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4 border-b border-slate-800/50 py-2 last:border-b-0">
      <span className="shrink-0 text-[11px] font-semibold uppercase tracking-wider text-slate-500">{label}</span>
      <span className="min-w-0 break-all text-right font-tech text-[13px] text-slate-300">{children}</span>
    </div>
  )
}

export function CertPanel({ cert }: { cert: CertInfo }) {
  const status = certExpiryStatus(cert)
  const expiresInDays = Math.max(0, Math.round(cert.expires_in_days))

  return (
    <section
      aria-label="Certificate details"
      className="rounded-lg border border-slate-800 bg-[var(--surface-2)] p-4"
    >
      <div className="mb-3 flex items-center justify-between gap-3">
        <span className="text-[11px] font-semibold uppercase tracking-wider text-slate-400">Certificate</span>
        <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${status.cls}`}>{status.label}</span>
      </div>

      <div className="grid gap-x-8 gap-y-0 md:grid-cols-2">
        <div>
          <CertRow label="Subject CN">{cert.subject_cn || '—'}</CertRow>
          <CertRow label="Issuer CN">{cert.issuer_cn || '—'}</CertRow>
          <CertRow label="Valid From">{formatDate(cert.not_before)}</CertRow>
          <CertRow label="Valid Until">
            <span className="inline-flex items-center gap-2">
              {formatDate(cert.not_after)}
              <span className={`rounded px-1 py-0.5 text-[10px] font-medium ${status.cls}`}>{status.label}</span>
            </span>
          </CertRow>
        </div>
        <div>
          <CertRow label="Expires In">
            {cert.expired ? (
              <span className="text-red-400">expired</span>
            ) : (
              `${expiresInDays} day${expiresInDays === 1 ? '' : 's'}`
            )}
          </CertRow>
          <CertRow label="Serial">{cert.serial || '—'}</CertRow>
          <CertRow label="Key Algorithm">{cert.key_algorithm || '—'}</CertRow>
          <CertRow label="CA">{cert.is_ca ? 'Yes' : 'No'}</CertRow>
        </div>
      </div>

      <div className="mt-3 border-t border-slate-800/50 pt-3">
        <span className="text-[11px] font-semibold uppercase tracking-wider text-slate-500">
          DNS Names {cert.dns_names && cert.dns_names.length > 0 ? `(${cert.dns_names.length})` : ''}
        </span>
        {cert.dns_names && cert.dns_names.length > 0 ? (
          <div className="mt-1.5 flex flex-wrap gap-1.5">
            {cert.dns_names.map((dns) => (
              <span
                key={dns}
                className="rounded-md border border-slate-700 bg-[var(--surface)] px-2 py-0.5 font-tech text-[11px] text-slate-300"
              >
                {dns}
              </span>
            ))}
          </div>
        ) : (
          <p className="mt-1.5 text-[12px] text-slate-600">No subject alternative names.</p>
        )}
      </div>
    </section>
  )
}
