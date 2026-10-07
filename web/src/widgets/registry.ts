/**
 * Widget registry. To add a widget type, implement a WidgetDefinition
 * (see ./types.ts and README → "Adding a widget type") and list it here.
 */
import type { MyElement } from '@/api/types'
import { widgetHint } from '@/api/types'
import { gaugeWidget } from './gauge/GaugeWidget'
import { lineChartWidget } from './line-chart/LineChartWidget'
import { sliderWidget } from './slider/SliderWidget'
import { statWidget } from './stat/StatWidget'
import { statusWidget } from './status/StatusWidget'
import { switchWidget } from './switch/SwitchWidget'
import type { Fit, WidgetDefinition } from './types'

export const WIDGETS: WidgetDefinition[] = [
  gaugeWidget,
  lineChartWidget,
  statWidget,
  switchWidget,
  sliderWidget,
  statusWidget,
]

const byType = new Map(WIDGETS.map((w) => [w.type, w]))

export function getWidget(type: string): WidgetDefinition | undefined {
  return byType.get(type)
}

/** The widget type to use by default for an element, from its `widget` style hint. */
export function defaultWidgetType(el: MyElement): string {
  switch (widgetHint(el)) {
    case 'sensor':
      return 'gauge'
    case 'chart':
      return 'line'
    case 'switch':
      return 'switch'
    case 'slider':
      return 'slider'
    default:
      return 'stat'
  }
}

/** Widget types sensible for an element, suggested first. */
export function widgetsFor(el: MyElement): { def: WidgetDefinition; fit: Fit }[] {
  const order: Record<Fit, number> = { suggested: 0, ok: 1, no: 2 }
  return WIDGETS.map((def) => ({ def, fit: def.fit(el) }))
    .filter((x) => x.fit !== 'no')
    .sort(
      (a, b) =>
        order[a.fit] - order[b.fit] ||
        (a.def.type === defaultWidgetType(el) ? -1 : b.def.type === defaultWidgetType(el) ? 1 : 0),
    )
}
