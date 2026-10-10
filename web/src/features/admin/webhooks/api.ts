import { useQuery } from '@tanstack/react-query'
import { ApiError } from '@/api/problem'
import { qk } from '@/api/queries'

export type WebhookFormat = 'standard' | 'slack' | 'discord' | 'teams' | 'telegram' | 'custom'
export type WebhookSeverity = 'info' | 'warning' | 'critical'

export interface WebhookEndpoint {
  id: string
  name: string
  url: string
  format: WebhookFormat
  secret: string
  severities: WebhookSeverity[]
  headers: Record<string, string>
  custom_template?: string
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface WebhookDelivery {
  id: string
  endpoint_id: string
  event_id: string
  event_type: string
  severity: WebhookSeverity
  payload: unknown
  status: 'pending' | 'delivering' | 'delivered' | 'retrying' | 'failed'
  attempts: number
  max_attempts: number
  last_status_code?: number
  last_error?: string
  latency_ms?: number
  next_retry_at: string
  created_at: string
  delivered_at?: string
}

export interface WebhookTestResult {
  status_code: number
  response: string
  latency_ms: number
  success: boolean
  error?: string
}

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...init?.headers,
    },
    credentials: 'same-origin',
  })
  if (!res.ok) {
    let errBody: ConstructorParameters<typeof ApiError>[1]
    try {
      errBody = (await res.json()) as ConstructorParameters<typeof ApiError>[1]
    } catch {
      // ignore json parse error
    }
    throw new ApiError(res.status, errBody, res.statusText)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}

export const webhookKeys = {
  all: qk.admin('webhooks'),
  list: () => qk.admin('webhooks', 'list'),
  detail: (id: string) => qk.admin('webhooks', { detail: id }),
  deliveries: (id: string) => qk.admin('webhooks', { deliveries: id }),
}

export const webhookApi = {
  list: () => request<WebhookEndpoint[]>('/api/v1/admin/webhooks'),
  get: (id: string) => request<WebhookEndpoint>(`/api/v1/admin/webhooks/${id}`),
  create: (data: Partial<WebhookEndpoint>) =>
    request<WebhookEndpoint>('/api/v1/admin/webhooks', {
      method: 'POST',
      body: JSON.stringify(data),
    }),
  update: ({ id, ...data }: Partial<WebhookEndpoint> & { id: string }) =>
    request<WebhookEndpoint>(`/api/v1/admin/webhooks/${id}`, {
      method: 'PUT',
      body: JSON.stringify(data),
    }),
  delete: (id: string) =>
    request<void>(`/api/v1/admin/webhooks/${id}`, {
      method: 'DELETE',
    }),
  test: ({ id, severity }: { id: string; severity?: WebhookSeverity }) =>
    request<WebhookTestResult>(`/api/v1/admin/webhooks/${id}/test`, {
      method: 'POST',
      body: JSON.stringify({ severity: severity ?? 'info' }),
    }),
  deliveries: ({ id, limit = 50 }: { id: string; limit?: number }) =>
    request<WebhookDelivery[]>(`/api/v1/admin/webhooks/${id}/deliveries?limit=${limit}`),
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
