/**
 * Value mappings: show a raw value under another name and color, e.g.
 * 1 → "Running" (green), 0 → "Stopped" (red), "OPEN" → "Door open".
 * Matching is by normalized text, so 1, "1" and true/"true" behave predictably.
 */
import { isRecord } from './utils'

export interface ValueMapping {
  match: string
  label: string
  color?: string
}

export function normalize(v: unknown): string {
  if (v === undefined || v === null) return ''
  if (typeof v === 'boolean') return v ? 'true' : 'false'
  if (typeof v === 'number') return String(v)
  if (typeof v === 'string') {
    const t = v.trim()
    const n = Number(t)
    return t !== '' && Number.isFinite(n) ? String(n) : t.toLowerCase()
  }
  return JSON.stringify(v)
}

export function readMappings(raw: unknown): ValueMapping[] {
  if (!Array.isArray(raw)) return []
  return raw
    .filter(isRecord)
    .map((m) => ({
      match: typeof m.match === 'string' ? m.match : String(m.match ?? ''),
      label: typeof m.label === 'string' ? m.label : '',
      color: typeof m.color === 'string' && m.color ? m.color : undefined,
    }))
    .filter((m) => m.match.trim() !== '' && m.label.trim() !== '')
}

/** The first mapping whose `match` equals the value (normalized), if any. */
export function mapValue(v: unknown, mappings: readonly ValueMapping[]): ValueMapping | undefined {
  if (v === undefined) return undefined
  const key = normalize(v)
  return mappings.find((m) => normalize(m.match) === key)
}

/** Parse a user-entered value for sending: numbers and booleans stay typed. */
export function parseLiteral(text: string): string | number | boolean {
  const t = text.trim()
  if (t === 'true') return true
  if (t === 'false') return false
  const n = Number(t)
  return t !== '' && Number.isFinite(n) ? n : t
}
