import { describe, it, expect } from 'vitest'
import {
  normalizeLevel,
  formatLogTime,
  parseLogfmt,
  parseLine,
  parseLog,
  mostlyStructured,
} from './logparse'

describe('normalizeLevel', () => {
  it('maps numeric (pino/bunyan) severities to bands', () => {
    expect(normalizeLevel('60')).toBe('critical')
    expect(normalizeLevel('50')).toBe('error')
    expect(normalizeLevel('40')).toBe('warn')
    expect(normalizeLevel('30')).toBe('info')
    expect(normalizeLevel('20')).toBe('debug')
    expect(normalizeLevel('10')).toBe('trace')
  })

  it('maps common word forms case-insensitively', () => {
    expect(normalizeLevel('FATAL')).toBe('critical')
    expect(normalizeLevel('Warning')).toBe('warn')
    expect(normalizeLevel('err')).toBe('error')
    expect(normalizeLevel('notice')).toBe('info')
    expect(normalizeLevel('verbose')).toBe('trace')
  })

  it('returns unknown for empty or unrecognized tokens', () => {
    expect(normalizeLevel('')).toBe('unknown')
    expect(normalizeLevel('bananas')).toBe('unknown')
  })
})

describe('formatLogTime', () => {
  it('formats epoch seconds to HH:MM:SS', () => {
    // 2021-01-01T00:00:00Z rendered in local time — just assert the shape.
    expect(formatLogTime('1609459200')).toMatch(/^\d{2}:\d{2}:\d{2}$/)
  })

  it('keeps sub-second precision for millisecond epochs', () => {
    expect(formatLogTime('1609459200123')).toMatch(/^\d{2}:\d{2}:\d{2}\.\d{3}$/)
  })

  it('returns the raw token when uninterpretable', () => {
    expect(formatLogTime('not-a-time')).toBe('not-a-time')
  })
})

describe('parseLogfmt', () => {
  it('parses key=value pairs including quoted values', () => {
    const fields = parseLogfmt('level=info msg="hello world" count=3')
    expect(fields).toEqual([
      { key: 'level', value: 'info' },
      { key: 'msg', value: 'hello world' },
      { key: 'count', value: '3' },
    ])
  })

  it('returns null for input without any real pair', () => {
    expect(parseLogfmt('just some prose here')).toBeNull()
  })
})

describe('parseLine', () => {
  it('parses a JSON object line into structured fields', () => {
    const line = parseLine('{"level":"error","msg":"boom","code":500}')
    expect(line.kind).toBe('json')
    expect(line.level).toBe('error')
    expect(line.message).toBe('boom')
    expect(line.fields).toEqual([{ key: 'code', value: '500' }])
  })

  it('parses a logfmt line', () => {
    const line = parseLine('ts=2021-01-01T00:00:00Z level=warn msg="disk low"')
    expect(line.kind).toBe('logfmt')
    expect(line.level).toBe('warn')
    expect(line.message).toBe('disk low')
  })

  it('falls back to plain text for unstructured lines', () => {
    const line = parseLine('a wild plain log line appeared')
    expect(line.kind).toBe('plain')
    expect(line.level).toBe('unknown')
  })

  it('never throws on malformed JSON', () => {
    const line = parseLine('{ not valid json')
    expect(line.kind).toBe('plain')
  })
})

describe('mostlyStructured', () => {
  it('is true when at least half of non-blank lines are structured', () => {
    const lines = parseLog('{"level":"info","msg":"a"}\nplain line\n{"level":"info","msg":"b"}')
    expect(mostlyStructured(lines)).toBe(true)
  })

  it('is false for an all-plain blob', () => {
    expect(mostlyStructured(parseLog('one\ntwo\nthree'))).toBe(false)
  })
})
