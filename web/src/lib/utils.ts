import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/** Short random id for widgets and other client-side objects. */
export function uid(prefix = 'w'): string {
  const rnd =
    typeof crypto !== 'undefined' && 'randomUUID' in crypto
      ? crypto.randomUUID().replace(/-/g, '').slice(0, 10)
      : Math.random().toString(36).slice(2, 12)
  return `${prefix}_${rnd}`
}

export function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

export function asNumber(v: unknown): number | undefined {
  if (typeof v === 'number' && Number.isFinite(v)) return v
  if (typeof v === 'boolean') return v ? 1 : 0
  if (typeof v === 'string' && v.trim() !== '' && Number.isFinite(Number(v))) return Number(v)
  return undefined
}

export function asString(v: unknown): string | undefined {
  return typeof v === 'string' ? v : undefined
}

export function shortId(id: string): string {
  return id.slice(0, 8)
}

export function formatNumber(v: number | undefined, decimals?: number): string {
  if (v === undefined || Number.isNaN(v)) return '—'
  if (decimals !== undefined) return v.toFixed(decimals)
  return Math.abs(v) >= 100 ? v.toFixed(0) : Number.isInteger(v) ? String(v) : v.toFixed(1)
}

export function pluralize(n: number, word: string, plural = `${word}s`) {
  return `${n} ${n === 1 ? word : plural}`
}
