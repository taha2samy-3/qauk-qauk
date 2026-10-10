import { Cable, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import type { Connection } from '@/api/types'
import { DataTable, Mono, type Column } from '@/components/DataTable'
import { Page } from '@/components/layout/AppShell'
import { RelativeTime } from '@/components/RelativeTime'
import { PageHeader } from '@/components/states'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { formatDateTime } from '@/lib/time'
import { asString, isRecord } from '@/lib/utils'
import { useAdminDevices, useConnections } from './api'

const ALL = '__all__'

function duration(from: string, to: string | null): string {
  const ms = (to ? Date.parse(to) : Date.now()) - Date.parse(from)
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`
  if (s < 86400) return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`
  return `${Math.floor(s / 86400)}d ${Math.floor((s % 86400) / 3600)}h`
}

export function ConnectionsPage() {
  const devices = useAdminDevices()
  const [device, setDevice] = useState(ALL)
  const [limit, setLimit] = useState('200')
  const conns = useConnections({ ...(device !== ALL ? { device_id: device } : {}), limit: Number(limit) })
  const deviceName = (id: string) => devices.data?.find((d) => d.id === id)?.name ?? id.slice(0, 8)
  const detail = (c: Connection, k: string) => (isRecord(c.details) ? asString(c.details[k]) : undefined)

  const columns: Column<Connection>[] = [
    {
      id: 'device',
      header: 'Device',
      sortValue: (c) => deviceName(c.device_id),
      searchValue: (c) => deviceName(c.device_id),
      cell: (c) => <span className="font-medium">{deviceName(c.device_id)}</span>,
    },
    {
      id: 'state',
      header: 'State',
      sortValue: (c) => (c.disconnected_at ? 1 : 0),
      cell: (c) =>
        c.disconnected_at ? <Badge variant="muted">Closed</Badge> : <Badge variant="success">Open</Badge>,
    },
    {
      id: 'connected',
      header: 'Connected',
      sortValue: (c) => c.connected_at,
      cell: (c) => (
        <span title={formatDateTime(c.connected_at)}>
          <RelativeTime value={c.connected_at} />
        </span>
      ),
    },
    {
      id: 'duration',
      header: 'Duration',
      sortValue: (c) =>
        Date.parse(c.disconnected_at ?? new Date().toISOString()) - Date.parse(c.connected_at),
      cell: (c) => <span className="tabular">{duration(c.connected_at, c.disconnected_at)}</span>,
    },
    {
      id: 'client',
      header: 'Client',
      searchValue: (c) => `${detail(c, 'client') ?? ''} ${detail(c, 'user_agent') ?? ''}`,
      cell: (c) => (
        <div>
          <Mono>{detail(c, 'client') ?? '—'}</Mono>
          <div className="text-muted-foreground max-w-[16rem] truncate text-xs">
            {detail(c, 'user_agent')}
          </div>
        </div>
      ),
    },
    {
      id: 'gateway',
      header: 'Gateway',
      searchValue: (c) => c.gateway_id,
      cell: (c) => (
        <Mono>
          {c.gateway_id}
          {detail(c, 'conn_id') ? ` / ${detail(c, 'conn_id')}` : ''}
        </Mono>
      ),
    },
  ]

  return (
    <Page wide>
      <PageHeader
        title={
          <div className="flex items-center gap-3">
            <img src="/brand/logo-connections.svg" alt="" className="size-7 rounded-lg shadow-xs" />
            <span>Connections</span>
          </div>
        }
        description="Audit log of device WebSocket sessions."
        actions={
          <Button variant="outline" onClick={() => void conns.refetch()} disabled={conns.isFetching}>
            <RefreshCw className={conns.isFetching ? 'animate-spin' : undefined} /> Refresh
          </Button>
        }
      />
      <DataTable
        label="Connections"
        data={conns.data}
        columns={columns}
        getRowId={(c) => c.id}
        isLoading={conns.isLoading}
        error={conns.error}
        onRetry={() => void conns.refetch()}
        searchPlaceholder="Search client, gateway…"
        initialSort={{ id: 'connected', dir: 'desc' }}
        pageSize={25}
        empty={{ icon: Cable, title: 'No connections recorded' }}
        toolbar={
          <>
            <Select value={device} onValueChange={setDevice}>
              <SelectTrigger className="w-48" aria-label="Filter by device">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL}>All devices</SelectItem>
                {(devices.data ?? []).map((d) => (
                  <SelectItem key={d.id} value={d.id}>
                    {d.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select value={limit} onValueChange={setLimit}>
              <SelectTrigger className="w-32" aria-label="Number of rows to load">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {['50', '200', '1000'].map((n) => (
                  <SelectItem key={n} value={n}>
                    Last {n}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </>
        }
      />
    </Page>
  )
}
