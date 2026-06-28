'use client'

import { useCallback, useEffect, useRef, useState } from 'react'
import { explainIncident, EXPLAINER_DISABLED_STATUS, ApiError, type Explanation } from '@/lib/api'
import { confidenceStyle, focusRingCls } from '@/lib/styles'

type State =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; explanation: Explanation }
  | { kind: 'disabled' }
  | { kind: 'error'; message: string }

/**
 * AiExplanation — ASSISTIVE, opt-in LLM narrative for a single incident.
 *
 * This panel is intentionally visually distinct from the deterministic
 * probable-cause / timeline output: a dashed accent border and an "AI" chip
 * signal that it is generated, not authoritative. The user must click to
 * request it (it can take ~30s on a CPU model). When the explainer feature is
 * not enabled the endpoint returns 503 and we render a muted "not configured"
 * note rather than an error — that is the default state.
 *
 * Stale-guard: each request bumps a ref-tracked id; a response is only applied
 * if it belongs to the latest request for the *current* incident, so switching
 * incidents or re-running never renders a stale narrative.
 */
export function AiExplanation({ incidentId }: { incidentId: string }) {
  const [state, setState] = useState<State>({ kind: 'idle' })
  const requestId = useRef(0)

  // Reset when the incident this panel is attached to changes.
  useEffect(() => {
    requestId.current += 1
    setState({ kind: 'idle' })
  }, [incidentId])

  const run = useCallback(() => {
    const myId = ++requestId.current
    setState({ kind: 'loading' })
    explainIncident(incidentId)
      .then((explanation) => {
        if (requestId.current === myId) setState({ kind: 'ready', explanation })
      })
      .catch((e) => {
        if (requestId.current !== myId) return
        if (e instanceof ApiError && e.status === EXPLAINER_DISABLED_STATUS) {
          setState({ kind: 'disabled' })
        } else {
          setState({ kind: 'error', message: e instanceof Error ? e.message : String(e) })
        }
      })
  }, [incidentId])

  return (
    <section
      aria-label="AI explanation (assistive)"
      className="mt-3 rounded-lg border border-dashed border-indigo-500/30 bg-[var(--accent-soft)] px-3 py-2.5"
    >
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <span className="rounded bg-indigo-500/20 px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-wider text-indigo-300 ring-1 ring-indigo-500/30">
            AI
          </span>
          <span className="text-[11px] font-semibold uppercase tracking-wider text-indigo-300/80">
            AI explanation (assistive)
          </span>
        </div>

        {state.kind !== 'loading' && (
          <button
            type="button"
            onClick={run}
            className={
              'inline-flex h-7 items-center gap-1.5 rounded-md border border-indigo-500/30 bg-indigo-500/10 px-2.5 text-[12px] font-medium text-indigo-200 transition-colors hover:bg-indigo-500/20 hover:text-indigo-100 disabled:opacity-50 ' +
              focusRingCls
            }
          >
            {state.kind === 'ready' ? 'Regenerate' : 'Explain with AI'}
          </button>
        )}
      </div>

      {state.kind === 'idle' && (
        <p className="mt-2 text-[12px] text-slate-500">
          Generate a plain-English root-cause narrative from the deterministic findings below.
        </p>
      )}

      {state.kind === 'loading' && (
        <div className="mt-2 flex items-center gap-2 text-[12px] text-slate-400" aria-busy="true">
          <span
            className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-slate-700 border-t-indigo-400"
            aria-hidden="true"
          />
          Analyzing… (small CPU model, may take ~30s)
        </div>
      )}

      {state.kind === 'disabled' && (
        <p className="mt-2 text-[12px] text-slate-500">
          AI explainer not configured (set <span className="font-tech">LOTSMAN_LLM_URL</span>).
        </p>
      )}

      {state.kind === 'error' && (
        <p role="alert" className="mt-2 text-[12px] text-red-300">
          Failed to generate explanation: {state.message}
        </p>
      )}

      {state.kind === 'ready' && (
        <div className="mt-2 space-y-2">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="rounded px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-slate-400 ring-1 ring-slate-700/40">
              {state.explanation.category}
            </span>
            <span
              className={`rounded px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide ${confidenceStyle(
                state.explanation.confidence,
              )}`}
            >
              {state.explanation.confidence} confidence
            </span>
          </div>
          <p className="whitespace-pre-line text-[13px] leading-relaxed text-slate-200">
            {state.explanation.summary}
          </p>
          <p className="border-t border-indigo-500/15 pt-2 text-[11px] text-slate-500">
            Generated by <span className="font-tech">{state.explanation.model}</span> · assistive, not
            authoritative — verify against the evidence below.
          </p>
        </div>
      )}
    </section>
  )
}
