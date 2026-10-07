import { asNumber, isRecord } from '@/lib/utils'
import { parseTime } from '@/lib/time'

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
