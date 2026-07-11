// Shared style constants implementing the Warm Operator design tokens (ADR-0006);
// the severity / signal-kind / env helpers are specific to the investigation UI.

import type { CertInfo, Severity, SignalKind } from './api'
import type { LogLevel } from './logparse'

export const focusRingCls =
  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500 focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg-base)]'

export const pageStackCls = 'space-y-8'

export const sectionHeadingCls = 'text-[13px] font-semibold tracking-tight text-slate-300'

export const cardCls = 'rounded-2xl border border-slate-800 bg-[var(--surface)] p-5 shadow-card'
export const cardClsCompact = 'rounded-2xl border border-slate-800 bg-[var(--surface)] p-4 shadow-card'

export const tableCls = 'overflow-hidden rounded-2xl border border-slate-800 bg-[var(--surface)] shadow-card'
export const thRowCls = 'border-b border-slate-800 bg-[var(--surface-2)]'
export const thCls = 'px-4 py-3 text-left text-xs font-semibold text-slate-400'

// ---- Dense (Lens/Freelens-style) console primitives ----

// Dense table chrome: thin borders, no rounded card, sticky compact header.
export const denseTableCls = 'min-w-full border-collapse text-[13px]'
export const denseThRowCls = 'border-b border-slate-800 bg-[var(--surface-2)]'
export const denseThCls =
  'sticky top-0 z-10 bg-[var(--surface-2)] px-3 py-2 text-left text-[11px] font-semibold uppercase tracking-wider text-slate-500'
export const denseTdCls = 'px-3 py-1.5 align-middle'
export const denseRowCls =
  'border-b border-slate-800/50 transition-colors last:border-b-0 hover:bg-[var(--surface-hover)]'

// Toolbar control surfaces.
export const toolbarInputCls =
  'h-8 rounded-md border border-slate-700 bg-[var(--surface-2)] px-2.5 text-[13px] text-slate-200 placeholder:text-slate-600 ' +
  focusRingCls
export const toolbarBtnCls =
  'inline-flex h-8 items-center gap-1.5 rounded-md border border-slate-700 bg-[var(--surface-2)] px-2.5 text-[13px] text-slate-300 transition-colors hover:bg-[var(--surface-hover)] hover:text-slate-100 disabled:opacity-50 ' +
  focusRingCls

// Severity badge for the Events table (info / warning / error / critical).
export function eventSeverityStyle(sev: number): { cls: string; label: string } {
  switch (sev) {
    case 3:
      return { cls: 'bg-red-500/15 text-red-300 ring-1 ring-red-500/30', label: 'critical' }
    case 2:
      return { cls: 'bg-red-500/10 text-red-400 ring-1 ring-red-500/20', label: 'error' }
    case 1:
      return { cls: 'bg-amber-500/10 text-amber-400 ring-1 ring-amber-500/20', label: 'warning' }
    default:
      return { cls: 'bg-slate-500/10 text-slate-400 ring-1 ring-slate-500/20', label: 'info' }
  }
}

// Status-dot color for a pod phase (Running=green, Pending=amber, Failed=red, …).
export function podPhaseDot(phase: string): string {
  switch (phase) {
    case 'Running':
      return 'bg-emerald-400'
    case 'Succeeded':
      return 'bg-sky-400'
    case 'Pending':
    case 'ContainerCreating':
      return 'bg-amber-400'
    case 'Failed':
    case 'CrashLoopBackOff':
    case 'Error':
      return 'bg-red-400'
    default:
      return 'bg-slate-500'
  }
}

