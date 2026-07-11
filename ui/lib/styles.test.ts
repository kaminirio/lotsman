import { describe, it, expect } from 'vitest'
import {
  relativeTime,
  severityStyle,
  eventSeverityStyle,
  maskValue,
  formatMemoryQuantity,
  formatDate,
  certExpiryStatus,
  podPhaseDot,
} from './styles'
import type { CertInfo } from './api'

describe('relativeTime', () => {
  it('returns em dash for an unparseable timestamp', () => {
    expect(relativeTime('not-a-date')).toBe('—')
  })

  it('clamps future timestamps to "now"', () => {
    const future = new Date(Date.now() + 60_000).toISOString()
    expect(relativeTime(future)).toBe('now')
  })

  it('formats seconds, minutes, hours, and days', () => {
    expect(relativeTime(new Date(Date.now() - 5_000).toISOString())).toBe('5s')
    expect(relativeTime(new Date(Date.now() - 5 * 60_000).toISOString())).toBe('5m')
    expect(relativeTime(new Date(Date.now() - 3 * 3_600_000).toISOString())).toBe('3h')
    expect(relativeTime(new Date(Date.now() - 2 * 86_400_000).toISOString())).toBe('2d')
  })
})

describe('eventSeverityStyle', () => {
  it('maps numeric severities to labels', () => {
    expect(eventSeverityStyle(3).label).toBe('critical')
    expect(eventSeverityStyle(2).label).toBe('error')
    expect(eventSeverityStyle(1).label).toBe('warning')
    expect(eventSeverityStyle(0).label).toBe('info')
  })

  it('returns a non-empty class string', () => {
    expect(eventSeverityStyle(3).cls).toContain('red')
    expect(eventSeverityStyle(1).cls).toContain('amber')
  })
})

describe('severityStyle', () => {
  it('tints by band', () => {
    expect(severityStyle(3)).toContain('red')
    expect(severityStyle(2)).toContain('red')
    expect(severityStyle(1)).toContain('amber')
    expect(severityStyle(0)).toContain('slate')
  })

  it('distinguishes critical from error (UI-4)', () => {
    // Both are red-tinted, but critical must be stronger than error so the two
    // bands never collapse into an identical pill.
    expect(severityStyle(3)).not.toBe(severityStyle(2))
    expect(severityStyle(3)).toContain('red-500/15')
    expect(severityStyle(2)).toContain('red-500/10')
  })

  it('stays consistent with eventSeverityStyle bands', () => {
    expect(severityStyle(3)).toBe(eventSeverityStyle(3).cls)
    expect(severityStyle(2)).toBe(eventSeverityStyle(2).cls)
  })
})

describe('podPhaseDot', () => {
  it('colors known phases and falls back for unknown', () => {
    expect(podPhaseDot('Running')).toBe('bg-emerald-400')
    expect(podPhaseDot('Failed')).toBe('bg-red-400')
    expect(podPhaseDot('Wat')).toBe('bg-slate-500')
  })
})

describe('maskValue', () => {
  it('edge-reveals long single-line values', () => {
    expect(maskValue('abcdefghijklmnop')).toBe('ab••••••op')
  })

  it('fully masks short values', () => {
    expect(maskValue('short')).toBe('••••••••')
  })

  it('fully masks multi-line values even when long', () => {
    expect(maskValue('line-one-is-long\nline-two')).toBe('••••••••')
  })
})

describe('formatMemoryQuantity', () => {
  it('formats binary suffixes to GiB', () => {
    expect(formatMemoryQuantity('16Gi')).toBe('16.0 GiB')
    expect(formatMemoryQuantity('16412236Ki')).toBe('15.7 GiB')
  })

  it('formats sub-GiB values as MiB', () => {
    expect(formatMemoryQuantity('512Mi')).toBe('512 MiB')
  })

  it('handles plain byte counts', () => {
    expect(formatMemoryQuantity('8000000000')).toBe('7.5 GiB')
  })

  it('returns em dash for empty and raw for unparseable', () => {
    expect(formatMemoryQuantity(undefined)).toBe('—')
    expect(formatMemoryQuantity('')).toBe('—')
    expect(formatMemoryQuantity('lots')).toBe('lots')
  })
})

describe('formatDate', () => {
  it('returns em dash for an invalid date', () => {
    expect(formatDate('nope')).toBe('—')
  })

  it('formats a valid ISO date', () => {
    expect(formatDate('2026-06-22T00:00:00Z')).toMatch(/2026/)
  })
})

describe('certExpiryStatus', () => {
  const base: CertInfo = {
    subject_cn: 'example.com',
    issuer_cn: 'CA',
    not_before: '2026-01-01T00:00:00Z',
    not_after: '2026-12-31T00:00:00Z',
    expired: false,
    expires_in_days: 90,
  }

  it('flags expired certs', () => {
    expect(certExpiryStatus({ ...base, expired: true }).label).toBe('Expired')
  })

  it('warns inside the warning window', () => {
    expect(certExpiryStatus({ ...base, expires_in_days: 5 }).label).toBe('Expires in 5d')
  })

  it('reports healthy certs with remaining days', () => {
    expect(certExpiryStatus(base).label).toBe('Valid (90d)')
  })
})
