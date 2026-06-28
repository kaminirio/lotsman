// Human-friendly structured-log parsing for the pod Logs "Pretty" view.
//
// Each raw log line is classified into one of three shapes — JSON object,
// logfmt (`key=value key="quoted value" …`), or plain text — and the well-known
// fields (level / timestamp / message) are extracted so the renderer can lay a
// line out as: timestamp · level chip · message · remaining key=value fields.
//
// Everything here is defensive: parsing NEVER throws. Anything that doesn't
// cleanly parse as structured falls back to a `plain` line rendered verbatim.

// Normalized log level. `unknown` covers anything we can't map to a known band.
export type LogLevel = 'error' | 'critical' | 'warn' | 'info' | 'debug' | 'trace' | 'unknown'

export interface LogField {
  key: string
  value: string
}

// A parsed log line. `plain` lines carry only their raw text; structured lines
// (`json`/`logfmt`) carry the extracted level/timestamp/message + leftover fields.
export interface ParsedLine {
  kind: 'json' | 'logfmt' | 'plain'
  raw: string
  level: LogLevel
  // Original level token as written (e.g. "WARNING") for the chip label; falls
  // back to the normalized level when absent.
  levelLabel?: string
  timestamp?: string // formatted HH:MM:SS(.mmm) when parseable, else the raw token
  message?: string
  fields: LogField[]
}

// Field-name aliases (lower-cased) for the three well-known fields.
const LEVEL_KEYS = new Set(['level', 'lvl', 'severity', 'levelname', 'loglevel'])
const TIME_KEYS = new Set(['time', 'ts', 'timestamp', '@timestamp', 'datetime'])
const MSG_KEYS = new Set(['msg', 'message', 'log'])

// Map a free-form level token onto a normalized band. Numeric levels (pino/bunyan
// style: 10=trace … 60=fatal) and common word forms are both handled.
export function normalizeLevel(token: string): LogLevel {
  const t = token.trim().toLowerCase()
  if (t === '') return 'unknown'
  // Numeric (pino/bunyan) severities.
  if (/^\d+$/.test(t)) {
    const n = Number(t)
    if (n >= 60) return 'critical'
    if (n >= 50) return 'error'
    if (n >= 40) return 'warn'
    if (n >= 30) return 'info'
    if (n >= 20) return 'debug'
    return 'trace'
  }
  switch (t) {
    case 'emerg':
    case 'emergency':
    case 'alert':
    case 'crit':
    case 'critical':
    case 'fatal':
    case 'panic':
      return 'critical'
    case 'err':
    case 'error':
      return 'error'
    case 'warn':
    case 'warning':
      return 'warn'
    case 'info':
    case 'notice':
    case 'information':
      return 'info'
    case 'debug':
      return 'debug'
    case 'trace':
    case 'verbose':
      return 'trace'
    default:
      return 'unknown'
  }
}

// Format a timestamp token to HH:MM:SS, keeping milliseconds when the source had
// sub-second precision. Accepts RFC3339/ISO strings and epoch seconds/millis.
// Returns the original token unchanged when it can't be interpreted (never throws).
export function formatLogTime(token: string): string {
  const t = token.trim()
  if (t === '') return t
  let ms: number | null = null

  if (/^\d+(\.\d+)?$/.test(t)) {
    // Epoch: seconds (10 digits), millis (13), or fractional seconds.
    const num = Number(t)
    if (!Number.isNaN(num)) {
      ms = t.includes('.') || t.length <= 11 ? num * 1000 : num
    }
  } else {
    const parsed = Date.parse(t)
    if (!Number.isNaN(parsed)) ms = parsed
  }

  if (ms === null || Number.isNaN(ms)) return t
  const d = new Date(ms)
  if (Number.isNaN(d.getTime())) return t
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  const ss = String(d.getSeconds()).padStart(2, '0')
  // Keep millis only when the source carried sub-second precision.
  const hadSubsecond = /[.,]\d/.test(t) || (/^\d{13,}$/.test(t))
  if (hadSubsecond) {
    const milli = String(d.getMilliseconds()).padStart(3, '0')
    return `${hh}:${mm}:${ss}.${milli}`
  }
  return `${hh}:${mm}:${ss}`
}

// Stringify a JSON field value for display. Objects/arrays are compactly
// serialized; primitives become their plain string form.
function stringifyValue(v: unknown): string {
  if (v === null) return 'null'
  if (typeof v === 'string') return v
  if (typeof v === 'number' || typeof v === 'boolean') return String(v)
  try {
    return JSON.stringify(v)
  } catch {
    return String(v)
  }
}

