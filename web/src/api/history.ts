import { api, unwrap } from './client'
import type { HistoryEvent } from './types'
import { bindPoint, type Point, type PointOptions } from '@/realtime/messages'

export type RangeKey = '15m' | '1h' | '6h' | '24h' | '7d'
type Step = 'raw' | '1m' | '5m' | '15m' | '1h' | '1d'

export const RANGES: Record<RangeKey, { label: string; ms: number; step: Step }> = {
  '15m': { label: '15m', ms: 15 * 60_000, step: 'raw' },
  '1h': { label: '1h', ms: 60 * 60_000, step: 'raw' },
  '6h': { label: '6h', ms: 6 * 3600_000, step: '1m' },
  '24h': { label: '24h', ms: 24 * 3600_000, step: '5m' },
  '7d': { label: '7d', ms: 7 * 86400_000, step: '1h' },
}
export const RANGE_KEYS = Object.keys(RANGES) as RangeKey[]

export function isRangeKey(v: unknown): v is RangeKey {
  return typeof v === 'string' && v in RANGES
}

const RAW_LIMIT = 5000

export interface HistoryResult {
  points: Point[]
  /** raw events, or server-side aggregates per bucket */
  mode: 'raw' | 'buckets'
}

function eventsToPoints(events: HistoryEvent[] | null | undefined, o: PointOptions): Point[] {
  const out: Point[] = []
  for (const e of events ?? []) {
    const p =
      bindPoint(e.message, e.t, o) ??
      (!o.field && e.value !== null ? ([Date.parse(e.t), e.value] as Point) : undefined)
    if (p) out.push(p)
  }
  return out.sort((a, b) => a[0] - b[0])
}

/**
 * History of one element for a chart series. `o.field` binds an attribute:
 * raw events are read client-side; aggregated ranges ask the server to
 * aggregate that attribute (`?field=`; without one, the element's value:
 * message.value, a chart's y, or a bare number), always on the server
 * receive time.
 */
export async function fetchHistory(
  id: string,
  range: RangeKey,
  o: PointOptions = {},
  now = Date.now(),
): Promise<HistoryResult> {
  const r = RANGES[range]
  const to = new Date(now)
  const from = new Date(now - r.ms)
  if (r.step === 'raw') {
    // the newest RAW_LIMIT events: a busy element keeps its most recent part
    const body = await unwrap(
      api.GET('/api/v1/elements/{id}/history', {
        params: {
          path: { id },
          query: { from: from.toISOString(), to: to.toISOString(), step: 'raw', limit: RAW_LIMIT, newest: true },
        },
      }),
    )
    return { points: eventsToPoints(body.events, o), mode: 'raw' }
  }
  const body = await unwrap(
    api.GET('/api/v1/elements/{id}/history', {
      params: {
        path: { id },
        query: {
          from: from.toISOString(),
          to: to.toISOString(),
          step: r.step,
          ...(o.field && o.field !== 'value' ? { field: o.field } : {}),
        },
      },
    }),
  )
  const scale = o.scale ?? 1
  const offset = o.offset ?? 0
  const points: Point[] = (body.buckets ?? [])
    .filter((b) => b.avg !== null)
    .map((b) => [Date.parse(b.t), (b.avg as number) * scale + offset])
  return { points, mode: 'buckets' }
}
