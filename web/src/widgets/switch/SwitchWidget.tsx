import { Loader2, ToggleRight } from 'lucide-react'
import { widgetHint } from '@/api/types'
import { Switch } from '@/components/ui/switch'
import { cn } from '@/lib/utils'
import { str } from '../options'
import { readRaw } from '@/realtime/messages'
import { normalize, parseLiteral } from '@/lib/valueMap'
import { asNumber } from '@/lib/utils'
import { usePendingCommand } from '../usePendingCommand'
import type { WidgetDefinition, WidgetRenderProps } from '../types'

/** On/off state of a raw attribute; explicit on/off values win, then common conventions. */
export function switchState(
  raw: unknown,
  onVal: unknown,
  offVal: unknown,
  explicit: boolean,
): boolean | undefined {
  if (raw === undefined || raw === null) return undefined
  const n = normalize(raw)
  if (n === normalize(onVal)) return true
  if (n === normalize(offVal)) return false
  if (explicit) return undefined
  if (['on', 'true', 'open', 'yes', 'high'].includes(n)) return true
  if (['off', 'false', 'closed', 'no', 'low'].includes(n)) return false
  const num = asNumber(raw)
  return num === undefined ? undefined : num !== 0
}

function SwitchWidget({ widget, element, options, rt, editing }: WidgetRenderProps) {
  const title = widget.title || element.name
  const field = str(options, 'field')?.trim() || undefined
  const onText = str(options, 'onValue')?.trim()
  const offText = str(options, 'offValue')?.trim()
  const onVal = onText ? parseLiteral(onText) : 1
  const offVal = offText ? parseLiteral(offText) : 0
  const { pending, command } = usePendingCommand(element.id, rt, title, field)
  const actual = switchState(readRaw(rt.message, field), onVal, offVal, !!(onText || offText))
  const pendingOn = pending === undefined ? undefined : normalize(pending) === normalize(onVal)
  const shown = pendingOn ?? actual ?? false
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
        onCheckedChange={(v) => command(v ? onVal : offVal)}
        aria-label={`${title}: ${shown ? onLabel : offLabel}`}
        className={cn(shown && 'data-[state=checked]:bg-success', pending !== undefined && 'opacity-70')}
      />
      <div className="flex h-5 items-center gap-1.5 text-sm font-medium" aria-live="polite">
        {pending !== undefined ? (
          <>
            <Loader2 className="text-muted-foreground size-3.5 animate-spin" />
            <span className="text-muted-foreground">
              Turning {pendingOn ? onLabel.toLowerCase() : offLabel.toLowerCase()}…
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
    {
      key: 'field',
      label: 'Attribute',
      kind: 'attribute',
      section: 'data',
      placeholder: 'auto (value)',
      help: 'Read the state from this attribute and send commands to it, in the device’s own format.',
    },
    {
      key: 'onValue',
      label: 'On value',
      kind: 'text',
      section: 'data',
      placeholder: '1',
      help: 'e.g. 1, true, ON',
    },
    {
      key: 'offValue',
      label: 'Off value',
      kind: 'text',
      section: 'data',
      placeholder: '0',
      help: 'e.g. 0, false, OFF',
    },
    { key: 'onLabel', label: 'On label', kind: 'text', placeholder: 'On' },
    { key: 'offLabel', label: 'Off label', kind: 'text', placeholder: 'Off' },
  ],
  Component: SwitchWidget,
}
