import { asNumber, isRecord } from '@/lib/utils'
import { parseTime } from '@/lib/time'
import { applyTransform, asTime, getPath, type Transform } from '@/lib/fieldPath'

/** [epoch ms, value] */
export type Point = [number, number]

/**
 * Numeric value of an element message. Sensors/switches/sliders send
 * {"value": N}; charts send {"x": ISO, "y": N}. Bare numbers/booleans are
 * accepted for robustness.
 */
export function messageValue(message: unknown): number | undefined {
  if (isRecord(message)) {
    if ('value' in message) return asNumber(message.value)
    if ('y' in message) return asNumber(message.y)
    return undefined
  }
  return asNumber(message)
}

/** Convert a message to a chart point; `fallbackTime` is used when it has no `x`. */
export function messageToPoint(
  message: unknown,
  fallbackTime: string | number | undefined,
): Point | undefined {
  const v = messageValue(message)
  if (v === undefined) return undefined
  const x = isRecord(message)
    ? parseTime(typeof message.x === 'string' || typeof message.x === 'number' ? message.x : undefined)
    : undefined
  const t = x ?? parseTime(fallbackTime)
  return t === undefined ? undefined : [t, v]
}

/**
 * The raw attribute a widget is bound to. Without a field: the legacy
 * convention ({value}, then {y}, then a bare value).
 */
export function readRaw(message: unknown, field?: string): unknown {
  if (field) return getPath(message, field)
  if (isRecord(message)) {
    if ('value' in message) return message.value
    if ('y' in message) return message.y
    return undefined
  }
  return message
}

/** Numeric value of a bound attribute, after the optional transform. */
export function readNumber(message: unknown, field?: string, t: Transform = {}): number | undefined {
  return applyTransform(asNumber(readRaw(message, field)), t)
}

/** X source of a chart: "" = auto (message.x, else receive time), "@received", or a path. */
export const X_RECEIVED = '@received'

export interface PointOptions extends Transform {
  field?: string
  xField?: string
}

/** A chart point from a message, honoring field/x bindings. */
export function bindPoint(message: unknown, receivedAt: string | number | undefined, o: PointOptions): Point | undefined {
  const v = readNumber(message, o.field, o)
  if (v === undefined) return undefined
  let t: number | undefined
  if (o.xField && o.xField !== X_RECEIVED) t = asTime(getPath(message, o.xField))
  else if (!o.xField && isRecord(message) && !o.field) {
    t = parseTime(typeof message.x === 'string' || typeof message.x === 'number' ? message.x : undefined)
  }
  t ??= parseTime(receivedAt)
  return t === undefined ? undefined : [t, v]
}
