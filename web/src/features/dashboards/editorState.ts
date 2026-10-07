/**
 * Dashboard editor state: the working layout plus undo/redo stacks.
 * Pure reducer so it is easy to test.
 */
import { uid } from '@/lib/utils'
import { findFreeSlot, type BreakpointKey, type DashboardLayout, type GridPos, type Widget } from './layout'

const HISTORY_LIMIT = 100

export interface EditorState {
  present: DashboardLayout
  past: DashboardLayout[]
  future: DashboardLayout[]
}

export type GridItem = { i: string; x: number; y: number; w: number; h: number }

export type EditorAction =
  | { type: 'reset'; layout: DashboardLayout }
  | {
      type: 'add'
      widget: Omit<Widget, 'layouts'>
      size: { w: number; h: number }
      /** drop position, in the grid of breakpoint `bp` (default lg) */
      at?: GridPos
      bp?: BreakpointKey
    }
  | { type: 'remove'; id: string }
  | { type: 'duplicate'; id: string; newId?: string }
  | { type: 'update'; id: string; patch: Partial<Pick<Widget, 'title' | 'options' | 'element_id' | 'type'>> }
  /** user finished a drag/resize at breakpoint `bp` (creates an undo step) */
  | { type: 'move'; bp: BreakpointKey; items: GridItem[] }
  /** grid compaction / breakpoint sync (no undo step) */
  | { type: 'sync'; bp: BreakpointKey; items: GridItem[] }
  | { type: 'undo' }
  | { type: 'redo' }

export function initEditor(layout: DashboardLayout): EditorState {
  return { present: layout, past: [], future: [] }
}

function commit(s: EditorState, next: DashboardLayout): EditorState {
  if (next === s.present) return s
  return { present: next, past: [...s.past, s.present].slice(-HISTORY_LIMIT), future: [] }
}

function samePos(a: GridPos | undefined, b: GridItem): boolean {
  return !!a && a.x === b.x && a.y === b.y && a.w === b.w && a.h === b.h
}

/**
 * Store grid positions for breakpoint `bp`. With `onlyArranged`, md/sm
 * positions are only updated for widgets already arranged at that size, so
 * compaction of the automatic packing never freezes it into the document.
 */
function applyItems(
  layout: DashboardLayout,
  bp: BreakpointKey,
  items: GridItem[],
  onlyArranged = false,
): DashboardLayout {
  const byId = new Map(items.map((it) => [it.i, it]))
  let changed = false
  const widgets = layout.widgets.map((w) => {
    const it = byId.get(w.id)
    if (!it || samePos(w.layouts[bp], it)) return w
    if (onlyArranged && bp !== 'lg' && !w.layouts[bp]) return w
    changed = true
    return { ...w, layouts: { ...w.layouts, [bp]: { x: it.x, y: it.y, w: it.w, h: it.h } } }
  })
  return changed ? { ...layout, widgets } : layout
}

export function editorReducer(s: EditorState, a: EditorAction): EditorState {
  switch (a.type) {
    case 'reset':
      return initEditor(a.layout)
    case 'add': {
      const dropBp = a.bp ?? 'lg'
      const lg = a.at && dropBp === 'lg' ? a.at : findFreeSlot(s.present.widgets, a.size.w, a.size.h)
      // md/sm stay automatic (packed by positionsFor) unless dropped at that size.
      const layouts: Widget['layouts'] = { lg }
      if (a.at && dropBp !== 'lg') layouts[dropBp] = a.at
      const w: Widget = { ...a.widget, options: a.widget.options ?? {}, layouts }
      return commit(s, { ...s.present, widgets: [...s.present.widgets, w] })
    }
    case 'remove': {
      if (!s.present.widgets.some((w) => w.id === a.id)) return s
      return commit(s, { ...s.present, widgets: s.present.widgets.filter((w) => w.id !== a.id) })
    }
    case 'duplicate': {
      const src = s.present.widgets.find((w) => w.id === a.id)
      if (!src) return s
      const lg = findFreeSlot(s.present.widgets, src.layouts.lg.w, src.layouts.lg.h)
      const copy: Widget = {
        ...structuredClone(src),
        id: a.newId ?? uid(),
        title: src.title ? `${src.title} (copy)` : src.title,
        layouts: { lg },
      }
      return commit(s, { ...s.present, widgets: [...s.present.widgets, copy] })
    }
    case 'update': {
      let found = false
      const widgets = s.present.widgets.map((w) => {
        if (w.id !== a.id) return w
        found = true
        return { ...w, ...a.patch }
      })
      return found ? commit(s, { ...s.present, widgets }) : s
    }
    case 'move':
      return commit(s, applyItems(s.present, a.bp, a.items))
    case 'sync': {
      const next = applyItems(s.present, a.bp, a.items, true)
      if (next === s.present) return s
      // Fold compaction into the current step so undo does not stop on it.
      return { ...s, present: next }
    }
    case 'undo': {
      const prev = s.past.at(-1)
      if (!prev) return s
      return { present: prev, past: s.past.slice(0, -1), future: [s.present, ...s.future] }
    }
    case 'redo': {
      const [next, ...rest] = s.future
      if (!next) return s
      return { present: next, past: [...s.past, s.present], future: rest }
    }
  }
}

/** Stable serialization used for dirty checks. */
export function layoutKey(l: DashboardLayout): string {
  return JSON.stringify(l)
}
