import { Activity } from 'lucide-react'
import { RelativeTime } from '@/components/RelativeTime'
import { cn } from '@/lib/utils'
import type { WidgetDefinition, WidgetRenderProps } from '../types'

function StatusWidget({ rt }: WidgetRenderProps) {
  const state = rt.deviceConnected === null ? 'unknown' : rt.deviceConnected ? 'online' : 'offline'
  return (
    <div
      className="flex h-full flex-col items-center justify-center gap-2 text-center"
      data-testid="status-widget"
      data-state={state}
    >
      <span className="relative flex size-10 items-center justify-center">
        {state === 'online' && (
          <span className="bg-success/30 absolute inline-flex size-full animate-ping rounded-full" />
        )}
        <span
          className={cn(
            'relative inline-flex size-5 rounded-full ring-4',
            state === 'online' && 'bg-success ring-success/20',
            state === 'offline' && 'bg-destructive ring-destructive/20',
            state === 'unknown' && 'bg-muted-foreground/40 ring-muted',
          )}
        />
      </span>
      <div className="text-lg font-semibold capitalize">{state}</div>
      <div className="text-muted-foreground text-xs">
        Last message <RelativeTime value={rt.lastEditAt} />
      </div>
    </div>
  )
}

export const statusWidget: WidgetDefinition = {
  type: 'status',
  label: 'Device status',
  description: 'Online/offline state of the element’s device',
  icon: Activity,
  defaultSize: { w: 2, h: 4 },
  minSize: { w: 2, h: 3 },
  fit: () => 'ok',
  defaultOptions: () => ({}),
  fields: [],
  Component: StatusWidget,
}
