import { Activity } from 'lucide-react'
import { useState } from 'react'
import type { PresenceRow } from '@/api/types'
import { DataTable, Mono, type Column } from '@/components/DataTable'
import { Page } from '@/components/layout/AppShell'
import { RelativeTime } from '@/components/RelativeTime'
import { PageHeader } from '@/components/states'
import { Label } from '@/components/ui/label'
import { Spinner } from '@/components/ui/spinner'
import { Switch } from '@/components/ui/switch'
import { useAdminDevices, usePresence } from './api'

const INTERVAL = 5000

export function PresencePage() {
  const [auto, setAuto] = useState(true)
  const presence = usePresence(auto ? INTERVAL : false)
  const devices = useAdminDevices()
  const deviceName = (id: string) => devices.data?.find((d) => d.id === id)?.name ?? id.slice(0, 8)

  const columns: Column<PresenceRow>[] = [
    {
      id: 'device',
      header: 'Device',
      sortValue: (p) => deviceName(p.device_id),
      searchValue: (p) => deviceName(p.device_id),
      cell: (p) => (
        <span className="inline-flex items-center gap-2 font-medium">
          <span className="bg-success size-2 rounded-full" /> {deviceName(p.device_id)}
        </span>
      ),
    },
    {
      id: 'since',
      header: 'Connected',
      sortValue: (p) => p.connected_at,
      cell: (p) => <RelativeTime value={p.connected_at} />,
    },
    {
      id: 'seen',
      header: 'Last seen',
      sortValue: (p) => p.last_seen_at,
      cell: (p) => <RelativeTime value={p.last_seen_at} />,
    },
    {
      id: 'gateway',
      header: 'Gateway',
      searchValue: (p) => p.gateway_id,
      cell: (p) => <Mono>{p.gateway_id}</Mono>,
    },
    {
      id: 'conn',
      header: 'Connection',
      searchValue: (p) => p.conn_id,
      cell: (p) => <Mono>{p.conn_id}</Mono>,
    },
  ]

  return (
    <Page>
      <PageHeader
        title="Presence"
        description="Live device leases held by gateways. A device is online while its lease is fresh."
        actions={
          <div className="flex items-center gap-3 text-sm">
            {presence.isFetching && <Spinner />}
            <span className="text-muted-foreground text-xs">
              Updated <RelativeTime value={presence.dataUpdatedAt || null} />
            </span>
            <div className="flex items-center gap-2">
              <Switch id="auto-refresh" checked={auto} onCheckedChange={setAuto} />
              <Label htmlFor="auto-refresh">Auto-refresh</Label>
            </div>
          </div>
        }
      />
      <DataTable
        label="Presence"
        data={presence.data}
        columns={columns}
        getRowId={(p) => p.conn_id}
        isLoading={presence.isLoading}
        error={presence.error}
        onRetry={() => void presence.refetch()}
        searchPlaceholder="Search devices…"
        initialSort={{ id: 'device', dir: 'asc' }}
        empty={{
          icon: Activity,
          title: 'No devices online',
          description: 'Connected devices appear here within seconds.',
        }}
      />
    </Page>
  )
}
