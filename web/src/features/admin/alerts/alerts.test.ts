import { describe, expect, it } from 'vitest'
import { NEW_RULE, alertKeys, describeResolve, describeRule, ruleBody, ruleForm, type ElementAlertRule } from './api'

const rule: ElementAlertRule = {
  id: 'rule-xyz',
  element_id: 'elem-123',
  name: 'Out of range',
  field: 'climate.humidity',
  condition: 'outside_range',
  threshold: 15,
  threshold_max: 35,
  hysteresis: 1,
  severity: 'critical',
  message: 'Temperature is outside the normal range',
  enabled: false,
  firing: false,
  created_at: '2026-10-10T12:00:00Z',
  updated_at: '2026-10-10T12:00:00Z',
}

describe('alert rules', () => {
  it('keys by element', () => {
    expect(alertKeys.elementAlerts('elem-123')).toEqual(['admin', 'alerts', 'elem-123'])
  })

  it('an edit sends the rule id and keeps it disabled', () => {
    const { body } = ruleBody(ruleForm(rule))
    expect(body).toMatchObject({ id: 'rule-xyz', field: 'climate.humidity', threshold: 15, threshold_max: 35, hysteresis: 1, enabled: false })
  })

  it('refuses what the server refuses', () => {
    const f = { ...NEW_RULE, name: 'x', threshold: '10' }
    expect(ruleBody({ ...f, name: ' ' }).error).toMatch(/Name/)
    expect(ruleBody({ ...f, field: 'a b' }).error).toMatch(/attribute/)
    expect(ruleBody({ ...f, field: 'sensors[*].t' }).error).toMatch(/attribute/)
    expect(ruleBody({ ...f, field: ' ' }).body!.field).toBe('value') // empty is the element's value
    expect(ruleBody({ ...f, field: 'sensors[0].t' }).body!.field).toBe('sensors[0].t')
    expect(ruleBody({ ...f, threshold: '' }).error).toMatch(/threshold/)
    expect(ruleBody({ ...f, hysteresis: '-1' }).error).toMatch(/hysteresis/)
    const range = { ...f, condition: 'outside_range' as const }
    expect(ruleBody(range).error).toMatch(/maximum/)
    expect(ruleBody({ ...range, thresholdMax: '5' }).error).toMatch(/greater/)
    expect(ruleBody({ ...range, thresholdMax: '30', hysteresis: '10' }).error).toMatch(/half the range/)
    // threshold_max only goes with outside_range
    expect(ruleBody({ ...f, thresholdMax: '30' }).body!.threshold_max).toBeUndefined()
  })

  it('describes when a rule fires and resolves', () => {
    expect(describeRule(rule)).toBe('climate.humidity < 15 or > 35')
    expect(describeRule({ condition: 'above', threshold: 30 })).toBe('value > 30')
    expect(describeResolve(rule)).toBe('resolves within 16–34')
    expect(describeResolve({ condition: 'above', threshold: 30, hysteresis: 0.1 + 0.2 })).toBe('resolves at ≤ 29.7')
    expect(describeResolve({ condition: 'below', threshold: 5, hysteresis: 0 })).toBeNull()
  })
})
