'use client'

// Live-update hooks (UI-2).
//
// Two complementary strategies, both unobtrusive, tab-visibility aware, and
// self-cleaning on unmount:
//
//   • useIncidentStream — subscribes to the backend Server-Sent Events incident
//     bus (GET /api/v1/stream, see internal/api/sse.go). The stream pushes a full
//     Incident JSON frame per open/update, so incidents update the instant the
//     detector fires rather than on the next manual refresh. Session auth rides
//     the HttpOnly cookie (same-origin, embedded build); the SSE route enforces
//     no CSRF header, so a plain EventSource works.
//
//   • useLivePoll — a generic interval refetch for endpoints without a stream
//     (events, pods). Fires the callback on a timer while the tab is visible,
//     pauses when hidden (to avoid pointless background traffic), and refetches
//     once immediately when the tab becomes visible again.

import { useEffect, useRef } from 'react'
import type { Incident } from './api'

const API_URL = process.env.NEXT_PUBLIC_API_URL || ''

// Default cadence for polled views. Slow enough to stay unobtrusive, fast enough
// to feel live for an incident console.
export const DEFAULT_POLL_MS = 15_000

interface LivePollOptions {
  intervalMs?: number
  enabled?: boolean
}

// Calls `onTick` every `intervalMs` while enabled and the tab is visible. The
// timer is suspended on tab-hide and a catch-up tick fires on tab-show. `onTick`
// is read through a ref so a changing callback identity never resets the timer.
export function useLivePoll(onTick: () => void, options: LivePollOptions = {}): void {
  const { intervalMs = DEFAULT_POLL_MS, enabled = true } = options
  const savedTick = useRef(onTick)

  useEffect(() => {
    savedTick.current = onTick
  }, [onTick])

  useEffect(() => {
    if (!enabled) return
    if (typeof window === 'undefined') return

    let timer: ReturnType<typeof setInterval> | undefined

    const start = () => {
      if (timer !== undefined) return
      timer = setInterval(() => savedTick.current(), intervalMs)
    }
    const stop = () => {
      if (timer !== undefined) {
        clearInterval(timer)
        timer = undefined
      }
    }

    const onVisibility = () => {
      if (document.visibilityState === 'visible') {
        savedTick.current() // catch up on whatever changed while hidden
        start()
      } else {
        stop()
      }
    }

    if (document.visibilityState === 'visible') start()
    document.addEventListener('visibilitychange', onVisibility)

    return () => {
      stop()
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [intervalMs, enabled])
}

interface IncidentStreamOptions {
  enabled?: boolean
}

// Subscribes to the SSE incident bus and invokes `onIncident` for each pushed
// Incident frame. Reconnection is left to the browser's built-in EventSource
// backoff. The connection is closed on unmount and while the tab is hidden (and
// reopened on show) so a backgrounded tab holds no server stream.
export function useIncidentStream(
  onIncident: (incident: Incident) => void,
  options: IncidentStreamOptions = {},
): void {
  const { enabled = true } = options
  const savedHandler = useRef(onIncident)

  useEffect(() => {
    savedHandler.current = onIncident
  }, [onIncident])

  useEffect(() => {
    if (!enabled) return
    if (typeof window === 'undefined' || typeof EventSource === 'undefined') return

    let source: EventSource | undefined

    const open = () => {
      if (source) return
      source = new EventSource(`${API_URL}/api/v1/stream`, { withCredentials: true })
      source.onmessage = (ev: MessageEvent<string>) => {
        try {
          const incident = JSON.parse(ev.data) as Incident
          savedHandler.current(incident)
        } catch {
          /* ignore heartbeats / malformed frames */
        }
      }
      source.onerror = () => {
        // Let EventSource auto-reconnect on transient errors; nothing to do.
      }
    }
    const close = () => {
      source?.close()
      source = undefined
    }

    const onVisibility = () => {
      if (document.visibilityState === 'visible') open()
      else close()
    }

    if (document.visibilityState === 'visible') open()
    document.addEventListener('visibilitychange', onVisibility)

    return () => {
      close()
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [enabled])
}
