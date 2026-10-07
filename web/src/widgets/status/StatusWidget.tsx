import { Activity } from 'lucide-react'
import { RelativeTime } from '@/components/RelativeTime'
import { resolveColor } from '@/lib/chartTheme'
import { useTheme } from '@/lib/theme'
import { cn, formatNumber } from '@/lib/utils'
import { mapValue, readMappings } from '@/lib/valueMap'
import { readRaw } from '@/realtime/messages'
import { MAPPINGS_FIELD, str } from '../options'
import type { WidgetDefinition, WidgetRenderProps } from '../types'

function DeviceStatus({ rt }: WidgetRenderProps) {
  const state = rt.deviceConnected === null ? 'unknown' : rt.deviceConnected ? 'online' : 'offline'
  return (
    <Shell testState={state} subtitle={<>Last message <RelativeTime value={rt.lastEditAt} /></>}>
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
    </Shell>
  )
}

/** The bound attribute, shown through value mappings (1 → Running, OFF → Stopped…). */
function AttributeStatus({ rt, options }: WidgetRenderProps) {
  const { resolved } = useTheme()
  const field = str(options, 'field')?.trim() || undefined
  const raw = readRaw(rt.message, field)
  const mapped = mapValue(raw, readMappings(options.mappings))
  const label =
    mapped?.label ??
    (raw === undefined ? 'No data' : typeof raw === 'number' ? formatNumber(raw) : String(raw))
  const color = mapped?.color ? resolveColor(mapped.color, resolved) : undefined
  return (
    <Shell testState={label} subtitle={<>Updated <RelativeTime value={rt.lastEditAt} /></>}>
      <span className="relative flex size-10 items-center justify-center">
        <span
          className={cn('relative inline-flex size-5 rounded-full ring-4', !color && 'bg-muted-foreground/40 ring-muted')}
          style={color ? { background: color, boxShadow: `0 0 0 4px ${color}33` } : undefined}
        />
      </span>
      <div className="text-lg font-semibold" style={color ? { color } : undefined} data-testid="status-label">
        {label}
      </div>
    </Shell>
  )
}

function Shell({ testState, subtitle, children }: { testState: string; subtitle: React.ReactNode; children: React.ReactNode }) {
  return (
    <div
      className="flex h-full flex-col items-center justify-center gap-2 text-center"
      data-testid="status-widget"
      data-state={testState}
    >
      {children}
      <div className="text-muted-foreground text-xs">{subtitle}</div>
    </div>
  )
}

function StatusWidget(props: WidgetRenderProps) {
  return str(props.options, 'source') === 'attribute' ? <AttributeStatus {...props} /> : <DeviceStatus {...props} />
}

export const statusWidget: WidgetDefinition = {
  type: 'status',
  label: 'Status',
  description: 'Device online/offline, or any attribute shown through value mappings',
  icon: Activity,
  defaultSize: { w: 2, h: 4 },
  minSize: { w: 2, h: 3 },
  fit: () => 'ok',
  defaultOptions: () => ({ source: 'device' }),
  fields: [
    {
      key: 'source',
      label: 'Show',
      kind: 'select',
      section: 'data',
      choices: [
        { value: 'device', label: 'Device online / offline' },
        { value: 'attribute', label: 'An attribute’s state' },
      ],
    },
    {
      key: 'field',
      label: 'Attribute',
      kind: 'attribute',
      section: 'data',
      placeholder: 'auto (value)',
      help: 'Used when “Show” is an attribute’s state.',
    },
    MAPPINGS_FIELD,
  ],
  Component: StatusWidget,
}
