import { asNumber, asString, isRecord } from '@/lib/utils'
import type { MyElement } from '@/api/types'
import type { OptionField, Options } from './types'

export interface Threshold {
  value: number
  color: string
}

export const num = (o: Options, k: string, d?: number) => asNumber(o[k]) ?? d
export const str = (o: Options, k: string, d?: string) => asString(o[k]) ?? d
export const bool = (o: Options, k: string, d = false) => (typeof o[k] === 'boolean' ? (o[k] as boolean) : d)

export function thresholds(o: Options): Threshold[] {
  const raw = o.thresholds
  if (!Array.isArray(raw)) return []
  return raw
    .filter(isRecord)
    .map((t) => ({ value: asNumber(t.value), color: asString(t.color) }))
    .filter((t): t is Threshold => t.value !== undefined && !!t.color)
    .sort((a, b) => a.value - b.value)
}

/** Color for a value: the last threshold it reaches, else the base color. */
export function colorFor(value: number | undefined, ts: Threshold[], base: string): string {
  if (value === undefined) return base
  let c = base
  for (const t of ts) if (value >= t.value) c = t.color
  return c
}

/** Element-level defaults (details.unit, minValue/maxValue, min/max/step). */
export function elementDefaults(el: MyElement) {
  const d = el.details
  return {
    unit: asString(d.unit) ?? '',
    min: asNumber(d.minValue) ?? asNumber(d.min),
    max: asNumber(d.maxValue) ?? asNumber(d.max),
    step: asNumber(d.step),
    title: asString(d.title),
  }
}

/** Attribute binding of a single-value widget. */
export function binding(o: Options): { field?: string; scale?: number; offset?: number } {
  const field = str(o, 'field')?.trim()
  return { field: field || undefined, scale: num(o, 'scale'), offset: num(o, 'offset') }
}

/** Data-section fields shared by read-only numeric widgets. */
export const NUMERIC_BINDING_FIELDS: OptionField[] = [
  {
    key: 'field',
    label: 'Attribute',
    kind: 'attribute',
    section: 'data',
    accepts: ['number', 'boolean'],
    placeholder: 'auto (value)',
    help: 'Which attribute of the message to show. Click one below or type a path like gps.lat.',
  },
  { key: 'scale', label: 'Multiply by', kind: 'number', section: 'data', placeholder: '1' },
  { key: 'offset', label: 'Then add', kind: 'number', section: 'data', placeholder: '0' },
]

export const MAPPINGS_FIELD: OptionField = {
  key: 'mappings',
  label: 'Value mappings',
  kind: 'mappings',
  section: 'data',
  help: 'Show a value under another name, e.g. 1 → Running, OFF → Stopped.',
}
