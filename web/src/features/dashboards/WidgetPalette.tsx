import { Plus } from 'lucide-react'
import { WIDGETS } from '@/widgets/registry'
import { DRAG_MIME, paletteDrag } from './DashboardGrid'

/** Widget types: drag onto the grid, or click/Enter to append. */
export function WidgetPalette({
  onPick,
  onAddByElement,
}: {
  onPick: (type: string) => void
  onAddByElement: () => void
}) {
  return (
    <div className="flex h-full flex-col gap-3">
      <div>
        <h2 className="text-sm font-semibold">Widgets</h2>
        <p className="text-muted-foreground text-xs">Drag onto the grid or click to add.</p>
      </div>
      <ul className="grid min-w-0 gap-2" aria-label="Widget palette">
        {WIDGETS.map((w) => (
          <li key={w.type} className="min-w-0">
            <button
              type="button"
              draggable
              data-testid={`palette-${w.type}`}
              onDragStart={(e) => {
                paletteDrag.type = w.type
                e.dataTransfer.setData(DRAG_MIME, w.type)
                e.dataTransfer.setData('text/plain', w.type)
                e.dataTransfer.effectAllowed = 'copy'
              }}
              onDragEnd={() => {
                paletteDrag.type = null
              }}
              onClick={() => onPick(w.type)}
              className="bg-card hover:border-ring/50 hover:bg-accent/40 focus-visible:ring-ring/50 flex w-full cursor-grab items-center gap-3 rounded-lg border p-2.5 text-left shadow-xs transition-colors select-none focus-visible:ring-[3px] focus-visible:outline-none active:cursor-grabbing"
            >
              <span className="bg-muted flex size-8 shrink-0 items-center justify-center rounded-md">
                <w.icon className="size-4" />
              </span>
              <span className="min-w-0 flex-1">
                <span className="block text-sm font-medium">{w.label}</span>
                <span className="text-muted-foreground block truncate text-xs">{w.description}</span>
              </span>
            </button>
          </li>
        ))}
      </ul>
      <div className="text-muted-foreground mt-auto rounded-lg border border-dashed p-3 text-xs">
        Know the element already?
        <button
          type="button"
          onClick={onAddByElement}
          className="text-foreground mt-1.5 flex items-center gap-1 font-medium hover:underline"
        >
          <Plus className="size-3.5" /> Add by element
        </button>
      </div>
    </div>
  )
}
