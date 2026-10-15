import { useQuery } from '@tanstack/react-query'
import { api, unwrap } from '@/api/client'
import { qk } from '@/api/queries'
import type { Schemas } from '@/api/types'

export type WebhookFormat = 'standard' | 'slack' | 'discord' | 'teams' | 'telegram' | 'custom'
export type WebhookSeverity = 'info' | 'warning' | 'critical'
export const SEVERITIES: WebhookSeverity[] = ['info', 'warning', 'critical']

/** An endpoint as the API shows it: the URL redacted (or its reference), header names only, no secret. */
export type WebhookEndpoint = Schemas['WebhookView']
/** What an admin sends. On update, absent fields and an empty url or secret keep the stored value. */
export type WebhookInput = Schemas['WebhookBody']
/** A created or updated endpoint, with the signing secret when one was just generated (shown once). */
export type WebhookSaved = Schemas['WebhookCreatedOutBody']
export type WebhookDelivery = Schemas['WebhookDelivery']
export type WebhookTestResult = Schemas['WebhookTestOutBody']

export const webhookKeys = {
  all: qk.admin('webhooks'),
  list: () => qk.admin('webhooks', 'list'),
  detail: (id: string) => qk.admin('webhooks', { detail: id }),
  deliveries: (id: string) => qk.admin('webhooks', { deliveries: id }),
}

export const webhookApi = {
  list: () => unwrap(api.GET('/api/v1/admin/webhooks')).then((d) => d ?? []),
  get: (id: string) => unwrap(api.GET('/api/v1/admin/webhooks/{id}', { params: { path: { id } } })),
  create: (body: WebhookInput) => unwrap(api.POST('/api/v1/admin/webhooks', { body })),
  update: ({ id, ...body }: WebhookInput & { id: string }) =>
    unwrap(api.PUT('/api/v1/admin/webhooks/{id}', { params: { path: { id } }, body })),
  delete: (id: string) => unwrap(api.DELETE('/api/v1/admin/webhooks/{id}', { params: { path: { id } } })),
  test: ({ id, severity }: { id: string; severity?: WebhookSeverity }) =>
    unwrap(
      api.POST('/api/v1/admin/webhooks/{id}/test', {
        params: { path: { id } },
        body: { severity: severity ?? 'info' },
      }),
    ),
  deliveries: ({ id, limit = 50 }: { id: string; limit?: number }) =>
    unwrap(
      api.GET('/api/v1/admin/webhooks/{id}/deliveries', { params: { path: { id }, query: { limit } } }),
    ).then((d) => d ?? []),
}

/** One header row in the form; on an existing endpoint an empty value keeps the stored one. */
export interface HeaderRow {
  name: string
  value: string
  /** The endpoint already has this header (its value is never shown). */
  stored: boolean
}

export interface WebhookFormValues {
  name: string
  url: string
  format: WebhookFormat
  severities: WebhookSeverity[]
  secret: string
  newSecret: boolean
  headers: HeaderRow[]
  customTemplate: string
  chatId: string
  enabled: boolean
}

const isRef = (v: string) => v.startsWith('env:') || v.startsWith('file:')

/** The form's starting values: secret values are never filled in, references are. */
export function formValues(ep: WebhookEndpoint | null): WebhookFormValues {
  return {
    name: ep?.name ?? '',
    url: ep && isRef(ep.url) ? ep.url : '',
    format: (ep?.format as WebhookFormat | undefined) ?? 'standard',
    severities: (ep?.severities as WebhookSeverity[] | null | undefined) ?? [...SEVERITIES],
    secret: ep?.secret_ref ?? '',
    newSecret: false,
    headers: (ep?.header_names ?? []).map((name) => ({ name, value: '', stored: true })),
    customTemplate: ep?.custom_template ?? '',
    chatId: ep?.options.chat_id ?? '',
    enabled: ep?.enabled ?? true,
  }
}

/** The request body for the form, or the first problem to fix. */
export function webhookBody(
  v: WebhookFormValues,
  editing: boolean,
): { body: WebhookInput; error?: undefined } | { body?: undefined; error: string } {
  if (!v.name.trim()) return { error: 'Name is required' }
  if (!editing && !v.url.trim()) return { error: 'URL is required' }
  if (v.severities.length === 0) return { error: 'Pick at least one severity' }
  if (v.format === 'telegram' && !v.chatId.trim()) return { error: 'Telegram needs a chat ID' }
  if (v.format === 'custom' && !v.customTemplate.trim()) return { error: 'The custom format needs a template' }
  const headers: Record<string, string> = {}
  for (const h of v.headers) {
    const name = h.name.trim()
    if (!name) continue
    if (name in headers) return { error: `Header ${name} is listed twice` }
    if (!h.value && !h.stored) return { error: `Header ${name} needs a value` }
    headers[name] = h.value
  }
  const body: WebhookInput = {
    name: v.name.trim(),
    format: v.format,
    severities: v.severities,
    headers,
    enabled: v.enabled,
    options: v.format === 'telegram' ? { chat_id: v.chatId.trim() } : {},
    custom_template: v.format === 'custom' ? v.customTemplate : '',
  }
  if (v.url.trim()) body.url = v.url.trim()
  if (v.newSecret) body.new_secret = true
  else if (v.secret.trim()) body.secret = v.secret.trim()
  return { body }
}

export const useWebhooks = () =>
  useQuery({
    queryKey: webhookKeys.list(),
    queryFn: webhookApi.list,
  })

export const useWebhookDeliveries = (id: string | null) =>
  useQuery({
    queryKey: webhookKeys.deliveries(id ?? ''),
    queryFn: () => (id ? webhookApi.deliveries({ id }) : Promise.resolve([])),
    enabled: !!id,
    refetchInterval: 5000,
  })
