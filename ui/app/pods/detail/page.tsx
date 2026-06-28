'use client'

import { Suspense, useCallback, useEffect, useMemo, useState } from 'react'
import Link from 'next/link'
import { useSearchParams } from 'next/navigation'
import {
  getPodLogs,
  listPods,
  type Pod,
  type Container,
  type ContainerEnvVar,
  type EnvVarSource,
  type PodLogsResult,
} from '@/lib/api'
import { NamespaceLink } from '@/components/namespace-link'
import { LoadingState, ErrorState, EmptyState } from '@/components/view-states'
import {
  podPhaseStyle,
  podPhaseDot,
  containerStatusStyle,
  focusRingCls,
  envSourceChipStyle,
  envSourceKindLabel,
  logLevelStyle,
} from '@/lib/styles'
import { parseLog, mostlyStructured, type ParsedLine } from '@/lib/logparse'

const labelCls = 'block text-[11px] font-semibold uppercase tracking-wider text-slate-500'
const selectCls =
  'h-8 w-auto rounded-md border border-slate-700 bg-[var(--surface-2)] px-2 text-[13px] text-slate-200 ' +
  focusRingCls

type Tab = 'overview' | 'env' | 'logs'

/**
 * Pod detail — full-window page replacing the former right-side PodDrawer.
 * Self-contained and shareable: identifiers come from the URL query
 * (?cluster=&namespace=&pod=) rather than the cross-view cluster context, so a
 * link to a specific pod always resolves. `useSearchParams` requires a Suspense
 * boundary under `output: 'export'`, so the page body is wrapped below.
 */
export default function PodDetailPage() {
  return (
    <Suspense fallback={<LoadingState label="Loading pod…" />}>
      <PodDetail />
    </Suspense>
  )
}

