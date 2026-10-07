import { ArrowLeft } from 'lucide-react'
import { useState } from 'react'
import type { MyElement } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { cn } from '@/lib/utils'
import { widgetsFor } from '@/widgets/registry'
import type { WidgetDefinition } from '@/widgets/types'
import { ElementList } from './ElementList'

/** Element-first flow: pick an element, then one of the widget types that suit it. */
export function AddWidgetDialog({
  open,
  onOpenChange,
  onAdd,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  onAdd: (el: MyElement, def: WidgetDefinition) => void
}) {
  const [element, setElement] = useState<MyElement | null>(null)

  const close = (o: boolean) => {
    if (!o) setElement(null)
    onOpenChange(o)
  }

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="flex h-[min(640px,calc(100dvh-2rem))] flex-col sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{element ? 'Choose a widget' : 'Add widget'}</DialogTitle>
          <DialogDescription>
            {element ? (
              <>
                Widgets that suit <span className="text-foreground font-medium">{element.name}</span>. The
                suggested one comes from the element’s style.
              </>
            ) : (
              'Pick the element this widget displays or controls.'
            )}
          </DialogDescription>
        </DialogHeader>
        {!element ? (
          <ElementList autoFocus onSelect={setElement} className="flex-1" />
        ) : (
          <div className="flex min-h-0 flex-1 flex-col gap-3">
            <div
              className="grid flex-1 auto-rows-min gap-2 overflow-y-auto sm:grid-cols-2"
              role="list"
              aria-label="Widget types"
            >
              {widgetsFor(element).map(({ def, fit }, i) => (
                <button
                  key={def.type}
                  type="button"
                  role="listitem"
                  autoFocus={i === 0}
                  data-testid={`widget-type-${def.type}`}
                  onClick={() => {
                    onAdd(element, def)
                    close(false)
                  }}
                  className={cn(
                    'bg-card hover:border-ring/60 hover:bg-accent/50 focus-visible:ring-ring/50 flex items-start gap-3 rounded-lg border p-3 text-left transition-colors focus-visible:ring-[3px] focus-visible:outline-none',
                    fit === 'suggested' && 'border-success/50',
                  )}
                >
                  <span className="bg-muted flex size-9 shrink-0 items-center justify-center rounded-md">
                    <def.icon className="size-4" />
                  </span>
                  <span className="min-w-0 space-y-0.5">
                    <span className="flex items-center gap-2 text-sm font-medium">
                      {def.label}
                      {fit === 'suggested' && <Badge variant="success">Suggested</Badge>}
                    </span>
                    <span className="text-muted-foreground block text-xs">{def.description}</span>
                    {def.control && element.permission !== 'RC' && (
                      <span className="dark:text-warning block text-xs text-amber-600">
                        You can view but not control this element.
                      </span>
                    )}
                  </span>
                </button>
              ))}
            </div>
            <div>
              <Button variant="ghost" size="sm" onClick={() => setElement(null)}>
                <ArrowLeft /> Back to elements
              </Button>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
