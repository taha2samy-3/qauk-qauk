import { ArrowDownRight, ArrowUpRight, Hash, Minus } from 'lucide-react'
import { widgetHint } from '@/api/types'
import { resolveColor } from '@/lib/chartTheme'
import { useTheme } from '@/lib/theme'
import { useSize } from '@/lib/useSize'
import { cn, formatNumber } from '@/lib/utils'
import { readNumber, readRaw } from '@/realtime/messages'
import { mapValue, readMappings } from '@/lib/valueMap'
import { Sparkline } from '../Sparkline'
import {
  binding,
  bool,
  colorFor,
  elementDefaults,
  MAPPINGS_FIELD,
  NUMERIC_BINDING_FIELDS,
  num,
  str,
  thresholds,
} from '../options'
import type { WidgetDefinition, WidgetRenderProps } from '../types'

function StatWidget({ options, rt }: WidgetRenderProps) {
  const { resolved } = useTheme()
  const [ref, size] = useSize<HTMLDivElement>()
  const unit = str(options, 'unit', '')
  const decimals = num(options, 'decimals')
  const showSpark = bool(options, 'sparkline', true)
  const ts = thresholds(options).map((t) => ({ ...t, color: resolveColor(t.color, resolved) }))
  const sparkColor = resolveColor(str(options, 'color', 'blue')!, resolved)
  const b = binding(options)
  const value = readNumber(rt.message, b.field, b)
  const mapped = mapValue(readRaw(rt.message, b.field), readMappings(options.mappings))
  const valueColor = mapped?.color
    ? resolveColor(mapped.color, resolved)
    : ts.length
      ? colorFor(value, ts, 'inherit')
      : 'inherit'

  // a mapping wins; a non-numeric attribute is shown as text
  const raw = readRaw(rt.message, b.field)
  const textual = !!mapped || (value === undefined && typeof raw === 'string' && raw !== '')
  const display = mapped ? mapped.label : textual ? String(raw) : formatNumber(value, decimals)

  const series = rt.history
    .slice(-60)
    .map((f) => readNumber(f.message, b.field, b))
    .filter((v): v is number => v !== undefined)
  const first = series[0]
  const delta = value !== undefined && first !== undefined ? value - first : undefined
  const fontSize = Math.max(
    22,
    Math.min(64, Math.min(size.width / 4.5, size.height / (showSpark ? 2.6 : 1.8))),
  )
  const Trend =
    delta === undefined || Math.abs(delta) < 1e-9 ? Minus : delta > 0 ? ArrowUpRight : ArrowDownRight

  return (
    <div
      ref={ref}
      className="flex h-full flex-col justify-between gap-1"
      data-testid="stat"
      data-value={value ?? ''}
    >
      <div className="flex flex-1 flex-col justify-center">
        <div
          className="tabular flex items-baseline gap-1.5 leading-none font-semibold tracking-tight"
          style={{ color: valueColor }}
        >
          <span style={{ fontSize }} data-testid="stat-text">
            {display}
          </span>
          {unit && !textual && (
            <span className="text-muted-foreground" style={{ fontSize: Math.max(12, fontSize * 0.38) }}>
              {unit}
            </span>
          )}
        </div>
        {delta !== undefined && series.length > 1 && (
          <div className={cn('text-muted-foreground tabular mt-1.5 flex items-center gap-1 text-xs')}>
            <Trend className="size-3.5" />
            {delta > 0 ? '+' : ''}
            {formatNumber(delta, decimals ?? 2)} {unit}
            <span className="opacity-70">· last {series.length} readings</span>
          </div>
        )}
      </div>
      {showSpark && (
        <Sparkline values={series} color={sparkColor} className="h-[30%] max-h-16 min-h-6 w-full" />
      )}
    </div>
  )
}

export const statWidget: WidgetDefinition = {
  type: 'stat',
  label: 'Stat',
  description: 'Big number with unit and a trend sparkline',
  icon: Hash,
  defaultSize: { w: 3, h: 4 },
  minSize: { w: 2, h: 3 },
  fit: (el) => (widgetHint(el) === 'sensor' ? 'suggested' : 'ok'),
  defaultOptions: (el) => ({
    unit: elementDefaults(el).unit,
    sparkline: true,
    color: 'blue',
    thresholds: [],
  }),
  fields: [
    ...NUMERIC_BINDING_FIELDS,
    MAPPINGS_FIELD,
    { key: 'unit', label: 'Unit', kind: 'text' },
    {
      key: 'decimals',
      label: 'Decimals',
      kind: 'number',
      min: 0,
      max: 6,
      integer: true,
      placeholder: 'auto',
    },
    { key: 'sparkline', label: 'Show sparkline', kind: 'boolean' },
    { key: 'color', label: 'Sparkline color', kind: 'color' },
    { key: 'thresholds', label: 'Value color thresholds', kind: 'thresholds' },
  ],
  Component: StatWidget,
}
