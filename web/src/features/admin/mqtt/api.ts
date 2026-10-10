import { useQuery } from '@tanstack/react-query'
import { api, unwrap } from '@/api/client'
import { ApiError, errorMessage } from '@/api/problem'
import { qk } from '@/api/queries'
import type { Schemas } from '@/api/types'

export type MqttConnection = Schemas['MQTTConnection']
export type MqttDetail = Schemas['MQTTConnectionDetail']
export type MqttUplink = Schemas['MQTTUplink']
export type MqttDownlink = Schemas['MQTTDownlink']
export type MqttStatus = Schemas['MQTTStatus']
export type MqttDecoder = Schemas['MQTTDecoder']
export type MqttRejected = Schemas['MQTTRejected']
export type MqttCaptured = Schemas['MQTTCaptured']
export type MqttTestResult = Schemas['MQTTTestOutBody']

const list = <T>(p: Promise<{ data?: T[] | null; error?: unknown; response: Response }>) =>
  unwrap(p).then((d) => d ?? [])

export const mqttKeys = {
  all: qk.admin('mqtt'),
  connections: qk.admin('mqtt', 'connections'),
  connection: (id: string) => qk.admin('mqtt', { connection: id }),
  status: qk.admin('mqtt', 'status'),
  decoders: qk.admin('mqtt', 'decoders'),
  rejected: qk.admin('mqtt', 'rejected'),
  captured: (id: string) => qk.admin('mqtt', { captured: id }),
}

export const useMqttConnections = () =>
  useQuery({ queryKey: mqttKeys.connections, queryFn: () => list(api.GET('/api/v1/admin/mqtt/connections')) })
export const useMqttStatus = () =>
  useQuery({
    queryKey: mqttKeys.status,
    queryFn: () => list(api.GET('/api/v1/admin/mqtt/status')),
    refetchInterval: 5000,
  })
export const useMqttConnection = (id: string) =>
  useQuery({
    queryKey: mqttKeys.connection(id),
    queryFn: () => unwrap(api.GET('/api/v1/admin/mqtt/connections/{id}', { params: { path: { id } } })),
    refetchInterval: 5000,
  })
export const useDecoders = () =>
  useQuery({ queryKey: mqttKeys.decoders, queryFn: () => list(api.GET('/api/v1/admin/mqtt/decoders')) })
export const useRejected = (limit: number) =>
  useQuery({
    queryKey: [...mqttKeys.rejected, limit],
    queryFn: () => list(api.GET('/api/v1/admin/mqtt/rejected', { params: { query: { limit } } })),
    refetchInterval: 10000,
  })
export const useCaptured = (uplinkId: string | undefined) =>
  useQuery({
    queryKey: mqttKeys.captured(uplinkId ?? ''),
    queryFn: () =>
      list(api.GET('/api/v1/admin/mqtt/uplinks/{id}/captured', { params: { path: { id: uplinkId! } } })),
    enabled: !!uplinkId,
    refetchInterval: 3000,
  })

type B = Schemas

export const mqttApi = {
  createConnection: (body: MqttConnection) => unwrap(api.POST('/api/v1/admin/mqtt/connections', { body })),
  updateConnection: ({ id, ...body }: MqttConnection) =>
    unwrap(
      api.PUT('/api/v1/admin/mqtt/connections/{id}', { params: { path: { id } }, body: { id, ...body } }),
    ),
  deleteConnection: (id: string) =>
    unwrap(api.DELETE('/api/v1/admin/mqtt/connections/{id}', { params: { path: { id } } })),
  grant: ({ id, ...body }: B['MQTTGrantInBody'] & { id: string }) =>
    unwrap(api.PUT('/api/v1/admin/mqtt/connections/{id}/devices', { params: { path: { id } }, body })),
  revoke: ({ id, device_id }: { id: string; device_id: string }) =>
    unwrap(
      api.DELETE('/api/v1/admin/mqtt/connections/{id}/devices/{device_id}', {
        params: { path: { id, device_id } },
      }),
    ),
  createUplink: ({ connectionId, ...body }: MqttUplink & { connectionId: string }) =>
    unwrap(
      api.POST('/api/v1/admin/mqtt/connections/{id}/uplinks', {
        params: { path: { id: connectionId } },
        body,
      }),
    ),
  deleteUplink: (id: string) =>
    unwrap(api.DELETE('/api/v1/admin/mqtt/uplinks/{id}', { params: { path: { id } } })),
  capture: ({ id, seconds }: { id: string; seconds: number }) =>
    unwrap(
      api.POST('/api/v1/admin/mqtt/uplinks/{id}/capture', { params: { path: { id } }, body: { seconds } }),
    ),
  createDownlink: ({ connectionId, ...body }: MqttDownlink & { connectionId: string }) =>
    unwrap(
      api.POST('/api/v1/admin/mqtt/connections/{id}/downlinks', {
        params: { path: { id: connectionId } },
        body,
      }),
    ),
  deleteDownlink: (id: string) =>
    unwrap(api.DELETE('/api/v1/admin/mqtt/downlinks/{id}', { params: { path: { id } } })),
  createDecoder: (body: B['DecoderInBody']) => unwrap(api.POST('/api/v1/admin/mqtt/decoders', { body })),
  updateDecoder: ({ id, ...body }: B['DecoderUpdateInBody'] & { id: string }) =>
    unwrap(api.PUT('/api/v1/admin/mqtt/decoders/{id}', { params: { path: { id } }, body })),
  deleteDecoder: (id: string) =>
    unwrap(api.DELETE('/api/v1/admin/mqtt/decoders/{id}', { params: { path: { id } } })),
  test: (body: B['MQTTTestInBody']) => unwrap(api.POST('/api/v1/admin/mqtt/test', { body })),
}

/** Short summaries for tables. */
export function deviceSpecLabel(d: unknown): string {
  const v = (d ?? {}) as { segment?: number; field?: string; fixed?: string }
  if (v.segment !== undefined) return `topic level ${v.segment}`
  if (v.field) return `field ${v.field}`
  if (v.fixed) return `always ${v.fixed}`
  return '—'
}

export function fieldMapEntries(m: unknown): { element: string; value: string }[] {
  return Array.isArray(m) ? (m as { element: string; value: string }[]) : []
}

/** A form-level message for a failed request (validation details included). */
export const formError = (e: unknown) => (e instanceof ApiError ? e.description : errorMessage(e))

/** A payload (base64 from the API) as text, or as hex bytes when it isn't printable. */
export function payloadPreview(b64: unknown): string {
  if (typeof b64 !== 'string') return ''
  try {
    const raw = atob(b64)
    return /^[\x20-\x7e\s]*$/.test(raw)
      ? raw
      : Array.from(raw, (c) => c.charCodeAt(0).toString(16).padStart(2, '0')).join(' ')
  } catch {
    return b64
  }
}
