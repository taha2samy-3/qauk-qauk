import { describe, expect, it } from 'vitest'
import { formValues, webhookBody, webhookKeys, type WebhookEndpoint } from './api'

const endpoint: WebhookEndpoint = {
  id: 'wh-123',
  name: 'Ops Slack',
  url: 'https://hooks.slack.com/…X00',
  format: 'slack',
  severities: ['warning', 'critical'],
  header_names: ['Authorization'],
  options: {},
  secret_set: true,
  enabled: true,
  created_at: '2026-10-10T12:00:00Z',
  updated_at: '2026-10-10T12:00:00Z',
}

describe('webhook keys', () => {
  it('nest under one prefix, so invalidating it refreshes every list', () => {
    expect(webhookKeys.all).toEqual(['admin', 'webhooks'])
    expect(webhookKeys.list()).toEqual(['admin', 'webhooks', 'list'])
    expect(webhookKeys.deliveries('ep-123')).toEqual(['admin', 'webhooks', { deliveries: 'ep-123' }])
  })
})

describe('webhook form', () => {
  it('never fills in secret values, only references', () => {
    const v = formValues(endpoint)
    expect(v.url).toBe('') // the redacted URL is a placeholder, not a value
    expect(v.secret).toBe('')
    expect(v.headers).toEqual([{ name: 'Authorization', value: '', stored: true }])

    const ref = formValues({ ...endpoint, url: 'env:OPS_HOOK', secret_ref: 'file:/run/secrets/hook' })
    expect(ref.url).toBe('env:OPS_HOOK')
    expect(ref.secret).toBe('file:/run/secrets/hook')
  })

  it('an untouched edit keeps the URL, secret and stored header values', () => {
    const { body } = webhookBody(formValues(endpoint), true)
    expect(body).toBeDefined()
    expect(body!.url).toBeUndefined()
    expect(body!.secret).toBeUndefined()
    expect(body!.new_secret).toBeUndefined()
    expect(body!.headers).toEqual({ Authorization: '' }) // empty keeps the stored value
  })

  it('a removed header is left out of the map, so the server drops it', () => {
    const v = { ...formValues(endpoint), headers: [] }
    expect(webhookBody(v, true).body!.headers).toEqual({})
  })

  it('refuses what the server would refuse, before sending', () => {
    const base = formValues(null)
    expect(webhookBody({ ...base, name: 'x' }, false).error).toMatch(/URL/)
    expect(webhookBody({ ...base, name: 'x', url: 'https://a.example', format: 'telegram' }, false).error).toMatch(/chat ID/)
    expect(webhookBody({ ...base, name: 'x', url: 'https://a.example', severities: [] }, false).error).toMatch(/severity/)
    const newHeader = { name: 'X-Token', value: '', stored: false }
    expect(webhookBody({ ...base, name: 'x', url: 'https://a.example', headers: [newHeader] }, false).error).toMatch(
      /needs a value/,
    )
  })

  it('sends the telegram chat and a new secret request', () => {
    const v = { ...formValues(endpoint), format: 'telegram' as const, chatId: ' -100 ', newSecret: true, secret: 'whsec_x' }
    const { body } = webhookBody(v, true)
    expect(body!.options).toEqual({ chat_id: '-100' })
    expect(body!.new_secret).toBe(true)
    expect(body!.secret).toBeUndefined()
  })
})
