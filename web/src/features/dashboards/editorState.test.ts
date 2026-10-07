import { describe, expect, it } from 'vitest'
import { editorReducer, initEditor, layoutKey } from './editorState'
import { emptyLayout, migrateLayout, findFreeSlot, deriveLayout, positionsFor, type Widget } from './layout'

const base = { type: 'gauge', element_id: 'e1', options: {} }

describe('editorReducer', () => {
  it('adds widgets into free slots with derived breakpoint layouts', () => {
    let s = initEditor(emptyLayout())
    s = editorReducer(s, { type: 'add', widget: { ...base, id: 'a' }, size: { w: 4, h: 6 } })
    s = editorReducer(s, { type: 'add', widget: { ...base, id: 'b' }, size: { w: 4, h: 6 } })
    const [a, b] = s.present.widgets
    expect(a!.layouts.lg).toEqual({ x: 0, y: 0, w: 4, h: 6 })
    expect(b!.layouts.lg).toEqual({ x: 4, y: 0, w: 4, h: 6 })
    // smaller breakpoints are automatic until the user arranges them
    expect(b!.layouts.md).toBeUndefined()
    expect(b!.layouts.sm).toBeUndefined()
  })

  it('supports undo/redo across add, move, update and remove', () => {
    let s = initEditor(emptyLayout())
    s = editorReducer(s, { type: 'add', widget: { ...base, id: 'a' }, size: { w: 4, h: 6 } })
    s = editorReducer(s, { type: 'move', bp: 'lg', items: [{ i: 'a', x: 2, y: 1, w: 5, h: 6 }] })
    expect(s.present.widgets[0]!.layouts.lg).toEqual({ x: 2, y: 1, w: 5, h: 6 })
    s = editorReducer(s, { type: 'update', id: 'a', patch: { title: 'Hello' } })
    s = editorReducer(s, { type: 'remove', id: 'a' })
    expect(s.present.widgets).toHaveLength(0)
    s = editorReducer(s, { type: 'undo' })
    expect(s.present.widgets[0]!.title).toBe('Hello')
    s = editorReducer(s, { type: 'undo' })
    s = editorReducer(s, { type: 'undo' })
    expect(s.present.widgets[0]!.layouts.lg.x).toBe(0)
    s = editorReducer(s, { type: 'redo' })
    expect(s.present.widgets[0]!.layouts.lg.x).toBe(2)
    // a new change clears the redo stack
    s = editorReducer(s, { type: 'duplicate', id: 'a', newId: 'c' })
    expect(s.future).toHaveLength(0)
    expect(s.present.widgets.map((w) => w.id)).toEqual(['a', 'c'])
  })

  it('does not create undo steps for no-op moves or compaction syncs', () => {
    let s = initEditor(emptyLayout())
    s = editorReducer(s, { type: 'add', widget: { ...base, id: 'a' }, size: { w: 4, h: 6 } })
    const pastLen = s.past.length
    s = editorReducer(s, { type: 'move', bp: 'lg', items: [{ i: 'a', x: 0, y: 0, w: 4, h: 6 }] })
    expect(s.past.length).toBe(pastLen)
    s = editorReducer(s, { type: 'sync', bp: 'md', items: [{ i: 'a', x: 0, y: 0, w: 3, h: 6 }] })
    expect(s.past.length).toBe(pastLen)
    // compaction of the automatic md packing is not frozen into the document
    expect(s.present.widgets[0]!.layouts.md).toBeUndefined()
    // ...but a user move at md is, and later syncs then keep it up to date
    s = editorReducer(s, { type: 'move', bp: 'md', items: [{ i: 'a', x: 2, y: 0, w: 3, h: 6 }] })
    expect(s.present.widgets[0]!.layouts.md).toEqual({ x: 2, y: 0, w: 3, h: 6 })
    s = editorReducer(s, { type: 'sync', bp: 'md', items: [{ i: 'a', x: 1, y: 0, w: 3, h: 6 }] })
    expect(s.present.widgets[0]!.layouts.md?.x).toBe(1)
  })

  it('serializes deterministically for dirty checks', () => {
    const a = initEditor(emptyLayout())
    expect(layoutKey(a.present)).toBe(layoutKey(emptyLayout()))
  })
})

