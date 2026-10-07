import { Loader2, SlidersHorizontal } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { widgetHint } from '@/api/types'
import { Slider } from '@/components/ui/slider'
import { formatNumber } from '@/lib/utils'
import { elementDefaults, num, str } from '../options'
import { usePendingCommand } from '../usePendingCommand'
import type { WidgetDefinition, WidgetRenderProps } from '../types'

const DEBOUNCE_MS = 300

function SliderWidget({ widget, element, options, rt, editing }: WidgetRenderProps) {
  const title = widget.title || element.name
  const min = num(options, 'min', 0)!
  const max = Math.max(num(options, 'max', 100)!, min + 1)
  const step = num(options, 'step', 1)!
  const unit = str(options, 'unit', '')
  const { pending, command } = usePendingCommand(element.id, rt, title)
  const [draft, setDraft] = useState<number | undefined>(undefined)
  const debounce = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(debounce.current), [])

  const canControl = rt.permission === 'RC' && rt.status === 'subscribed' && rt.deviceConnected !== false
  const shown = draft ?? pending ?? rt.value ?? min

  const schedule = (v: number) => {
    clearTimeout(debounce.current)
    debounce.current = setTimeout(() => {
      command(v)
      setDraft(undefined)
    }, DEBOUNCE_MS)
  }

  return (
    <div
      className="flex h-full flex-col justify-center gap-4 px-1"
      data-testid="slider-widget"
      data-value={rt.value ?? ''}
    >
      <div className="flex items-baseline justify-between gap-2">
        <div className="tabular flex items-baseline gap-1 text-3xl font-semibold tracking-tight">
          {formatNumber(shown)}
          {unit && <span className="text-muted-foreground text-base font-medium">{unit}</span>}
        </div>
        {pending !== undefined && draft === undefined && (
          <span className="text-muted-foreground flex items-center gap-1 text-xs">
            <Loader2 className="size-3 animate-spin" /> Applying…
          </span>
        )}
        {pending === undefined && rt.value !== undefined && draft !== undefined && (
          <span className="text-muted-foreground tabular text-xs">device: {formatNumber(rt.value)}</span>
        )}
      </div>
      <Slider
        min={min}
        max={max}
        step={step}
        value={[Math.min(max, Math.max(min, shown))]}
        disabled={!canControl || editing}
        aria-label={title}
        onValueChange={([v]) => {
          if (v === undefined) return
          setDraft(v)
          schedule(v)
        }}
      />
      <div className="text-muted-foreground tabular flex justify-between text-[11px]">
        <span>
          {formatNumber(min)} {unit}
        </span>
        {!canControl && !editing && (
          <span>
            {rt.permission !== 'RC'
              ? 'Read-only'
              : rt.deviceConnected === false
                ? 'Device offline'
                : 'Connecting…'}
          </span>
        )}
        <span>
          {formatNumber(max)} {unit}
        </span>
      </div>
    </div>
  )
}

export const sliderWidget: WidgetDefinition = {
  type: 'slider',
  label: 'Slider',
  description: 'Set a numeric value on the device',
  icon: SlidersHorizontal,
  defaultSize: { w: 4, h: 4 },
  minSize: { w: 3, h: 3 },
  control: true,
  fit: (el) => {
    const h = widgetHint(el)
    return h === 'slider' ? 'suggested' : h === undefined ? 'ok' : 'no'
  },
  defaultOptions: (el) => {
    const d = elementDefaults(el)
    return { min: d.min ?? 0, max: d.max ?? 100, step: d.step ?? 1, unit: d.unit }
  },
  fields: [
    { key: 'min', label: 'Minimum', kind: 'number' },
    { key: 'max', label: 'Maximum', kind: 'number' },
    { key: 'step', label: 'Step', kind: 'number', min: 0 },
    { key: 'unit', label: 'Unit', kind: 'text' },
  ],
  Component: SliderWidget,
}
