import type { ComponentType } from 'react'
import type { LucideIcon } from 'lucide-react'
import type { MyElement } from '@/api/types'
import type { UseElementResult } from '@/realtime/hooks'
import type { Widget } from '@/features/dashboards/layout'

export type Fit = 'suggested' | 'ok' | 'no'

/** Where a field is shown in the configuration sheet. */
export type FieldSection = 'data' | 'display'

export type OptionFieldBase = { section?: FieldSection }

export type OptionField = OptionFieldBase &
  (
    | { key: string; label: string; kind: 'text'; placeholder?: string; help?: string }
    | {
        key: string
        label: string
        kind: 'number'
        placeholder?: string
        help?: string
        min?: number
        max?: number
        step?: number
        integer?: boolean
      }
    | { key: string; label: string; kind: 'boolean'; help?: string }
    | {
        key: string
        label: string
        kind: 'select'
        choices: { value: string; label: string }[]
        help?: string
      }
    | { key: string; label: string; kind: 'thresholds'; help?: string }
    | { key: string; label: string; kind: 'color'; help?: string }
    /** an attribute path in the element's messages; `accepts` filters the suggestions */
    | {
        key: string
        label: string
        kind: 'attribute'
        help?: string
        accepts?: ('number' | 'boolean' | 'string' | 'time')[]
        placeholder?: string
        xAxis?: boolean
      }
    /** value → label/color table */
    | { key: string; label: string; kind: 'mappings'; help?: string }
    /** extra chart series from other elements */
    | { key: string; label: string; kind: 'series'; help?: string }
  )

export type Options = Record<string, unknown>

export interface WidgetRenderProps {
  widget: Widget
  element: MyElement
  options: Options
  rt: UseElementResult
  editing: boolean
  /** per-instance UI state shared by Component and Actions (e.g. selected time range) */
  local: Options
  setLocal: (patch: Options) => void
}

export interface WidgetDefinition {
  type: string
  label: string
  description: string
  icon: LucideIcon
  /** default and minimum size in grid units (lg, 12 columns) */
  defaultSize: { w: number; h: number }
  minSize: { w: number; h: number }
  /** how sensible this widget is for an element */
  fit: (el: MyElement) => Fit
  /** initial options for a new widget bound to `el` */
  defaultOptions: (el: MyElement) => Options
  /** fields shown in the configuration sheet */
  fields: OptionField[]
  /** widgets that send commands (need RC to interact) */
  control?: boolean
  Component: ComponentType<WidgetRenderProps>
  /** optional compact controls rendered in the widget header */
  Actions?: ComponentType<WidgetRenderProps>
}