// Tokenize a logfmt line into ordered key/value pairs. Handles `key=value`,
// `key="quoted value with spaces"` (with `\"` escapes), and bare tokens
// (recorded as a key with an empty value). Defensive: returns [] for input that
// doesn't look like logfmt so the caller can fall back to plain text.
export function parseLogfmt(line: string): LogField[] | null {
  const out: LogField[] = []
  let i = 0
  const n = line.length
  let sawPair = false

  while (i < n) {
    // Skip whitespace between pairs.
    while (i < n && /\s/.test(line[i])) i++
    if (i >= n) break

    // Read the key (up to '=' or whitespace).
    const keyStart = i
    while (i < n && line[i] !== '=' && !/\s/.test(line[i])) i++
    const key = line.slice(keyStart, i)
    if (key === '') {
      i++
      continue
    }

    if (i < n && line[i] === '=') {
      i++ // consume '='
      let value = ''
      if (i < n && line[i] === '"') {
        // Quoted value: read until the closing unescaped quote.
        i++ // opening quote
        let buf = ''
        while (i < n) {
          const ch = line[i]
          if (ch === '\\' && i + 1 < n) {
            buf += line[i + 1]
            i += 2
            continue
          }
          if (ch === '"') {
            i++ // closing quote
            break
          }
          buf += ch
          i++
        }
        value = buf
      } else {
        // Bare value: read until whitespace.
        const valStart = i
        while (i < n && !/\s/.test(line[i])) i++
        value = line.slice(valStart, i)
      }
      out.push({ key, value })
      sawPair = true
    } else {
      // Bare token with no '=' — not a real pair. Record it with an empty value.
      out.push({ key, value: '' })
    }
  }

  // Require at least one genuine `key=value` pair to call this logfmt.
  if (!sawPair) return null
  return out
}

// Pull the well-known level/timestamp/message out of an ordered field list,
// leaving the rest as display fields (order preserved). Matching is
// case-insensitive on the key.
function extractWellKnown(fields: LogField[]): {
  level: LogLevel
  levelLabel?: string
  timestamp?: string
  message?: string
  rest: LogField[]
} {
  let level: LogLevel = 'unknown'
  let levelLabel: string | undefined
  let timestamp: string | undefined
  let message: string | undefined
  const rest: LogField[] = []

  for (const f of fields) {
    const lk = f.key.toLowerCase()
    if (levelLabel === undefined && LEVEL_KEYS.has(lk)) {
      level = normalizeLevel(f.value)
      levelLabel = f.value
    } else if (timestamp === undefined && TIME_KEYS.has(lk)) {
      timestamp = formatLogTime(f.value)
    } else if (message === undefined && MSG_KEYS.has(lk)) {
      message = f.value
    } else {
      rest.push(f)
    }
  }

  return { level, levelLabel, timestamp, message, rest }
}

// Parse a single log line into its structured shape. Tries JSON first, then
// logfmt, then falls back to plain text. Never throws.
export function parseLine(line: string): ParsedLine {
  const trimmed = line.trim()
  if (trimmed === '') {
    return { kind: 'plain', raw: line, level: 'unknown', fields: [] }
  }

  // 1) JSON object.
  if (trimmed[0] === '{') {
    try {
      const obj: unknown = JSON.parse(trimmed)
      if (obj && typeof obj === 'object' && !Array.isArray(obj)) {
        const fields: LogField[] = Object.entries(obj as Record<string, unknown>).map(
          ([key, value]) => ({ key, value: stringifyValue(value) }),
        )
        const { level, levelLabel, timestamp, message, rest } = extractWellKnown(fields)
        return { kind: 'json', raw: line, level, levelLabel, timestamp, message, fields: rest }
      }
    } catch {
      /* not valid JSON — fall through */
    }
  }

  // 2) logfmt.
  const logfmt = parseLogfmt(line)
  if (logfmt && logfmt.length > 0) {
    const { level, levelLabel, timestamp, message, rest } = extractWellKnown(logfmt)
    // Only treat as structured if we actually recognized a well-known field or
    // there are multiple key=value pairs; otherwise a stray `a=b` in prose would
    // hijack the whole line. A line with a level/timestamp/message OR ≥2 pairs
    // reads better structured.
    const hasWellKnown = level !== 'unknown' || timestamp !== undefined || message !== undefined
    if (hasWellKnown || logfmt.filter((f) => f.value !== '').length >= 2) {
      return { kind: 'logfmt', raw: line, level, levelLabel, timestamp, message, fields: rest }
    }
  }

  // 3) Plain text.
  return { kind: 'plain', raw: line, level: 'unknown', fields: [] }
}

// Parse a fetched log blob into ordered lines. Bounded by the ~200 lines the
// caller fetches.
export function parseLog(text: string): ParsedLine[] {
  return text.split('\n').map(parseLine)
}

// Heuristic: are most non-blank lines structured? Drives whether "Pretty" is the
// sensible default view for a given log blob.
export function mostlyStructured(lines: ParsedLine[]): boolean {
  let structured = 0
  let total = 0
  for (const l of lines) {
    if (l.kind === 'plain' && l.raw.trim() === '') continue
    total++
    if (l.kind !== 'plain') structured++
  }
  if (total === 0) return false
  return structured / total >= 0.5
}
