import { ShieldAlert } from 'lucide-react'
import { DataTable, Mono, type Column } from '@/components/DataTable'
import { Page } from '@/components/layout/AppShell'
import { RelativeTime } from '@/components/RelativeTime'
import { PageHeader } from '@/components/states'
import { Badge } from '@/components/ui/badge'
import { useMqttConnections, useRejected, type MqttRejected } from './api'
import { MqttNav } from './MqttNav'

function preview(b64: unknown): string {
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

export function MqttRejectedPage() {
  const rejected = useRejected(200)
  const conns = useMqttConnections()
  const connName = (id: string) => conns.data?.find((c) => c.id === id)?.name ?? id.slice(0, 8)
  const columns: Column<MqttRejected>[] = [
    {
      id: 'time',
      header: 'When',
      sortValue: (r) => r.time,
      cell: (r) => <RelativeTime value={r.time} className="text-muted-foreground" />,
    },
    { id: 'conn', header: 'Connection', cell: (r) => connName(r.connection_id) },
    { id: 'topic', header: 'Topic', searchValue: (r) => r.topic, cell: (r) => <Mono>{r.topic}</Mono> },
    {
      id: 'device',
      header: 'Device',
      cell: (r) => (r.device_external_id ? <Mono>{r.device_external_id}</Mono> : '—'),
    },
    {
      id: 'reasons',
      header: 'Why',
      searchValue: (r) => (r.reasons ?? []).join(' '),
      cell: (r) => (
        <div className="flex max-w-md flex-col gap-1">
          {(r.reasons ?? []).map((x, i) => (
            <Badge key={i} variant="destructive" className="justify-start text-left whitespace-normal">
              {x}
            </Badge>
          ))}
        </div>
      ),
    },
    {
      id: 'payload',
      header: 'Payload',
      cell: (r) => (
        <Mono className="block max-w-xs truncate">
          {preview(r.payload)} {r.payload_size > 4096 && `(${r.payload_size} B)`}
        </Mono>
      ),
    },
  ]
  return (
    <Page wide>
      <PageHeader
        title="MQTT"
        description="Messages the decoders, field maps or grants refused (mqtt.dlq.v1, 30 days)."
      />
      <MqttNav />
      <DataTable
        label="Rejected messages"
        data={rejected.data}
        columns={columns}
        getRowId={(r) => `${r.time}-${r.topic}-${r.gateway_id}`}
        isLoading={rejected.isLoading}
        error={rejected.error}
        onRetry={() => void rejected.refetch()}
        initialSort={{ id: 'time', dir: 'desc' }}
        searchPlaceholder="Search topics or reasons…"
        empty={{
          icon: ShieldAlert,
          title: 'Nothing rejected',
          description: 'Every message matched a rule and a granted device.',
        }}
      />
    </Page>
  )
}
