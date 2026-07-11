import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useLivePoll, useIncidentStream } from './use-live'

// use-live.ts drives the two live-update hooks (polling + SSE). Both are
// tab-visibility aware and self-cleaning, so the risks worth locking down are:
// leaked timers/streams on unmount, double-starting on repeated visible events,
// and the catch-up fire on becoming visible. We stub `document.visibilityState`
// and `EventSource` and run under fake timers to exercise all of that
// deterministically.

// ---- visibility stubbing --------------------------------------------------

function defineVisibility(state: 'visible' | 'hidden'): void {
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    get: () => state,
  })
}

// Change visibility AND fire the event the hooks listen on.
function changeVisibility(state: 'visible' | 'hidden'): void {
  defineVisibility(state)
  document.dispatchEvent(new Event('visibilitychange'))
}

// ---- EventSource stub -----------------------------------------------------

class FakeEventSource {
  static instances: FakeEventSource[] = []
  url: string
  withCredentials: boolean
  onmessage: ((ev: MessageEvent<string>) => void) | null = null
  onerror: (() => void) | null = null
  close = vi.fn()

  constructor(url: string, init?: { withCredentials?: boolean }) {
    this.url = url
    this.withCredentials = init?.withCredentials ?? false
    FakeEventSource.instances.push(this)
  }
}

beforeEach(() => {
  vi.useFakeTimers()
  defineVisibility('visible')
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  defineVisibility('visible')
})

describe('useLivePoll', () => {
  it('ticks on the interval while visible', () => {
    const tick = vi.fn()
    renderHook(() => useLivePoll(tick, { intervalMs: 1000 }))
    act(() => {
      vi.advanceTimersByTime(2500)
    })
    expect(tick).toHaveBeenCalledTimes(2)
  })

  it('clears the interval on unmount (no leaked timer)', () => {
    const tick = vi.fn()
    const { unmount } = renderHook(() => useLivePoll(tick, { intervalMs: 1000 }))
    act(() => {
      vi.advanceTimersByTime(2000)
    })
    expect(tick).toHaveBeenCalledTimes(2)
    unmount()
    act(() => {
      vi.advanceTimersByTime(5000)
    })
    // Nothing fires after unmount.
    expect(tick).toHaveBeenCalledTimes(2)
  })

  it('does not stack a second interval on repeated visible events', () => {
    const tick = vi.fn()
    renderHook(() => useLivePoll(tick, { intervalMs: 1000 }))
    // Repeatedly re-signalling "visible" must not start a second timer; start()
    // is guarded. (Each visible event still fires one immediate catch-up.)
    act(() => {
      changeVisibility('visible')
      changeVisibility('visible')
    })
    tick.mockClear()
    act(() => {
      vi.advanceTimersByTime(1000)
    })
    // One interval running => exactly one tick per period, not two.
    expect(tick).toHaveBeenCalledTimes(1)
  })

  it('suspends the interval while the tab is hidden', () => {
    const tick = vi.fn()
    renderHook(() => useLivePoll(tick, { intervalMs: 1000 }))
    act(() => {
      changeVisibility('hidden')
    })
    tick.mockClear()
    act(() => {
      vi.advanceTimersByTime(5000)
    })
    expect(tick).not.toHaveBeenCalled()
  })

  it('fires a single catch-up tick when the tab becomes visible', () => {
    const tick = vi.fn()
    defineVisibility('hidden')
    renderHook(() => useLivePoll(tick, { intervalMs: 1000 }))
    // Mounted hidden: no timer, no tick.
    act(() => {
      vi.advanceTimersByTime(3000)
    })
    expect(tick).not.toHaveBeenCalled()
    // Becoming visible: one immediate catch-up refresh, then the timer resumes.
    act(() => {
      changeVisibility('visible')
    })
    expect(tick).toHaveBeenCalledTimes(1)
    act(() => {
      vi.advanceTimersByTime(1000)
    })
    expect(tick).toHaveBeenCalledTimes(2)
  })

  it('does nothing when disabled', () => {
    const tick = vi.fn()
    renderHook(() => useLivePoll(tick, { intervalMs: 1000, enabled: false }))
    act(() => {
      vi.advanceTimersByTime(5000)
    })
    expect(tick).not.toHaveBeenCalled()
  })
})

describe('useIncidentStream', () => {
  it('opens a stream on mount and closes it on unmount', () => {
    const onIncident = vi.fn()
    const { unmount } = renderHook(() => useIncidentStream(onIncident))
    expect(FakeEventSource.instances).toHaveLength(1)
    expect(FakeEventSource.instances[0].withCredentials).toBe(true)
    unmount()
    expect(FakeEventSource.instances[0].close).toHaveBeenCalledTimes(1)
  })

  it('closes the stream when hidden and reopens on visible', () => {
    renderHook(() => useIncidentStream(vi.fn()))
    const first = FakeEventSource.instances[0]
    act(() => {
      changeVisibility('hidden')
    })
    expect(first.close).toHaveBeenCalledTimes(1)
    act(() => {
      changeVisibility('visible')
    })
    // A fresh stream is opened on show.
    expect(FakeEventSource.instances).toHaveLength(2)
  })

  it('does not open a second stream on repeated visible events', () => {
    renderHook(() => useIncidentStream(vi.fn()))
    act(() => {
      changeVisibility('visible')
      changeVisibility('visible')
    })
    // open() is guarded by an existing source; still just one connection.
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  it('parses pushed incident frames and ignores malformed ones', () => {
    const onIncident = vi.fn()
    renderHook(() => useIncidentStream(onIncident))
    const src = FakeEventSource.instances[0]
    act(() => {
      src.onmessage?.({ data: JSON.stringify({ id: 'inc-1' }) } as MessageEvent<string>)
      src.onmessage?.({ data: 'not-json (heartbeat)' } as MessageEvent<string>)
    })
    expect(onIncident).toHaveBeenCalledTimes(1)
    expect(onIncident).toHaveBeenCalledWith({ id: 'inc-1' })
  })

  it('does not open a stream when disabled', () => {
    renderHook(() => useIncidentStream(vi.fn(), { enabled: false }))
    expect(FakeEventSource.instances).toHaveLength(0)
  })
})
