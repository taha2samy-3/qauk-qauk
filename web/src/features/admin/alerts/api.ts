import { useQuery } from '@tanstack/react-query'
import { ApiError } from '@/api/problem'
import { qk } from '@/api/queries'

export type AlertCondition = 'above' | 'below' | 'outside_range' | 'equals'
export type AlertSeverity = 'info' | 'warning' | 'critical'

export interface ElementAlertRule {
  id: string
  element_id: string
  name: string
  condition: AlertCondition
  threshold: number
  threshold_max?: number
  hysteresis: number
  severity: AlertSeverity
  message: string
  enabled: boolean
  created_at: string
  updated_at: string
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
      // ignore
    }
    throw new ApiError(res.status, errBody, res.statusText)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}

export const alertKeys = {
  elementAlerts: (elementId: string) => qk.admin('alerts', elementId),
}

export const alertApi = {
  list: (elementId: string) =>
    request<ElementAlertRule[]>(`/api/v1/admin/elements/${elementId}/alerts`),
  save: ({ elementId, ...rule }: Partial<ElementAlertRule> & { elementId: string }) =>
    request<ElementAlertRule>(`/api/v1/admin/elements/${elementId}/alerts`, {
      method: 'POST',
      body: JSON.stringify(rule),
    }),
  delete: ({ elementId, ruleId }: { elementId: string; ruleId: string }) =>
    request<void>(`/api/v1/admin/elements/${elementId}/alerts/${ruleId}`, {
      method: 'DELETE',
    }),
}

export const useElementAlerts = (elementId: string | undefined) =>
  useQuery({
    queryKey: alertKeys.elementAlerts(elementId ?? ''),
    queryFn: () => (elementId ? alertApi.list(elementId) : Promise.resolve([])),
    enabled: !!elementId,
  })
