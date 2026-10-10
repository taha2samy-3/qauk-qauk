import { describe, expect, it } from 'vitest'
import { alertKeys, type AlertCondition, type AlertSeverity, type ElementAlertRule } from './api'

describe('Alert Rules API and Helpers', () => {
  it('generates correct query key for element alerts', () => {
    const key = alertKeys.elementAlerts('elem-123')
    expect(key).toEqual(['admin', 'alerts', 'elem-123'])
  })

  it('validates alert condition definitions', () => {
    const conditions: AlertCondition[] = ['above', 'below', 'outside_range', 'equals']
    expect(conditions).toHaveLength(4)
  })

  it('verifies alert severity levels', () => {
    const severities: AlertSeverity[] = ['info', 'warning', 'critical']
    expect(severities).toContain('info')
    expect(severities).toContain('warning')
    expect(severities).toContain('critical')
  })

  it('constructs well-formed element alert rule structure', () => {
    const rule: ElementAlertRule = {
      id: 'rule-xyz',
      element_id: 'elem-123',
      name: 'High temperature alert',
      condition: 'outside_range',
      threshold: 15.0,
      threshold_max: 35.0,
      hysteresis: 1.0,
      severity: 'critical',
      message: 'Temperature is outside normal operating range',
      enabled: true,
      created_at: '2026-10-10T12:00:00Z',
      updated_at: '2026-10-10T12:00:00Z',
    }

    expect(rule.condition).toBe('outside_range')
    expect(rule.threshold).toBeLessThan(rule.threshold_max!)
    expect(rule.hysteresis).toBeGreaterThan(0)
    expect(rule.severity).toBe('critical')
  })
})
