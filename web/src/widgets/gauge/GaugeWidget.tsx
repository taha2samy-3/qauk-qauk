import { Gauge } from 'lucide-react'
import { useMemo } from 'react'
import type { EChartsOption } from 'echarts'
import { widgetHint } from '@/api/types'
import { chartPalette, resolveColor } from '@/lib/chartTheme'
import { useTheme } from '@/lib/theme'
import { useSize } from '@/lib/useSize'
import { formatNumber } from '@/lib/utils'
import { EChart } from '../EChart'
import { colorFor, elementDefaults, num, str, thresholds } from '../options'
import type { WidgetDefinition, WidgetRenderProps } from '../types'

function withAlpha(hex: string, alpha: number): string {
  const m = /^#([0-9a-f]{6})$/i.exec(hex)
  if (!m) return hex
  const n = parseInt(m[1]!, 16)
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`
}

function GaugeWidget({ options, rt }: WidgetRenderProps) {
  const { resolved } = useTheme()
  const pal = chartPalette(resolved)
  const [ref, size] = useSize<HTMLDivElement>()
  const min = num(options, 'min', 0)!
  const max = Math.max(num(options, 'max', 100)!, min + 1)
  const unit = str(options, 'unit', '')
  const decimals = num(options, 'decimals')
  const base = resolveColor(str(options, 'baseColor', 'blue')!, resolved)
  const ts = thresholds(options).map((t) => ({ ...t, color: resolveColor(t.color, resolved) }))
  const value = rt.value
  const clamped = value === undefined ? min : Math.min(max, Math.max(min, value))
  const color = colorFor(value, ts, base)
  const dim = Math.min(size.width, size.height * 1.25)
  const font = Math.max(16, Math.min(44, dim / 6))

  const option = useMemo<EChartsOption>(() => {
    // background bands: base up to the first threshold, then each threshold's color
    const stops: [number, string][] = []
    let prev = base
    for (const t of ts) {
      const at = (t.value - min) / (max - min)
      if (at > 0 && at < 1) stops.push([at, withAlpha(prev, 0.28)])
      prev = t.color
    }
    stops.push([1, withAlpha(prev, 0.28)])
    const width = Math.max(8, Math.min(18, dim / 16))
    return {
      animationDurationUpdate: 400,
      series: [
        {
          type: 'gauge',
          min,
          max,
          startAngle: 215,
          endAngle: -35,
          radius: '92%',
          center: ['50%', '58%'],
          splitNumber: 4,
          axisLine: { lineStyle: { width, color: ts.length ? stops : [[1, pal.track]] } },
          progress: { show: true, width, itemStyle: { color }, roundCap: false },
          pointer: { show: false },
          anchor: { show: false },
          axisTick: { show: false },
          splitLine: { show: false },
          axisLabel: {
            distance: -width - 14,
            color: pal.muted,
            fontSize: 10,
            formatter: (v: number) => (v === min || v === max ? formatNumber(v) : ''),
          },
          title: { show: false },
          detail: {
            valueAnimation: true,
            offsetCenter: [0, '6%'],
            fontSize: font,
            fontWeight: 600,
            color: pal.text,
            formatter: () =>
              value === undefined ? '{u|—}' : `${formatNumber(value, decimals)}${unit ? `{u| ${unit}}` : ''}`,
            rich: {
              u: {
                fontSize: Math.max(11, font * 0.42),
                color: pal.muted,
                fontWeight: 500,
                padding: [0, 0, 4, 0],
              },
            },
          },
          data: [{ value: clamped }],
        },
      ],
    }
  }, [base, clamped, color, decimals, dim, font, max, min, pal, ts, unit, value])

  return (
    <div ref={ref} className="h-full w-full" data-testid="gauge" data-value={value ?? ''}>
      {size.width > 0 && <EChart option={option} />}
    </div>
  )
}

export const gaugeWidget: WidgetDefinition = {
  type: 'gauge',
  label: 'Gauge',
  description: 'Radial gauge with colored thresholds',
  icon: Gauge,
  defaultSize: { w: 3, h: 6 },
  minSize: { w: 2, h: 4 },
  fit: (el) => {
    const h = widgetHint(el)
    return h === 'sensor' ? 'suggested' : h === 'switch' ? 'no' : 'ok'
  },
  defaultOptions: (el) => {
    const d = elementDefaults(el)
    return { unit: d.unit, min: d.min ?? 0, max: d.max ?? 100, baseColor: 'blue', thresholds: [] }
  },
  fields: [
    { key: 'unit', label: 'Unit', kind: 'text', placeholder: '°C' },
    { key: 'min', label: 'Minimum', kind: 'number' },
    { key: 'max', label: 'Maximum', kind: 'number' },
    {
      key: 'decimals',
      label: 'Decimals',
      kind: 'number',
      min: 0,
      max: 6,
      integer: true,
      placeholder: 'auto',
    },
    { key: 'baseColor', label: 'Base color', kind: 'color' },
    {
      key: 'thresholds',
      label: 'Thresholds',
      kind: 'thresholds',
      help: 'From each value upwards the gauge uses the threshold color.',
    },
  ],
  Component: GaugeWidget,
}
