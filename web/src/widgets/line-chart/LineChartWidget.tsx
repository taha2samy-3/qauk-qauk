import { LineChart as LineIcon } from 'lucide-react'
import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import type { EChartsOption } from 'echarts'
import { fetchHistory, isRangeKey, RANGE_KEYS, RANGES, type RangeKey } from '@/api/history'
import { qk } from '@/api/queries'
import { widgetHint } from '@/api/types'
import { chartPalette, resolveColor } from '@/lib/chartTheme'
import { useTheme } from '@/lib/theme'
import { formatNumber } from '@/lib/utils'
import { useNow } from '@/lib/useNow'
import { messageToPoint, type Point } from '@/realtime/messages'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { Spinner } from '@/components/ui/spinner'
import { EChart } from '../EChart'
import { bool, elementDefaults, num, str, thresholds } from '../options'
import type { WidgetDefinition, WidgetRenderProps } from '../types'

function currentRange({ local, options }: Pick<WidgetRenderProps, 'local' | 'options'>): RangeKey {
  if (isRangeKey(local.range)) return local.range
  return isRangeKey(options.range) ? options.range : '1h'
}

function LineChartWidget({ element, options, rt, local }: WidgetRenderProps) {
  const { resolved } = useTheme()
  const pal = chartPalette(resolved)
  const range = currentRange({ local, options })
  const unit = str(options, 'unit', '')
  const decimals = num(options, 'decimals')
  const color = resolveColor(str(options, 'color', 'blue')!, resolved)
  const area = bool(options, 'area', true)
  const yMin = num(options, 'min')
  const yMax = num(options, 'max')
  const ts = thresholds(options)

  const history = useQuery({
    queryKey: qk.history(element.id, range),
    queryFn: () => fetchHistory(element.id, range),
    staleTime: 60_000,
    refetchOnWindowFocus: false,
  })

  const now = useNow()
  const points = useMemo<Point[]>(() => {
    const base = history.data?.points ?? []
    const lastT = base.length ? base[base.length - 1]![0] : -Infinity
    const live: Point[] = []
    for (const f of rt.history) {
      const p = messageToPoint(f.message, f.at)
      if (p && p[0] > lastT) live.push(p)
    }
    const all = base.concat(live)
    const newest = all.length ? all[all.length - 1]![0] : now
    const cutoff = Math.max(now, newest) - RANGES[range].ms
    return all.filter((p) => p[0] >= cutoff)
  }, [history.data, rt.history, range, now])

  const option = useMemo<EChartsOption>(
    () => ({
      animation: false,
      grid: { left: 8, right: 12, top: 12, bottom: 4, containLabel: true },
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
      series: [
        {
          type: 'line',
          name: element.name,
          data: points,
          showSymbol: false,
          smooth: false,
          sampling: 'lttb',
          lineStyle: { width: 1.75, color },
          itemStyle: { color },
          areaStyle: area
            ? {
                color: {
                  type: 'linear',
                  x: 0,
                  y: 0,
                  x2: 0,
                  y2: 1,
                  colorStops: [
                    { offset: 0, color: `${color}55` },
                    { offset: 1, color: `${color}00` },
                  ],
                },
              }
            : undefined,
          markLine: ts.length
            ? {
                silent: true,
                symbol: 'none',
                label: { show: false },
                data: ts.map((t) => ({
                  yAxis: t.value,
                  lineStyle: { color: resolveColor(t.color, resolved), type: 'dashed', width: 1 },
                })),
              }
            : undefined,
        },
      ],
    }),
    [area, color, decimals, element.name, pal, points, resolved, ts, unit, yMax, yMin],
  )

  return (
    <div className="relative h-full w-full" data-testid="line-chart" data-points={points.length}>
      <EChart option={option} />
      {points.length === 0 && (
        <div className="text-muted-foreground absolute inset-0 flex items-center justify-center text-xs">
          {history.isLoading ? (
            <Spinner />
          ) : history.isError ? (
            'Could not load history'
          ) : (
            `No data in the last ${range}`
          )}
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
