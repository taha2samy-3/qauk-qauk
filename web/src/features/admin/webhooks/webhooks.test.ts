import { describe, expect, it } from 'vitest'
import { webhookKeys, type WebhookEndpoint, type WebhookFormat } from './api'

describe('Webhooks API and Configuration', () => {
  it('generates consistent query keys', () => {
    expect(webhookKeys.all).toEqual(['admin', 'webhooks'])
    expect(webhookKeys.list()).toEqual(['admin', 'webhooks', 'list'])
    expect(webhookKeys.detail('ep-123')).toEqual(['admin', 'webhooks', { detail: 'ep-123' }])
    expect(webhookKeys.deliveries('ep-123')).toEqual(['admin', 'webhooks', { deliveries: 'ep-123' }])
  })

  it('validates supported webhook payload formats', () => {
    const formats: WebhookFormat[] = ['standard', 'slack', 'discord', 'teams', 'telegram', 'custom']
    expect(formats).toHaveLength(6)
    expect(formats).toContain('standard')
    expect(formats).toContain('slack')
    expect(formats).toContain('discord')
    expect(formats).toContain('teams')
    expect(formats).toContain('telegram')
    expect(formats).toContain('custom')
  })

  it('constructs well-formed webhook endpoint definition', () => {
    const ep: WebhookEndpoint = {
      id: 'wh-123',
      name: 'Ops Slack Channel',
      url: 'https://hooks.slack.com/services/T00/B00/X00',
      format: 'slack',
      secret: 'whsec_sample',
      severities: ['warning', 'critical'],
      headers: {},
      enabled: true,
      created_at: '2026-10-10T12:00:00Z',
      updated_at: '2026-10-10T12:00:00Z',
    }

    expect(ep.format).toBe('slack')
    expect(ep.severities).toContain('critical')
    expect(ep.enabled).toBe(true)
  })
})
