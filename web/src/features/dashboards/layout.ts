/**
 * The dashboard `layout` document. The backend stores it verbatim; the
 * frontend owns the schema. Bump LAYOUT_VERSION and add a migration in
 * `migrateLayout` for breaking changes.
 */
import { z } from 'zod'
import { isRecord } from '@/lib/utils'

export const LAYOUT_VERSION = 2

export const BREAKPOINTS = { lg: 1100, md: 680, sm: 0 } as const
export const COLS = { lg: 12, md: 8, sm: 4 } as const
export type BreakpointKey = keyof typeof BREAKPOINTS
export const BREAKPOINT_KEYS: BreakpointKey[] = ['lg', 'md', 'sm']
export const ROW_HEIGHT = 40
export const GRID_MARGIN = 12

const gridPos = z.object({
  x: z.number().int().min(0),
  y: z.number().int().min(0),
  w: z.number().int().min(1),
  h: z.number().int().min(1),
})
export type GridPos = z.infer<typeof gridPos>

export const widgetSchema = z.object({
  id: z.string().min(1),
  type: z.string().min(1),
  element_id: z.string().nullable(),
  title: z.string().optional(),
  options: z.record(z.string(), z.unknown()).default({}),
  layouts: z.object({ lg: gridPos, md: gridPos.optional(), sm: gridPos.optional() }),
})
export type Widget = z.infer<typeof widgetSchema>

export const layoutSchema = z.object({
  version: z.literal(LAYOUT_VERSION),
  widgets: z.array(widgetSchema),
})
export type DashboardLayout = z.infer<typeof layoutSchema>

export const emptyLayout = (): DashboardLayout => ({ version: LAYOUT_VERSION, widgets: [] })

export interface ParsedLayout {
  layout: DashboardLayout
  /** widgets that could not be read (kept out of the editor, reported to the user) */
  dropped: number
}

/**
 * Parse anything the server returns into the current layout version.
 *
 * v1 → v2: v1 stored md/sm positions that were mechanically scaled from lg,
 * which left holes on tablets. v2 only stores md/sm for boards the user
 * arranged at that size; everything else is packed by `positionsFor`. The
 * scaled v1 positions are dropped.
 */
export function migrateLayout(raw: unknown): ParsedLayout {
  if (!isRecord(raw)) return { layout: emptyLayout(), dropped: 0 }
  const version = raw.version ?? (Array.isArray(raw.widgets) ? 1 : undefined)
  if (version === 1 && Array.isArray(raw.widgets)) {
    raw = {
      version: LAYOUT_VERSION,
      widgets: raw.widgets.map((w: unknown) =>
        isRecord(w) && isRecord(w.layouts) ? { ...w, layouts: { lg: w.layouts.lg } } : w,
      ),
    }
  } else if (version !== LAYOUT_VERSION) {
    // v0 / unknown documents: nothing salvageable beyond an empty board.
    return { layout: emptyLayout(), dropped: Array.isArray(raw.widgets) ? raw.widgets.length : 0 }
  }
  const doc = raw as Record<string, unknown>
  const widgets: Widget[] = []
  let dropped = 0
  for (const w of Array.isArray(doc.widgets) ? doc.widgets : []) {
    const parsed = widgetSchema.safeParse(w)
    if (parsed.success) widgets.push(parsed.data)
    else dropped++
  }
  return { layout: { version: LAYOUT_VERSION, widgets }, dropped }
}

/** Derive a smaller breakpoint's position from the lg one. */
export function deriveLayout(lg: GridPos, bp: BreakpointKey): GridPos {
  if (bp === 'lg') return lg
  const cols = COLS[bp]
  const scale = cols / COLS.lg
  const w = bp === 'sm' ? cols : Math.max(1, Math.min(cols, Math.round(lg.w * scale)))
  const x = Math.max(0, Math.min(cols - w, Math.round(lg.x * scale)))
  return { x, y: lg.y, w, h: lg.h }
}

/** Minimum width (in lg columns) of a widget; defaults to 1. */
export type MinWidth = (w: Widget) => number

/**
 * Positions of all widgets at a breakpoint.
 * - lg: as stored.
 * - md/sm: widgets the user arranged at that size keep their stored position;
 *   the rest are packed in reading order (by lg row, then column) into the
 *   first slot that fits, so narrower screens never get holes.
 */
export function positionsFor(
  widgets: Widget[],
  bp: BreakpointKey,
  minWidth?: MinWidth,
): Map<string, GridPos> {
  const out = new Map<string, GridPos>()
  if (bp === 'lg') {
    for (const w of widgets) out.set(w.id, w.layouts.lg)
    return out
  }
  const cols = COLS[bp]
  const occupied: GridPos[] = []
  for (const w of widgets) {
    const p = w.layouts[bp]
    if (p) {
      out.set(w.id, p)
      occupied.push(p)
    }
  }
  const auto = widgets
    .filter((w) => !w.layouts[bp])
    .sort((a, b) => a.layouts.lg.y - b.layouts.lg.y || a.layouts.lg.x - b.layouts.lg.x)
  for (const w of auto) {
    const size = deriveLayout(w.layouts.lg, bp)
    const width = Math.min(cols, Math.max(size.w, minWidth?.(w) ?? 1))
    const p = firstFit(occupied, width, size.h, cols)
    out.set(w.id, p)
    occupied.push(p)
  }
  return out
}

function firstFit(occupied: GridPos[], w: number, h: number, cols: number): GridPos {
  const fits = (x: number, y: number) =>
    occupied.every((o) => x + w <= o.x || o.x + o.w <= x || y + h <= o.y || o.y + o.h <= y)
  const maxY = occupied.reduce((m, o) => Math.max(m, o.y + o.h), 0)
  for (let y = 0; y <= maxY; y++) {
    for (let x = 0; x + w <= cols; x++) if (fits(x, y)) return { x, y, w, h }
  }
  return { x: 0, y: maxY, w, h }
}

/** Bottom of the layout (in rows) for a breakpoint. */
export function bottomOf(widgets: Widget[], bp: BreakpointKey): number {
  let m = 0
  for (const p of positionsFor(widgets, bp).values()) m = Math.max(m, p.y + p.h)
  return m
}

/** First free slot (scanning rows) that fits w×h in the grid of `bp`. */
export function findFreeSlot(widgets: Widget[], w: number, h: number, bp: BreakpointKey = 'lg'): GridPos {
  const cols = COLS[bp]
  return firstFit([...positionsFor(widgets, bp).values()], Math.min(w, cols), h, cols)
}