function PodDetail() {
  const params = useSearchParams()
  const cluster = params.get('cluster') ?? ''
  const namespace = params.get('namespace') ?? ''
  const podName = params.get('pod') ?? ''

  const [pod, setPod] = useState<Pod | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [tab, setTab] = useState<Tab>('overview')

  useEffect(() => {
    if (!cluster || !namespace || !podName) {
      setLoading(false)
      setError(null)
      setPod(null)
      return
    }
    let cancelled = false
    setLoading(true)
    setError(null)
    listPods(cluster, namespace)
      .then((pods) => {
        if (cancelled) return
        setPod(pods.find((p) => p.name === podName) ?? null)
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
  }, [cluster, namespace, podName])

  return (
    <>
      {/* Header */}
      <div className="border-b border-slate-800 px-6 py-4">
        <Link
          href="/pods"
          className={`inline-flex items-center gap-1.5 text-[12px] text-slate-500 transition-colors hover:text-slate-300 ${focusRingCls}`}
        >
          <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.9} aria-hidden="true">
            <path strokeLinecap="round" strokeLinejoin="round" d="M15 19l-7-7 7-7" />
          </svg>
          Back to Pods
        </Link>

        {pod && (
          <div className="mt-3 flex items-center gap-2.5">
            <span className={`h-2.5 w-2.5 shrink-0 rounded-full ${podPhaseDot(pod.phase)}`} aria-hidden="true" />
            <h1 className="truncate font-tech text-lg font-semibold text-slate-100">{pod.name}</h1>
            <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${podPhaseStyle(pod.phase)}`}>
              {pod.phase}
            </span>
          </div>
        )}
        {pod && (
          <p className="mt-1 font-tech text-[12px] text-slate-500">
            {cluster} / <NamespaceLink cluster={cluster} namespace={pod.namespace} />
          </p>
        )}
      </div>

      {/* Body */}
      <div className="flex flex-1 flex-col overflow-hidden">
        {loading ? (
          <LoadingState label="Loading pod…" />
        ) : error ? (
          <ErrorState label="Failed to load pod" error={error} />
        ) : !cluster || !namespace || !podName ? (
          <EmptyState label="Missing pod identifiers in the URL." />
        ) : !pod ? (
          <EmptyState label={`Pod "${podName}" not found in ${cluster} / ${namespace}.`} />
        ) : (
          <>
            {/* Tabs */}
            <div className="flex gap-1 border-b border-slate-800 px-6">
              {(['overview', 'env', 'logs'] as const).map((t) => (
                <button
                  key={t}
                  type="button"
                  onClick={() => setTab(t)}
                  aria-current={tab === t ? 'true' : undefined}
                  className={[
                    'px-3 py-2.5 text-xs font-medium capitalize transition-colors',
                    tab === t
                      ? 'border-b-2 border-[var(--accent)] text-slate-100'
                      : 'border-b-2 border-transparent text-slate-500 hover:text-slate-300',
                    focusRingCls,
                  ].join(' ')}
                >
                  {t}
                </button>
              ))}
            </div>

            <div className="flex-1 overflow-auto px-6 py-5">
              {tab === 'overview' && <OverviewPanel cluster={cluster} pod={pod} />}
              {tab === 'env' && (
                <EnvPanel cluster={cluster} namespace={pod.namespace} containers={pod.containers} />
              )}
              {tab === 'logs' && <LogsPanel cluster={cluster} pod={pod} />}
            </div>
          </>
        )}
      </div>
    </>
  )
}

function FieldRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4 border-b border-slate-800/50 py-2 last:border-b-0">
      <span className="text-[11px] font-semibold uppercase tracking-wider text-slate-500">{label}</span>
      <span className="min-w-0 truncate text-right font-tech text-[13px] text-slate-300">{children}</span>
    </div>
  )
}

function OverviewPanel({ cluster, pod }: { cluster: string; pod: Pod }) {
  return (
    <div className="grid max-w-5xl gap-8 lg:grid-cols-2">
      <div>
        <FieldRow label="Namespace">{pod.namespace}</FieldRow>
        <FieldRow label="Cluster">{cluster}</FieldRow>
        <FieldRow label="Owner">{pod.owner ? `${pod.owner.kind}/${pod.owner.name}` : '—'}</FieldRow>
        <FieldRow label="Node">{pod.node || '—'}</FieldRow>
        <FieldRow label="Ready">
          <span className={pod.ready ? 'text-emerald-400' : 'text-slate-500'}>
            {pod.ready ? 'Ready' : 'Not ready'}
          </span>
        </FieldRow>
        <FieldRow label="Restarts">{pod.restarts}</FieldRow>
      </div>

      <div className="space-y-2">
        <span className="text-[11px] font-semibold uppercase tracking-wider text-slate-500">
          Containers ({pod.containers.length})
        </span>
        <div className="space-y-1.5">
          {pod.containers.map((c) => {
            const { cls, label } = containerStatusStyle(c)
            return (
              <div
                key={c.name}
                className="flex flex-col gap-0.5 rounded-md border border-slate-800 bg-[var(--surface-2)] px-3 py-2"
              >
                <div className="flex items-center gap-2">
                  <span className={`h-3 w-3 shrink-0 rounded-[3px] ${cls}`} title={label} aria-label={label} />
                  <span className="font-tech text-[13px] text-slate-200">{c.name}</span>
                  {(c.restart_count ?? 0) > 0 && (
                    <span className="font-tech text-[11px] text-amber-400">↻ {c.restart_count}</span>
                  )}
                </div>
                <span className="break-all font-tech text-[11px] text-slate-500">{c.image}</span>
                <span className="font-tech text-[11px] text-slate-500">
                  {label}
                </span>
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}

function EnvPanel({
  cluster,
  namespace,
  containers,
}: {
  cluster: string
  namespace: string
  containers: Container[]
}) {
  const withEnv = containers.filter((c) => (c.env?.length ?? 0) > 0)
  if (withEnv.length === 0) {
    return <p className="text-[13px] text-slate-500">No environment variables.</p>
  }
  return (
    <div className="max-w-5xl space-y-6">
      {withEnv.map((c) => (
        <div key={c.name} className="space-y-2">
          {containers.length > 1 && <span className="font-tech text-xs text-slate-500">{c.name}</span>}
          <div className="overflow-hidden rounded-lg border border-slate-800">
            <table className="w-full text-[13px]">
              <thead>
                <tr className="border-b border-slate-800 bg-[var(--surface-2)]">
                  <th className="w-1/4 px-3 py-2 text-left text-[11px] font-semibold uppercase tracking-wider text-slate-500">
                    Name
                  </th>
                  <th className="px-3 py-2 text-left text-[11px] font-semibold uppercase tracking-wider text-slate-500">
                    Value
                  </th>
                  <th className="px-3 py-2 text-left text-[11px] font-semibold uppercase tracking-wider text-slate-500">
                    Source
                  </th>
                </tr>
              </thead>
              <tbody>
                {c.env!.map((e) => (
                  <EnvRow key={e.name} env={e} cluster={cluster} namespace={namespace} />
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ))}
    </div>
  )
}

function EnvRow({ env, cluster, namespace }: { env: ContainerEnvVar; cluster: string; namespace: string }) {
  return (
    <tr
      className={[
        'border-b border-slate-800/60 last:border-b-0',
        env.masked ? 'bg-amber-500/[0.03]' : '',
      ].join(' ')}
    >
      <td className="px-3 py-2 align-top font-tech text-slate-200">{env.name}</td>
      <td className="px-3 py-2 align-top">
        {env.masked ? (
          <span className="inline-flex items-center gap-1.5 text-slate-500">
            <LockIcon />
            <span className="font-tech">•••••• (masked)</span>
          </span>
        ) : env.value !== undefined ? (
          <span className="break-all font-tech text-slate-300">{env.value}</span>
        ) : (
          <span className="text-slate-600">—</span>
        )}
      </td>
      <td className="px-3 py-2 align-top">
        {env.source ? (
          <SourceChip source={env.source} cluster={cluster} namespace={namespace} />
        ) : (
          <span className="text-slate-600">—</span>
        )}
      </td>
    </tr>
  )
}

// Builds the detail-page href for an env source that maps to a raw resource the
// Config browser can render: `secret` -> /secrets/detail, `configMap` ->
// /configmaps/detail. Returns null for kinds without a browsable resource
// (field/resource refs, inline workload literals). The data key is passed as
// `&key=` so the detail page can highlight the relevant row.
function sourceHref(source: EnvVarSource, cluster: string, namespace: string): string | null {
  const kind = source.kind.trim().toLowerCase()
  const base = kind === 'secret' ? '/secrets/detail' : kind === 'configmap' ? '/configmaps/detail' : null
  if (!base || !source.name || !cluster || !namespace) return null
  const qs = new URLSearchParams({ cluster, namespace, name: source.name })
  if (source.key) qs.set('key', source.key)
  return `${base}?${qs.toString()}`
}

function SourceChip({ source, cluster, namespace }: { source: EnvVarSource; cluster: string; namespace: string }) {
  // Format: `Kind/name`, appending `/key` only when a key is present
  // (secret/configMap carry one; workload/field sources do not).
  const ref = [source.name, source.key].filter(Boolean).join('/')
  const chipCls = `inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[11px] ${envSourceChipStyle(source.kind)}`
  const inner = (
    <>
      <span className="font-semibold">{envSourceKindLabel(source.kind)}</span>
      {ref && (
        <>
          <span className="opacity-50">/</span>
          <span className="font-tech">{ref}</span>
        </>
      )}
    </>
  )

  // Secret / ConfigMap refs link to the raw source resource; other kinds
  // (field/resource/workload literals) stay non-interactive labels.
  const href = sourceHref(source, cluster, namespace)
  if (href) {
    return (
      <Link
        href={href}
        aria-label={`Open ${envSourceKindLabel(source.kind)} ${source.name ?? ''}`}
        className={`${chipCls} cursor-pointer underline decoration-dotted underline-offset-2 transition-colors hover:brightness-125 ${focusRingCls}`}
      >
        {inner}
      </Link>
    )
  }

  return <span className={chipCls}>{inner}</span>
}

const TAIL_OPTIONS = [100, 200, 500] as const
type LogView = 'raw' | 'pretty'

function LogsPanel({ cluster, pod }: { cluster: string; pod: Pod }) {
  const containerNames = pod.containers.map((c) => c.name)
  const [container, setContainer] = useState(containerNames[0] ?? '')
  const [tail, setTail] = useState<number>(200)
  // null = not yet user-chosen; the default is derived from the fetched content
  // (Pretty when most lines parse as structured, else Raw).
  const [view, setView] = useState<LogView | null>(null)
  const [result, setResult] = useState<PodLogsResult | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    getPodLogs(cluster, pod.namespace, pod.name, container || undefined, tail)
      .then((r) => {
        if (!cancelled) setResult(r)
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
  }, [cluster, pod.namespace, pod.name, container, tail])

  useEffect(() => load(), [load])

  // Parse once per fetched blob; reused by both the default-view heuristic and
  // the Pretty renderer (pure client-side, no refetch when switching modes).
  const parsed = useMemo<ParsedLine[]>(
    () => (result ? parseLog(result.lines) : []),
    [result],
  )

  // Pick a sensible default the first time content arrives, unless the user has
  // already chosen a mode explicitly.
  const effectiveView: LogView =
    view ?? (result && mostlyStructured(parsed) ? 'pretty' : 'raw')

  return (
    <div className="flex h-full flex-col gap-3">
      <div className="flex flex-wrap items-end gap-3">
        {containerNames.length > 1 && (
          <div className="space-y-1.5">
            <label htmlFor="log-container" className={labelCls}>
              Container
            </label>
            <select
              id="log-container"
              value={container}
              onChange={(e) => setContainer(e.target.value)}
              className={selectCls}
            >
              {containerNames.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </div>
        )}

        <div className="space-y-1.5">
          <label htmlFor="log-tail" className={labelCls}>
            Tail
          </label>
          <select
            id="log-tail"
            value={tail}
            onChange={(e) => setTail(Number(e.target.value))}
            className={selectCls}
          >
            {TAIL_OPTIONS.map((n) => (
              <option key={n} value={n}>
                {n} lines
              </option>
            ))}
          </select>
        </div>

        <div className="space-y-1.5">
          <span className={labelCls}>Format</span>
          <LogViewSwitcher view={effectiveView} onChange={setView} />
        </div>

        <button
          type="button"
          onClick={load}
          className={`h-8 rounded-md border border-slate-700 bg-[var(--surface-2)] px-3 text-xs text-slate-300 transition-colors hover:bg-[var(--surface-hover)] ${focusRingCls}`}
        >
          Refresh
        </button>

        {result?.truncated && (
          <span className="rounded-md bg-amber-500/10 px-2 py-1 text-[11px] font-medium text-amber-400 ring-1 ring-amber-500/20">
            truncated
          </span>
        )}
      </div>

      {loading && <p className="text-[13px] text-slate-500">Loading logs…</p>}
      {error && (
        <div className="rounded-lg border border-red-500/20 bg-red-500/5 px-3 py-2.5 text-[13px] text-red-400">
          Failed to load logs: {error}
        </div>
      )}

      {!loading &&
        !error &&
        result &&
        (result.lines.trim().length === 0 ? (
          <p className="text-[13px] text-slate-500">No log output.</p>
        ) : effectiveView === 'raw' ? (
          <pre className="min-h-0 flex-1 overflow-auto whitespace-pre rounded-lg border border-slate-800 bg-[var(--bg-deep)] p-3 font-mono text-[11px] leading-relaxed text-slate-300">
            {result.lines}
          </pre>
        ) : (
          <PrettyLogView lines={parsed} />
        ))}
    </div>
  )
}

function LogViewSwitcher({ view, onChange }: { view: LogView; onChange: (v: LogView) => void }) {
  const options: { value: LogView; label: string }[] = [
    { value: 'raw', label: 'Raw' },
    { value: 'pretty', label: 'Pretty' },
  ]
  return (
    <div
      role="radiogroup"
      aria-label="Log format"
      className="inline-flex h-8 items-center rounded-md border border-slate-700 bg-[var(--surface-2)] p-0.5"
    >
      {options.map((o) => {
        const active = view === o.value
        return (
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => onChange(o.value)}
            className={[
              'rounded px-2.5 py-1 text-xs font-medium transition-colors',
              active
                ? 'bg-[var(--accent)] text-white'
                : 'text-slate-400 hover:text-slate-200',
              focusRingCls,
            ].join(' ')}
          >
            {o.label}
          </button>
        )
      })}
    </div>
  )
}

// Very long field values are truncated inline (the full value stays in a title
// tooltip) so a single noisy field can't blow out the row width.
const MAX_FIELD_VALUE = 120

/**
 * Pretty log view — structlog-for-humans. Renders one row per parsed line:
 * dim timestamp · colored level chip · prominent message · subtle key=value
 * fields, with the whole row tinted faintly by level. Plain-text lines fall back
 * to muted mono. Pure client rendering of the already-fetched lines.
 */
function PrettyLogView({ lines }: { lines: ParsedLine[] }) {
  return (
    <div className="min-h-0 flex-1 overflow-auto rounded-lg border border-slate-800 bg-[var(--bg-deep)] py-1 font-mono text-[11px] leading-relaxed">
      {lines.map((line, i) => (
        <LogLineRow key={i} line={line} />
      ))}
    </div>
  )
}

function LogLineRow({ line }: { line: ParsedLine }) {
  // Plain (non-structured) lines: muted mono, no chip. Blank lines keep a row so
  // line order/spacing is preserved.
  if (line.kind === 'plain') {
    if (line.raw.trim() === '') return <div className="h-[1.4em]" aria-hidden="true" />
    return (
      <div className="w-max min-w-full whitespace-nowrap px-3 py-0.5 text-slate-500">
        {line.raw}
      </div>
    )
  }

  const lvl = logLevelStyle(line.level)
  const chipLabel = (line.levelLabel ?? lvl.label).toUpperCase()

  return (
    <div
      className={`flex w-max min-w-full items-baseline gap-x-2 whitespace-nowrap px-3 py-0.5 ${lvl.row}`}
    >
      {line.timestamp && (
        <span className="shrink-0 tabular-nums text-slate-600">{line.timestamp}</span>
      )}
      <span
        className={`shrink-0 rounded px-1 py-px text-[10px] font-semibold uppercase tracking-wide ${lvl.chip}`}
      >
        {chipLabel}
      </span>
      {line.message !== undefined && line.message !== '' && (
        <span className="shrink-0 text-slate-200">{line.message}</span>
      )}
      {line.fields.map((f, i) => {
        const truncated = f.value.length > MAX_FIELD_VALUE
        const shown = truncated ? `${f.value.slice(0, MAX_FIELD_VALUE)}…` : f.value
        return (
          <span key={`${f.key}-${i}`} className="shrink-0">
            <span className="text-slate-600">{f.key}</span>
            <span className="text-slate-600">=</span>
            <span className="text-slate-400" title={truncated ? f.value : undefined}>
              {shown}
            </span>
          </span>
        )
      })}
    </div>
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
