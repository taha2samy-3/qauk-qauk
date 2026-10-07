import { useCallback, useMemo, useRef } from 'react'
import {
  ResponsiveGridLayout,
  useContainerWidth,
  verticalCompactor,
  type Layout,
  type LayoutItem,
} from 'react-grid-layout'
import type { MyElement } from '@/api/types'
import { cn } from '@/lib/utils'
import { getWidget } from '@/widgets/registry'
import { DashboardWidget } from './DashboardWidget'
import type { GridItem } from './editorState'
import {
  BREAKPOINT_KEYS,
  BREAKPOINTS,
  COLS,
  GRID_MARGIN,
  positionsFor,
  ROW_HEIGHT,
  type BreakpointKey,
  type GridPos,
  type Widget,
} from './layout'

export const DRAG_MIME = 'application/x-quack-widget'

/** Widget type being dragged from the palette (dataTransfer is unreadable during dragover). */
export const paletteDrag = { type: null as string | null }

interface Props {
  widgets: Widget[]
  elements: Map<string, MyElement>
  elementsLoaded: boolean
  deviceNames: Map<string, string>
  editing: boolean
  socketOpen: boolean
  onMove: (bp: BreakpointKey, items: GridItem[]) => void
  onSync: (bp: BreakpointKey, items: GridItem[]) => void
  onDropType: (type: string, at: GridPos, bp: BreakpointKey) => void
  onConfigure: (id: string) => void
  onDuplicate: (id: string) => void
  onRemove: (id: string) => void
}

const toItems = (layout: Layout): GridItem[] =>
  layout.filter((l) => !l.i.startsWith('__')).map(({ i, x, y, w, h }) => ({ i, x, y, w, h }))

export function DashboardGrid(p: Props) {
  const { width, containerRef, mounted } = useContainerWidth({ initialWidth: 1200 })
  const bpRef = useRef<BreakpointKey>('lg')
  const interacting = useRef(false)
  const { onMove, onSync, onDropType } = p

  const layouts = useMemo(() => {
    const out: Partial<Record<BreakpointKey, LayoutItem[]>> = {}
    const minWidth = (w: Widget) => getWidget(w.type)?.minSize.w ?? 1
    for (const bp of BREAKPOINT_KEYS) {
      const positions = positionsFor(p.widgets, bp, minWidth)
      out[bp] = p.widgets.map((w) => {
        const def = getWidget(w.type)
        const pos = positions.get(w.id)!
        const minW = Math.min(def?.minSize.w ?? 1, COLS[bp])
        return { i: w.id, ...pos, w: Math.max(pos.w, minW), minW, minH: def?.minSize.h ?? 2 }
      })
    }
    return out
  }, [p.widgets])

  const stop = useCallback(
    (layout: Layout) => {
      interacting.current = false
      onMove(bpRef.current, toItems(layout))
    },
    [onMove],
  )

  return (
    <div ref={containerRef as React.Ref<HTMLDivElement>} className="w-full">
      {mounted && (
        <ResponsiveGridLayout
          className={cn('dashboard-grid min-h-[60vh]', p.editing && 'is-editing')}
          width={width}
          breakpoints={BREAKPOINTS}
          cols={COLS}
          layouts={layouts}
          rowHeight={ROW_HEIGHT}
          margin={[GRID_MARGIN, GRID_MARGIN]}
          containerPadding={[0, 0]}
          compactor={verticalCompactor}
          dragConfig={{ enabled: p.editing, handle: '.widget-drag-handle', cancel: '.no-drag', threshold: 4 }}
          resizeConfig={{ enabled: p.editing, handles: ['se'] }}
          dropConfig={{
            enabled: p.editing,
            defaultItem: { w: 3, h: 4 },
            onDragOver: (e) => {
              const type = paletteDrag.type
              if (!type || !e.dataTransfer?.types.includes(DRAG_MIME)) return false
              const def = getWidget(type)
              return def
                ? { w: Math.min(def.defaultSize.w, COLS[bpRef.current]), h: def.defaultSize.h }
                : false
            },
          }}
          onBreakpointChange={(bp) => {
            bpRef.current = bp as BreakpointKey
          }}
          onLayoutChange={(layout) => {
            if (!interacting.current) onSync(bpRef.current, toItems(layout))
          }}
          onDragStart={() => {
            interacting.current = true
          }}
          onResizeStart={() => {
            interacting.current = true
          }}
          onDragStop={stop}
          onResizeStop={stop}
          onDrop={(_layout, item, e) => {
            const type = paletteDrag.type ?? (e as DragEvent).dataTransfer?.getData(DRAG_MIME)
            paletteDrag.type = null
            if (!type || !item) return
            onDropType(type, { x: item.x, y: item.y, w: item.w, h: item.h }, bpRef.current)
          }}
        >
          {p.widgets.map((w) => {
            const el = w.element_id ? p.elements.get(w.element_id) : undefined
            return (
              <div key={w.id}>
                <DashboardWidget
                  widget={w}
                  element={el}
                  elementsLoaded={p.elementsLoaded}
                  deviceName={el ? p.deviceNames.get(el.device_id) : undefined}
                  editing={p.editing}
                  socketOpen={p.socketOpen}
                  onConfigure={p.onConfigure}
                  onDuplicate={p.onDuplicate}
                  onRemove={p.onRemove}
                />
              </div>
            )
          })}
        </ResponsiveGridLayout>
      )}
    </div>
  )
}
