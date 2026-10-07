/**
 * Attribute paths into element messages: "temperature", "gps.lat",
 * "sensors[0].temp". The same syntax the server accepts for history
 * aggregation (`?field=`), see server/internal/store/fieldpath.go.
 */
import { asNumber, isRecord } from './utils'

const PATH_RE = /^[A-Za-z0-9_@$-]+(\.[A-Za-z0-9_@$-]+|\[[0-9]+\])*$/

export function isValidPath(path: string): boolean {
  return path.length > 0 && path.length <= 200 && PATH_RE.test(path)
}

/** "sensors[0].temp" → ["sensors", 0, "temp"] */
export function parsePath(path: string): (string | number)[] {
  const out: (string | number)[] = []
  for (const part of path.split('.')) {
    const m = /^([^[]+)((?:\[\d+\])*)$/.exec(part)
    if (!m) return []
    out.push(m[1]!)
    for (const idx of m[2]!.matchAll(/\[(\d+)\]/g)) out.push(Number(idx[1]))
  }
  return out
}

export function getPath(obj: unknown, path: string): unknown {
  let cur: unknown = obj
  for (const key of parsePath(path)) {
    if (typeof key === 'number') {
      if (!Array.isArray(cur)) return undefined
      cur = cur[key]
    } else {
      if (!isRecord(cur)) return undefined
      cur = cur[key]
    }
  }
  return cur
}

/** Build the message a control widget sends: setPath({}, "relay", "ON") → {relay: "ON"}. */
export function buildMessage(path: string, value: unknown): Record<string, unknown> {
  const keys = parsePath(path)
  if (keys.length === 0) return { value }
  const root: Record<string, unknown> = {}
  let cur: Record<string, unknown> | unknown[] = root
  keys.forEach((key, i) => {
    const last = i === keys.length - 1
    const next = last ? value : typeof keys[i + 1] === 'number' ? [] : {}
    if (Array.isArray(cur)) cur[key as number] = next
    else cur[key as string] = next
    if (!last) cur = next as Record<string, unknown> | unknown[]
  })
  return root
}

export type FieldKind = 'number' | 'boolean' | 'string' | 'time'

export interface DiscoveredField {
  path: string
  kind: FieldKind
  sample: unknown
}

const TIME_KEY = /^(ts|time|timestamp|date|datetime|at|created_at|updated_at)$/i

function kindOf(key: string, v: unknown): FieldKind | undefined {
  if (typeof v === 'boolean') return 'boolean'
  if (typeof v === 'number') return TIME_KEY.test(key) && v > 1e9 ? 'time' : 'number'
  if (typeof v === 'string') {
    if (asNumber(v) !== undefined) return 'number'
    if (v.length >= 10 && /^\d{4}-\d{2}-\d{2}/.test(v) && !Number.isNaN(Date.parse(v))) return 'time'
    return 'string'
  }
  return undefined
}

/**
 * Leaf attributes found in recent messages (newest sample wins), so the UI
 * can offer them with an example value. Bounded in depth and count.
 */
export function discoverFields(messages: readonly unknown[], max = 40): DiscoveredField[] {
  const found = new Map<string, DiscoveredField>()
  const walk = (v: unknown, path: string, key: string, depth: number) => {
    if (found.size >= max || depth > 4) return
    if (Array.isArray(v)) {
      v.slice(0, 3).forEach((item, i) => walk(item, `${path}[${i}]`, key, depth + 1))
      return
    }
    if (isRecord(v)) {
      for (const [k, child] of Object.entries(v)) walk(child, path ? `${path}.${k}` : k, k, depth + 1)
      return
    }
    const kind = kindOf(key, v)
    if (kind && path && isValidPath(path)) found.set(path, { path, kind, sample: v })
  }
  for (const m of messages) walk(m, '', '', 0)
  return [...found.values()]
}

/** Parse a time attribute: ISO string, or epoch in ms (or s if it looks like seconds). */
export function asTime(v: unknown): number | undefined {
  if (typeof v === 'number' && Number.isFinite(v)) return v < 1e12 ? v * 1000 : v
  if (typeof v === 'string') {
    const n = Number(v)
    if (v.trim() !== '' && Number.isFinite(n)) return n < 1e12 ? n * 1000 : n
    const t = Date.parse(v)
    return Number.isNaN(t) ? undefined : t
  }
  return undefined
}

export interface Transform {
  scale?: number
  offset?: number
}

export function applyTransform(v: number | undefined, t: Transform): number | undefined {
  if (v === undefined) return undefined
  return v * (t.scale ?? 1) + (t.offset ?? 0)
}
