import { useQuery } from '@tanstack/react-query'
import { api, unwrap } from './client'
import { qk, useMe, useMyElements } from './queries'
import type { MyElement } from './types'
import { shortId } from '@/lib/utils'

export interface DeviceInfo {
  id: string
  name: string
  /** false when the name is a placeholder (see below) */
  named: boolean
  elements: MyElement[]
}

/**
 * Device names for the devices behind the user's readable elements.
 *
 * BACKEND GAP: /me/elements has `device_id` but no device name, and the only
 * device endpoint is admin-only. Admins get names from /admin/devices; other
 * users get them from the newest raw history event of one element per device
 * (device-sourced events carry the device name as `actor_name`). Devices
 * without recent history fall back to a short id.
 */
async function fetchDeviceNames(isAdmin: boolean, elements: MyElement[]): Promise<Record<string, string>> {
  if (isAdmin) {
    const devices = (await unwrap(api.GET('/api/v1/admin/devices'))) ?? []
    return Object.fromEntries(devices.map((d) => [d.id, d.name]))
  }
  const firstByDevice = new Map<string, string>()
  for (const el of elements) if (!firstByDevice.has(el.device_id)) firstByDevice.set(el.device_id, el.id)
  const to = new Date()
  const from = new Date(to.getTime() - 5 * 60_000)
  const entries = await Promise.all(
    [...firstByDevice].map(async ([deviceId, elementId]) => {
      try {
        const body = await unwrap(
          api.GET('/api/v1/elements/{id}/history', {
            params: {
              path: { id: elementId },
              query: { from: from.toISOString(), to: to.toISOString(), step: 'raw', limit: 50 },
            },
          }),
        )
        const ev = (body.events ?? []).findLast((e) => e.source === 'device' && e.actor_id === deviceId)
        return ev ? ([deviceId, ev.actor_name] as const) : undefined
      } catch {
        return undefined
      }
    }),
  )
  return Object.fromEntries(entries.filter((e) => e !== undefined))
}

export function useDevices() {
  const me = useMe()
  const elements = useMyElements()
  const deviceIds = [...new Set((elements.data ?? []).map((e) => e.device_id))].sort()
  const names = useQuery({
    queryKey: [...qk.deviceNames(deviceIds), me.data?.is_admin ?? false],
    queryFn: () => fetchDeviceNames(!!me.data?.is_admin, elements.data ?? []),
    enabled: !!elements.data && !!me.data,
    staleTime: 5 * 60_000,
  })
  const devices: DeviceInfo[] = deviceIds.map((id) => {
    const name = names.data?.[id]
    return {
      id,
      name: name ?? `Device ${shortId(id)}`,
      named: !!name,
      elements: (elements.data ?? []).filter((e) => e.device_id === id),
    }
  })
  devices.sort((a, b) => a.name.localeCompare(b.name))
  const byId = new Map(devices.map((d) => [d.id, d]))
  return {
    devices,
    byId,
    isLoading: elements.isLoading || (names.isLoading && !names.data),
    error: elements.error ?? null,
    refetch: () => {
      void elements.refetch()
      void names.refetch()
    },
  }
}