describe('layout helpers', () => {
  it('migrates unknown or empty documents to the current version', () => {
    expect(migrateLayout(null).layout).toEqual(emptyLayout())
    expect(migrateLayout({}).layout).toEqual(emptyLayout())
    const parsed = migrateLayout({
      version: 2,
      widgets: [
        { id: 'a', type: 'gauge', element_id: null, layouts: { lg: { x: 0, y: 0, w: 2, h: 2 } } },
        { id: 'broken' },
      ],
    })
    expect(parsed.layout.widgets).toHaveLength(1)
    expect(parsed.layout.widgets[0]!.options).toEqual({})
    expect(parsed.dropped).toBe(1)
    expect(migrateLayout({ version: 99, widgets: [{}, {}] }).dropped).toBe(2)
  })

  it('drops the mechanically scaled md/sm positions of v1 documents', () => {
    const { layout } = migrateLayout({
      version: 1,
      widgets: [
        {
          id: 'a',
          type: 'gauge',
          element_id: null,
          options: {},
          layouts: { lg: { x: 3, y: 0, w: 3, h: 4 }, md: { x: 2, y: 0, w: 2, h: 4 } },
        },
      ],
    })
    expect(layout.version).toBe(2)
    expect(layout.widgets[0]!.layouts).toEqual({ lg: { x: 3, y: 0, w: 3, h: 4 } })
  })

  it('packs smaller breakpoints without holes', () => {
    // the demo board: row of 3+3+6, then 3+2+2+3+2 — scaling alone leaves gaps at md (8 cols)
    const mk = (id: string, x: number, y: number, w: number, h: number): Widget => ({
      id,
      type: 'gauge',
      element_id: null,
      options: {},
      layouts: { lg: { x, y, w, h } },
    })
    const ws = [
      mk('t', 0, 0, 3, 7),
      mk('wt', 3, 0, 3, 7),
      mk('soil', 6, 0, 6, 7),
      mk('hum', 0, 7, 3, 5),
      mk('pump', 3, 7, 2, 5),
      mk('burner', 5, 7, 2, 5),
      mk('fan', 7, 7, 3, 5),
      mk('st', 10, 7, 2, 5),
    ]
    const md = positionsFor(ws, 'md')
    const cells = new Set<string>()
    let bottom = 0
    for (const p of md.values()) {
      bottom = Math.max(bottom, p.y + p.h)
      for (let x = p.x; x < p.x + p.w; x++)
        for (let y = p.y; y < p.y + p.h; y++) {
          const k = `${x},${y}`
          expect(cells.has(k)).toBe(false) // no overlaps
          cells.add(k)
        }
    }
    // every column of every row above the last widget row is filled: no holes
    const lastRowTop = Math.max(...[...md.values()].map((p) => p.y))
    for (let y = 0; y < lastRowTop; y++) for (let x = 0; x < 8; x++) expect(cells.has(`${x},${y}`)).toBe(true)
    // reading order is preserved for the first row
    expect(md.get('t')!.x).toBeLessThan(md.get('wt')!.x)
    // minimum widths are respected
    const wide = positionsFor([mk('a', 0, 0, 2, 4)], 'md', () => 4)
    expect(wide.get('a')!.w).toBe(4)
    // stored md positions win
    const fixed = positionsFor(
      [
        {
          ...mk('a', 0, 0, 3, 4),
          layouts: { lg: { x: 0, y: 0, w: 3, h: 4 }, md: { x: 5, y: 2, w: 3, h: 4 } },
        },
      ],
      'md',
    )
    expect(fixed.get('a')).toEqual({ x: 5, y: 2, w: 3, h: 4 })
  })

  it('finds free slots and derives smaller layouts', () => {
    expect(findFreeSlot([], 6, 4)).toEqual({ x: 0, y: 0, w: 6, h: 4 })
    expect(deriveLayout({ x: 6, y: 2, w: 6, h: 4 }, 'md')).toEqual({ x: 4, y: 2, w: 4, h: 4 })
    expect(deriveLayout({ x: 6, y: 2, w: 6, h: 4 }, 'sm')).toEqual({ x: 0, y: 2, w: 4, h: 4 })
  })
})
