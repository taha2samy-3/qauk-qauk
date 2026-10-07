import { LineChart as LineIcon } from 'lucide-react'
import { useQueries } from '@tanstack/react-query'
import type { EChartsOption } from 'echarts'
import { fetchHistory, isRangeKey, RANGE_KEYS, RANGES, type RangeKey } from '@/api/history'
import { qk, useMyElements } from '@/api/queries'
import { widgetHint } from '@/api/types'
import { chartPalette, resolveColor } from '@/lib/chartTheme'
import { useTheme } from '@/lib/theme'
import { formatNumber } from '@/lib/utils'
import { useNow } from '@/lib/useNow'
import { bindPoint, type Point, type PointOptions } from '@/realtime/messages'
import { useElementStates } from '@/realtime/hooks'
import type { Frame } from '@/realtime/client'
import { isRecord } from '@/lib/utils'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { Spinner } from '@/components/ui/spinner'
import { EChart } from '../EChart'
import { binding, bool, elementDefaults, num, str, thresholds } from '../options'
import type { WidgetDefinition, WidgetRenderProps } from '../types'

function currentRange({ local, options }: Pick<WidgetRenderProps, 'local' | 'options'>): RangeKey {
  if (isRangeKey(local.range)) return local.range
  return isRangeKey(options.range) ? options.range : '1h'
}

interface SeriesSpec {
  elementId: string
  name: string
  color: string
  o: PointOptions
}

/** Extra series stored in options.series: [{element_id, field, label, color}] */
function readExtraSeries(
  raw: unknown,
): { element_id: string; field?: string; label?: string; color?: string }[] {
  if (!Array.isArray(raw)) return []
  return raw
    .filter(isRecord)
    .filter((r) => typeof r.element_id === 'string' && r.element_id !== '')
    .map((r) => ({
      element_id: r.element_id as string,
      field: typeof r.field === 'string' && r.field ? r.field : undefined,
      label: typeof r.label === 'string' && r.label ? r.label : undefined,
      color: typeof r.color === 'string' && r.color ? r.color : undefined,
    }))
}

const EXTRA_COLORS = ['orange', 'green', 'purple', 'red', 'yellow', 'blue']

/** History backfill + live frames newer than it, cut to the visible window. */
function mergePoints(
  base: Point[],
  frames: readonly Frame[],
  o: PointOptions,
  windowMs: number,
  now: number,
): Point[] {
  const lastT = base.length ? base[base.length - 1]![0] : -Infinity
  const live: Point[] = []
  for (const f of frames) {
    const p = bindPoint(f.message, f.at, o)
    if (p && p[0] > lastT) live.push(p)
  }
  const all = base.concat(live.sort((a, b) => a[0] - b[0]))
  const newest = all.length ? all[all.length - 1]![0] : now
  const cutoff = Math.max(now, newest) - windowMs
  return all.filter((p) => p[0] >= cutoff)
}

function LineChartWidget({ widget, element, options, rt, local }: WidgetRenderProps) {
  const { resolved } = useTheme()
  const pal = chartPalette(resolved)
  const range = currentRange({ local, options })
  const unit = str(options, 'unit', '')
  const decimals = num(options, 'decimals')
  const area = bool(options, 'area', true)
  const yMin = num(options, 'min')
  const yMax = num(options, 'max')
  const ts = thresholds(options)
  const { data: myElements } = useMyElements()

  const b = binding(options)
  const xField = str(options, 'xField')?.trim() || undefined
  const extras = readExtraSeries(options.series)
  const specs: SeriesSpec[] = [
    {
      elementId: element.id,
      name: str(options, 'label')?.trim() || widget.title || element.name,
      color: resolveColor(str(options, 'color', 'blue')!, resolved),
      o: { field: b.field, xField, scale: b.scale, offset: b.offset },
    },
    ...extras.map((x, i) => ({
      elementId: x.element_id,
      name: x.label || myElements?.find((e) => e.id === x.element_id)?.name || `Series ${i + 2}`,
      color: resolveColor(x.color ?? EXTRA_COLORS[i % EXTRA_COLORS.length]!, resolved),
      o: { field: x.field, xField },
    })),
  ]

  const histories = useQueries({
    queries: specs.map((sp) => ({
      queryKey: qk.history(sp.elementId, range, JSON.stringify(sp.o)),
      queryFn: () => fetchHistory(sp.elementId, range, sp.o),
      staleTime: 60_000,
      refetchOnWindowFocus: false,
    })),
  })
  const extraStates = useElementStates(extras.map((x) => x.element_id))

  const now = useNow()
  const windowMs = RANGES[range].ms
  const seriesPoints: Point[][] = specs.map((sp, i) =>
    mergePoints(
      histories[i]?.data?.points ?? [],
      i === 0 ? rt.history : (extraStates[i - 1]?.history ?? []),
      sp.o,
      windowMs,
      now,
    ),
  )
  const total = seriesPoints.reduce((n, p) => n + p.length, 0)
  const multi = specs.length > 1
  const loading = histories.some((h) => h.isLoading)
  const failed = histories.every((h) => h.isError)

  const option: EChartsOption = {
    animation: false,
    grid: { left: 8, right: 12, top: multi ? 30 : 12, bottom: 4, containLabel: true },
    legend: multi
      ? {
          show: true,
          top: 0,
          left: 0,
          icon: 'roundRect',
          itemWidth: 10,
          itemHeight: 4,
          textStyle: { color: pal.muted, fontSize: 11 },
        }
      : { show: false },
    tooltip: {
      trigger: 'axis',
      backgroundColor: pal.tooltipBg,
      borderColor: pal.tooltipBorder,
      textStyle: { color: pal.text, fontSize: 12 },
      valueFormatter: (v) => `${formatNumber(Number(v), decimals)}${unit ? ` ${unit}` : ''}`,
    },
    xAxis: {
      type: 'time',
      axisLine: { lineStyle: { color: pal.axis } },
      axisTick: { show: false },
      axisLabel: { color: pal.muted, fontSize: 10, hideOverlap: true },
      splitLine: { show: false },
    },
    yAxis: {
      type: 'value',
      scale: yMin === undefined && yMax === undefined,
      min: yMin,
      max: yMax,
      axisLabel: { color: pal.muted, fontSize: 10, formatter: (v: number) => formatNumber(v) },
      splitLine: { lineStyle: { color: pal.grid } },
    },
    series: specs.map((sp, i) => ({
      type: 'line' as const,
      name: sp.name,
      data: seriesPoints[i],
      showSymbol: false,
      smooth: false,
      sampling: 'lttb' as const,
      lineStyle: { width: 1.75, color: sp.color },
      itemStyle: { color: sp.color },
      // fill only a single series: stacked fills hide each other
      areaStyle:
        area && !multi
          ? {
              color: {
                type: 'linear' as const,
                x: 0,
                y: 0,
                x2: 0,
                y2: 1,
                colorStops: [
                  { offset: 0, color: `${sp.color}55` },
                  { offset: 1, color: `${sp.color}00` },
                ],
              },
            }
          : undefined,
      markLine:
        i === 0 && ts.length
          ? {
              silent: true,
              symbol: 'none' as const,
              label: { show: false },
              data: ts.map((t) => ({
                yAxis: t.value,
                lineStyle: { color: resolveColor(t.color, resolved), type: 'dashed' as const, width: 1 },
              })),
            }
          : undefined,
    })),
  }

  return (
    <div
      className="relative h-full w-full"
      data-testid="line-chart"
      data-points={total}
      data-series={specs.length}
    >
      <EChart option={option} />
      {total === 0 && (
        <div className="text-muted-foreground absolute inset-0 flex items-center justify-center text-xs">
          {loading ? <Spinner /> : failed ? 'Could not load history' : `No data in the last ${range}`}
        </div>
      )}
    </div>
  )
}