// Compact relative-time formatter ("12s", "4m", "3h", "2d") for event/incident rows.
export function relativeTime(iso: string): string {
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return '—'
  const secs = Math.floor((Date.now() - then) / 1000)
  if (secs < 0) return 'now'
  if (secs < 60) return `${secs}s`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h`
  const days = Math.floor(hrs / 24)
  return `${days}d`
}

// Severity badge — pill style status badges.
export function severityStyle(sev: Severity): string {
  switch (sev) {
    case 3:
      return 'bg-red-500/15 text-red-300 ring-1 ring-red-500/30' // critical
    case 2:
      return 'bg-red-500/10 text-red-400 ring-1 ring-red-500/20' // error
    case 1:
      return 'bg-amber-500/10 text-amber-400 ring-1 ring-amber-500/20' // warning
    default:
      return 'bg-slate-500/10 text-slate-400 ring-1 ring-slate-500/20' // info
  }
}

// Signal-kind accent — colors the timeline by signal family.
export function signalKindStyle(kind: SignalKind): { dot: string; text: string; label: string } {
  switch (kind) {
    case 'change':
      return { dot: 'bg-indigo-400', text: 'text-indigo-300', label: 'deploy' }
    case 'metric':
      return { dot: 'bg-sky-400', text: 'text-sky-300', label: 'metric' }
    case 'k8s_event':
      return { dot: 'bg-amber-400', text: 'text-amber-300', label: 'k8s' }
    case 'log':
      return { dot: 'bg-fuchsia-400', text: 'text-fuchsia-300', label: 'log' }
    default:
      return { dot: 'bg-slate-500', text: 'text-slate-400', label: 'signal' }
  }
}

// Pod-phase badge — colors the phase column in the Workloads browser.
export function podPhaseStyle(phase: string): string {
  switch (phase) {
    case 'Running':
      return 'bg-emerald-500/10 text-emerald-400 ring-1 ring-emerald-500/20'
    case 'Succeeded':
      return 'bg-sky-500/10 text-sky-400 ring-1 ring-sky-500/20'
    case 'Pending':
    case 'ContainerCreating':
      return 'bg-amber-500/10 text-amber-400 ring-1 ring-amber-500/20'
    case 'Failed':
    case 'CrashLoopBackOff':
    case 'Error':
      return 'bg-red-500/10 text-red-400 ring-1 ring-red-500/20'
    default:
      return 'bg-slate-500/10 text-slate-400 ring-1 ring-slate-700/40'
  }
}

// Confidence badge for the assistive AI explanation panel. The LLM returns a
// free-text confidence ("low"/"medium"/"high"); unknown values fall back to a
// muted neutral pill so a misbehaving model never renders a strong/alarming
// badge. Deliberately quieter than the deterministic severity badges so the
// assistive output never visually competes with grounded evidence.
export function confidenceStyle(confidence: string): string {
  switch (confidence.trim().toLowerCase()) {
    case 'high':
      return 'bg-indigo-500/15 text-indigo-300 ring-1 ring-indigo-500/30' // strong
    case 'medium':
    case 'med':
      return 'bg-indigo-500/10 text-indigo-300/80 ring-1 ring-indigo-500/20' // normal
    case 'low':
      return 'bg-slate-500/10 text-slate-400 ring-1 ring-slate-700/40' // muted
    default:
      return 'bg-slate-500/10 text-slate-400 ring-1 ring-slate-700/40' // muted fallback
  }
}

// Workload-kind chip color, keyed by controller kind. Shared by the Workloads
// list, the Pods list "Owner" chip, and the workload detail header so a kind
// reads identically everywhere. Unknown kinds fall back to muted slate.
export function kindStyle(kind: string): string {
  switch (kind) {
    case 'Deployment':
      return 'bg-indigo-500/10 text-indigo-300 ring-1 ring-indigo-500/20'
    case 'StatefulSet':
      return 'bg-sky-500/10 text-sky-300 ring-1 ring-sky-500/20'
    case 'DaemonSet':
      return 'bg-teal-500/10 text-teal-300 ring-1 ring-teal-500/20'
    case 'Job':
    case 'CronJob':
      return 'bg-amber-500/10 text-amber-300 ring-1 ring-amber-500/20'
    default:
      return 'bg-slate-500/10 text-slate-400 ring-1 ring-slate-700/40'
  }
}

// Per-container status indicator (Lens-style squares). Returns the square's fill
// class plus a human label for the tooltip/aria text. Semantics, in priority
// order: a terminated container that exited cleanly ("Completed") is neutral
// slate; any other non-running state, or an unready/back-off container, is red
// for hard failures (CrashLoopBackOff / ImagePullBackOff / ErrImagePull / Error
// / OOMKilled) and amber for soft/pending ones (e.g. ContainerCreating,
// PodInitializing); a running+ready container is emerald; running-but-unready is
// amber; an unreported container (no state yet) is muted slate.
export function containerStatusStyle(c: {
  ready?: boolean
  state?: string
  reason?: string
}): { cls: string; label: string } {
  const state = (c.state ?? '').toLowerCase()
  const reason = c.reason ?? ''
  const hardFail = /CrashLoopBackOff|ImagePullBackOff|ErrImagePull|InvalidImageName|CreateContainerError|OOMKilled|Error/i.test(
    reason,
  )
  if (state === 'terminated') {
    if (reason === 'Completed') return { cls: 'bg-slate-500', label: 'Terminated: Completed' }
    return { cls: 'bg-red-500', label: `Terminated${reason ? `: ${reason}` : ''}` }
  }
  if (state === 'waiting') {
    return {
      cls: hardFail ? 'bg-red-500' : 'bg-amber-400',
      label: `Waiting${reason ? `: ${reason}` : ''}`,
    }
  }
  if (state === 'running') {
    return c.ready
      ? { cls: 'bg-emerald-400', label: 'Running, ready' }
      : { cls: 'bg-amber-400', label: 'Running, not ready' }
  }
  return { cls: 'bg-slate-600', label: 'Unknown' }
}

// Env-var source chip styling + display normalization. The backend sends either
// a Kubernetes ref kind (lowercase `secret`/`configMap`/`field`/`resource`) or a
// workload kind (capitalized `Deployment`/`StatefulSet`/`DaemonSet`/`Pod`). We
// normalize the kind for readable display and tint the chip subtly per family,
// kept muted so it never competes with the masked-value treatment.
export function envSourceKindLabel(kind: string): string {
  switch (kind.trim().toLowerCase()) {
    case 'secret':
      return 'Secret'
    case 'configmap':
      return 'ConfigMap'
    case 'field':
      return 'Field'
    case 'resource':
      return 'Resource'
    case 'deployment':
      return 'Deployment'
    case 'statefulset':
      return 'StatefulSet'
    case 'daemonset':
      return 'DaemonSet'
    case 'pod':
      return 'Pod'
    default:
      // Unknown kind: title-case the first letter, leave the rest as sent.
      return kind ? kind.charAt(0).toUpperCase() + kind.slice(1) : '—'
  }
}

export function envSourceChipStyle(kind: string): string {
  switch (kind.trim().toLowerCase()) {
    case 'secret':
      return 'border-amber-500/20 bg-amber-500/10 text-amber-300/90'
    case 'configmap':
      return 'border-sky-500/20 bg-sky-500/10 text-sky-300/90'
    case 'field':
    case 'resource':
      return 'border-slate-700 bg-[var(--surface-2)] text-slate-400'
    // Workload sources (inline/literal env vars).
    case 'deployment':
    case 'statefulset':
    case 'daemonset':
    case 'pod':
      return 'border-slate-700 bg-slate-500/10 text-slate-300'
    default:
      return 'border-slate-700 bg-[var(--surface-2)] text-slate-400'
  }
}

// Certificate expiry status badge. Drives the Certificates table and the cert
// panel: expired → red, within the warning window (<= 14 days) → amber, else
// green. The label encodes the remaining days so a row reads at a glance.
export const CERT_EXPIRY_WARN_DAYS = 14

export function certExpiryStatus(cert: CertInfo): { cls: string; label: string } {
  if (cert.expired) {
    return { cls: 'bg-red-500/10 text-red-400 ring-1 ring-red-500/20', label: 'Expired' }
  }
  const days = Math.max(0, Math.round(cert.expires_in_days))
  if (days <= CERT_EXPIRY_WARN_DAYS) {
    return {
      cls: 'bg-amber-500/10 text-amber-400 ring-1 ring-amber-500/20',
      label: `Expires in ${days}d`,
    }
  }
  return {
    cls: 'bg-emerald-500/10 text-emerald-400 ring-1 ring-emerald-500/20',
    label: `Valid (${days}d)`,
  }
}

// Absolute-date formatter for cert validity windows ("Jun 22, 2026").
export function formatDate(iso: string): string {
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return '—'
  return new Date(t).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
}

// Per-level styling for the "Pretty" structured-log view. `chip` colors the
// level badge; `row` is a very subtle whole-row tint (faint left border + bg) so
// error/warn lines stand out without overwhelming the dense log stream. Reuses
// the same color bands as the severity badges (red/amber/emerald/slate).
export function logLevelStyle(level: LogLevel): { chip: string; row: string; label: string } {
  switch (level) {
    case 'critical':
      return {
        chip: 'bg-red-500/15 text-red-300 ring-1 ring-red-500/30',
        row: 'border-l-2 border-red-500/40 bg-red-500/[0.04]',
        label: 'CRIT',
      }
    case 'error':
      return {
        chip: 'bg-red-500/10 text-red-400 ring-1 ring-red-500/20',
        row: 'border-l-2 border-red-500/30 bg-red-500/[0.03]',
        label: 'ERROR',
      }
    case 'warn':
      return {
        chip: 'bg-amber-500/10 text-amber-400 ring-1 ring-amber-500/20',
        row: 'border-l-2 border-amber-500/30 bg-amber-500/[0.03]',
        label: 'WARN',
      }
    case 'info':
      return {
        chip: 'bg-emerald-500/10 text-emerald-400 ring-1 ring-emerald-500/20',
        row: 'border-l-2 border-transparent',
        label: 'INFO',
      }
    case 'debug':
      return {
        chip: 'bg-slate-500/10 text-slate-400 ring-1 ring-slate-700/40',
        row: 'border-l-2 border-transparent',
        label: 'DEBUG',
      }
    case 'trace':
      return {
        chip: 'bg-slate-500/10 text-slate-500 ring-1 ring-slate-700/40',
        row: 'border-l-2 border-transparent',
        label: 'TRACE',
      }
    default:
      return {
        chip: 'bg-slate-500/10 text-slate-400 ring-1 ring-slate-700/40',
        row: 'border-l-2 border-transparent',
        label: 'LOG',
      }
  }
}

// Format a Kubernetes resource quantity (e.g. node memory capacity "16412236Ki",
// "16Gi", "8000000000") into a human GiB string with one decimal. Falls back to
// the raw value when it can't be parsed, so unexpected formats never break the
// table. Binary (Ki/Mi/Gi/Ti) and decimal (k/M/G/T) SI suffixes are both handled.
const BINARY_FACTORS: Record<string, number> = {
  Ki: 1024,
  Mi: 1024 ** 2,
  Gi: 1024 ** 3,
  Ti: 1024 ** 4,
  Pi: 1024 ** 5,
}
const DECIMAL_FACTORS: Record<string, number> = {
  k: 1e3,
  M: 1e6,
  G: 1e9,
  T: 1e12,
  P: 1e15,
}

export function formatMemoryQuantity(raw: string | undefined): string {
  if (!raw) return '—'
  const q = raw.trim()
  if (q === '') return '—'

  const binary = /^(\d+(?:\.\d+)?)([KMGTP]i)$/.exec(q)
  const decimal = /^(\d+(?:\.\d+)?)([kMGTP])$/.exec(q)
  const plain = /^(\d+(?:\.\d+)?)$/.exec(q)

  let bytes: number | null = null
  if (binary) bytes = Number(binary[1]) * BINARY_FACTORS[binary[2]]
  else if (decimal) bytes = Number(decimal[1]) * DECIMAL_FACTORS[decimal[2]]
  else if (plain) bytes = Number(plain[1])

  if (bytes === null || Number.isNaN(bytes)) return q
  const gib = bytes / 1024 ** 3
  // Sub-GiB values read better in MiB.
  if (gib < 1) {
    const mib = bytes / 1024 ** 2
    return `${mib.toFixed(0)} MiB`
  }
  return `${gib.toFixed(1)} GiB`
}

// maskValue renders the hidden preview of a secret value. Long single-line
// values keep a couple of leading + trailing characters around a fixed dot run
// (so a key stays recognizable without exposing it); short or multi-line values
// are fully dotted, since showing the edges of a short secret would leak too
// much of it.
const MASK_FULL = '••••••••'
const MASK_MIN_PARTIAL = 12 // only edge-reveal values at least this long
export function maskValue(value: string): string {
  if (value.length >= MASK_MIN_PARTIAL && !value.includes('\n')) {
    return `${value.slice(0, 2)}••••••${value.slice(-2)}`
  }
  return MASK_FULL
}

export function envBadgeStyle(env: string): string {
  switch (env) {
    case 'prod':
      return 'bg-fuchsia-500/10 text-fuchsia-400 ring-1 ring-fuchsia-500/20'
    case 'stg':
      return 'bg-amber-500/10 text-amber-400 ring-1 ring-amber-500/20'
    case 'dev':
      return 'bg-teal-500/10 text-teal-400 ring-1 ring-teal-500/20'
    default:
      return 'bg-slate-500/10 text-slate-400 ring-1 ring-slate-700/40'
  }
}
