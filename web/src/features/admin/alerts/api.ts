import { useQuery } from '@tanstack/react-query'
import { api, unwrap } from '@/api/client'
import { qk } from '@/api/queries'
import type { Schemas } from '@/api/types'

export type AlertCondition = 'above' | 'below' | 'outside_range' | 'equals'
export type AlertSeverity = 'info' | 'warning' | 'critical'

/** A rule with whether it is firing now. */
export type ElementAlertRule = Schemas['AlertRuleView']
export type AlertRuleBody = Schemas['ElementAlertSaveInBody']

export const alertKeys = {
  elementAlerts: (elementId: string) => qk.admin('alerts', elementId),
}

export const alertApi = {
  list: (elementId: string) =>
    unwrap(api.GET('/api/v1/admin/elements/{id}/alerts', { params: { path: { id: elementId } } })).then(
      (d) => d ?? [],
    ),
  /** Creates a rule, or changes the one named by id. */
  save: ({ elementId, ...body }: AlertRuleBody & { elementId: string }) =>
    unwrap(api.POST('/api/v1/admin/elements/{id}/alerts', { params: { path: { id: elementId } }, body })),
  delete: ({ elementId, ruleId }: { elementId: string; ruleId: string }) =>
    unwrap(
      api.DELETE('/api/v1/admin/elements/{id}/alerts/{ruleId}', {
        params: { path: { id: elementId, ruleId } },
      }),
    ),
}

/** The rule form: numbers as typed. */
export interface AlertRuleForm {
  id?: string
  name: string
  condition: AlertCondition
  threshold: string
  thresholdMax: string
  hysteresis: string
  severity: AlertSeverity
  message: string
  enabled: boolean
}

export const NEW_RULE: AlertRuleForm = {
  name: '',
  condition: 'above',
  threshold: '',
  thresholdMax: '',
  hysteresis: '0',
  severity: 'warning',
  message: '',
  enabled: true,
}

export function ruleForm(r: ElementAlertRule): AlertRuleForm {
  return {
    id: r.id,
    name: r.name,
    condition: r.condition as AlertCondition,
    threshold: String(r.threshold),
    thresholdMax: r.threshold_max === undefined ? '' : String(r.threshold_max),
    hysteresis: String(r.hysteresis),
    severity: r.severity as AlertSeverity,
    message: r.message,
    enabled: r.enabled,
  }
}

const number = (t: string) => (t.trim() === '' ? NaN : Number(t))

/** The request body, or the first problem; the same checks the server makes. */
export function ruleBody(f: AlertRuleForm): { body: AlertRuleBody; error?: undefined } | { body?: undefined; error: string } {
  const threshold = number(f.threshold)
  const hysteresis = f.hysteresis.trim() === '' ? 0 : Number(f.hysteresis)
  if (!f.name.trim()) return { error: 'Name is required' }
  if (!Number.isFinite(threshold)) return { error: 'The threshold must be a number' }
  if (!Number.isFinite(hysteresis) || hysteresis < 0) return { error: 'The hysteresis must be a number ≥ 0' }
  const body: AlertRuleBody = {
    name: f.name.trim(),
    condition: f.condition,
    threshold,
    hysteresis,
    severity: f.severity,
    message: f.message.trim(),
    enabled: f.enabled,
  }
  if (f.id) body.id = f.id
  if (f.condition === 'outside_range') {
    const max = number(f.thresholdMax)
    if (!Number.isFinite(max)) return { error: 'The maximum must be a number' }
    if (max <= threshold) return { error: 'The maximum must be greater than the minimum' }
    if (hysteresis >= (max - threshold) / 2) {
      return { error: 'The hysteresis must be less than half the range, or the alert could never clear' }
    }
    body.threshold_max = max
  }
  return { body }
}

/** "> 30", "< 10 or > 30"… */
export function describeRule(r: { condition: string; threshold: number; threshold_max?: number }): string {
  switch (r.condition) {
    case 'above':
      return `value > ${r.threshold}`
    case 'below':
      return `value < ${r.threshold}`
    case 'outside_range':
      return `value < ${r.threshold} or > ${r.threshold_max ?? '—'}`
    case 'equals':
      return `value = ${r.threshold}`
    default:
      return r.condition
  }
}

const fmt = (n: number) => String(Number(n.toPrecision(12)))

/** When a firing rule resolves, given its hysteresis. */
export function describeResolve(r: { condition: string; threshold: number; threshold_max?: number; hysteresis: number }) {
  const h = r.hysteresis
  if (h <= 0) return null
  switch (r.condition) {
    case 'above':
      return `resolves at ≤ ${fmt(r.threshold - h)}`
    case 'below':
      return `resolves at ≥ ${fmt(r.threshold + h)}`
    case 'outside_range':
      return `resolves within ${fmt(r.threshold + h)}–${fmt((r.threshold_max ?? 0) - h)}`
    case 'equals':
      return `resolves beyond ±${h}`
    default:
      return null
  }
}

export const useElementAlerts = (elementId: string | undefined) =>
  useQuery({
    queryKey: alertKeys.elementAlerts(elementId ?? ''),
    queryFn: () => (elementId ? alertApi.list(elementId) : Promise.resolve([])),
    enabled: !!elementId,
    refetchInterval: 10_000,
  })