function RangeActions({ local, options, setLocal }: WidgetRenderProps) {
  const range = currentRange({ local, options })
  return (
    <ToggleGroup
      type="single"
      value={range}
      aria-label="Time range"
      onValueChange={(v) => v && setLocal({ range: v })}
      className="hidden @[22rem]:inline-flex"
    >
      {RANGE_KEYS.map((k) => (
        <ToggleGroupItem key={k} value={k} aria-label={`Last ${k}`}>
          {RANGES[k].label}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  )
}

export const lineChartWidget: WidgetDefinition = {
  type: 'line',
  label: 'Line chart',
  description: 'History with live updates and a time range selector',
  icon: LineIcon,
  defaultSize: { w: 6, h: 7 },
  minSize: { w: 3, h: 4 },
  fit: (el) => {
    const h = widgetHint(el)
    return h === 'chart' ? 'suggested' : 'ok'
  },
  defaultOptions: (el) => {
    const d = elementDefaults(el)
    return { unit: d.unit, range: '1h', color: 'blue', area: true, thresholds: [] }
  },
  fields: [
    {
      key: 'field',
      label: 'Attribute (Y)',
      kind: 'attribute',
      section: 'data',
      accepts: ['number'],
      placeholder: 'auto (value, or y)',
      help: 'The attribute plotted on the Y axis.',
    },
    {
      key: 'xField',
      label: 'Time (X)',
      kind: 'attribute',
      section: 'data',
      accepts: ['time'],
      xAxis: true,
      placeholder: 'auto (message x, else receive time)',
      help: 'A time attribute in the message (ISO or epoch), or the time the server received it. Aggregated ranges (6h and longer) always use the receive time.',
    },
    { key: 'scale', label: 'Multiply by', kind: 'number', section: 'data', placeholder: '1' },
    { key: 'offset', label: 'Then add', kind: 'number', section: 'data', placeholder: '0' },
    { key: 'label', label: 'Name in legend', kind: 'text', section: 'data', placeholder: 'widget title' },
    {
      key: 'series',
      label: 'More series',
      kind: 'series',
      section: 'data',
      help: 'Plot other elements (or other attributes of this one) on the same chart.',
    },
    { key: 'unit', label: 'Unit', kind: 'text' },
    {
      key: 'range',
      label: 'Default time range',
      kind: 'select',
      choices: RANGE_KEYS.map((k) => ({ value: k, label: `Last ${k}` })),
    },
    { key: 'min', label: 'Y min', kind: 'number', placeholder: 'auto' },
    { key: 'max', label: 'Y max', kind: 'number', placeholder: 'auto' },
    {
      key: 'decimals',
      label: 'Decimals',
      kind: 'number',
      min: 0,
      max: 6,
      integer: true,
      placeholder: 'auto',
    },
    { key: 'color', label: 'Line color', kind: 'color' },
    { key: 'area', label: 'Fill area', kind: 'boolean' },
    { key: 'thresholds', label: 'Threshold lines', kind: 'thresholds' },
  ],
  Component: LineChartWidget,
  Actions: RangeActions,
}
