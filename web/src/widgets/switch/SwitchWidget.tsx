import { Loader2, ToggleRight } from 'lucide-react'
import { widgetHint } from '@/api/types'
import { Switch } from '@/components/ui/switch'
import { cn } from '@/lib/utils'
import { str } from '../options'
import { usePendingCommand } from '../usePendingCommand'
import type { WidgetDefinition, WidgetRenderProps } from '../types'

function SwitchWidget({ widget, element, options, rt, editing }: WidgetRenderProps) {
  const title = widget.title || element.name
  const { pending, command } = usePendingCommand(element.id, rt, title)
  const actual = rt.value === undefined ? undefined : rt.value !== 0
  const shown = pending !== undefined ? pending !== 0 : (actual ?? false)
  const canControl = rt.permission === 'RC' && rt.status === 'subscribed' && rt.deviceConnected !== false
  const onLabel = str(options, 'onLabel') || 'On'
  const offLabel = str(options, 'offLabel') || 'Off'
  const reason =
    rt.permission !== 'RC'
      ? 'Read-only'
      : rt.deviceConnected === false
        ? 'Device offline'
        : rt.status !== 'subscribed'
          ? 'Connecting…'
          : null

  return (
    <div
      className="flex h-full flex-col items-center justify-center gap-3"
      data-testid="switch-widget"
      data-state={actual === undefined ? 'unknown' : actual ? 'on' : 'off'}
      data-pending={pending !== undefined}
    >
      <Switch
        size="lg"
        checked={shown}
        disabled={!canControl || pending !== undefined || editing}
        onCheckedChange={(v) => command(v ? 1 : 0)}
        aria-label={`${title}: ${shown ? onLabel : offLabel}`}
        className={cn(shown && 'data-[state=checked]:bg-success', pending !== undefined && 'opacity-70')}
      />
      <div className="flex h-5 items-center gap-1.5 text-sm font-medium" aria-live="polite">
        {pending !== undefined ? (
          <>
            <Loader2 className="text-muted-foreground size-3.5 animate-spin" />
            <span className="text-muted-foreground">
              Turning {pending ? onLabel.toLowerCase() : offLabel.toLowerCase()}…
            </span>
          </>
        ) : actual === undefined ? (
          <span className="text-muted-foreground">Unknown state</span>
        ) : (
          <span className={actual ? 'text-success' : 'text-muted-foreground'}>
            {actual ? onLabel : offLabel}
          </span>
        )}
      </div>
      {reason && !editing && <span className="text-muted-foreground text-[11px]">{reason}</span>}
    </div>
  )
}

export const switchWidget: WidgetDefinition = {
  type: 'switch',
  label: 'Switch',
  description: 'On/off control; waits for the device to confirm',
  icon: ToggleRight,
  defaultSize: { w: 3, h: 4 },
  minSize: { w: 2, h: 3 },
  control: true,
  fit: (el) => {
    const h = widgetHint(el)
    return h === 'switch' ? 'suggested' : h === undefined ? 'ok' : 'no'
  },
  defaultOptions: () => ({ onLabel: 'On', offLabel: 'Off' }),
  fields: [
    { key: 'onLabel', label: 'On label', kind: 'text', placeholder: 'On' },
    { key: 'offLabel', label: 'Off label', kind: 'text', placeholder: 'Off' },
  ],
  Component: SwitchWidget,
}
