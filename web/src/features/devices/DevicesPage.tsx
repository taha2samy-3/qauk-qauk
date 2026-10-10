import { Cpu, Lock, Search } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useDevices, type DeviceInfo } from '@/api/devices'
import { widgetHint, type MyElement } from '@/api/types'
import { Page } from '@/components/layout/AppShell'
import { RelativeTime } from '@/components/RelativeTime'
import { EmptyState, ErrorState, PageHeader } from '@/components/states'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Hint } from '@/components/ui/tooltip'
import { asString, cn, formatNumber, pluralize, shortId } from '@/lib/utils'
import { useElement } from '@/realtime/hooks'

function StatusDot({ connected }: { connected: boolean | null }) {
  return (
    <span
      className={cn(
        'inline-flex size-2 rounded-full',
        connected === null
          ? 'bg-muted-foreground/40'
          : connected
            ? 'bg-success shadow-success/20 shadow-[0_0_0_3px]'
            : 'bg-destructive',
      )}
    />
  )
}

function ElementRow({ el }: { el: MyElement }) {
  const rt = useElement(el.id)
  const unit = asString(el.details.unit) ?? ''
  const hint = widgetHint(el)
  const display =
    rt.status === 'revoked'
      ? 'revoked'
      : rt.value === undefined
        ? '—'
        : hint === 'switch'
          ? rt.value
            ? 'On'
            : 'Off'
          : `${formatNumber(rt.value)}${unit ? ` ${unit}` : ''}`
  return (
    <li
      className="flex items-center gap-3 px-5 py-2.5 text-sm"
      data-testid="device-element"
      data-element={el.name}
    >
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 truncate font-medium">
          {el.name}
          {el.permission === 'R' && (
            <Hint label="Read-only">
              <Lock className="text-muted-foreground size-3" aria-label="Read-only" />
            </Hint>
          )}
        </div>
        {el.description && <div className="text-muted-foreground truncate text-xs">{el.description}</div>}
      </div>
      {hint && (
        <Badge variant="muted" className="hidden sm:inline-flex">
          {hint}
        </Badge>
      )}
      <div className="w-28 text-right">
        <div className="tabular font-semibold" data-testid="device-element-value">
          {display}
        </div>
        <div className="text-muted-foreground text-[11px]">
          {rt.lastEditAt ? <RelativeTime value={rt.lastEditAt} /> : 'no data'}
        </div>
      </div>
    </li>
  )
}

function DeviceCard({ device }: { device: DeviceInfo }) {
  // Any element of the device reports its connection status.
  const probe = useElement(device.elements[0]?.id)
  const connected = probe.deviceConnected
  return (
    <section
      className="bg-card overflow-hidden rounded-xl border shadow-xs"
      aria-label={device.name}
      data-testid="device-card"
      data-device={device.name}
    >
      <header className="flex items-center gap-3 border-b px-5 py-4">
        <span className="bg-muted flex size-9 items-center justify-center rounded-lg">
          <Cpu className="size-4" />
        </span>
        <div className="min-w-0 flex-1">
          <h2 className="truncate font-semibold">{device.name}</h2>
          <p className="text-muted-foreground font-mono text-[11px]" title={device.id}>
            {shortId(device.id)} · {pluralize(device.elements.length, 'element')}
          </p>
        </div>
        <Badge
          variant={connected === null ? 'muted' : connected ? 'success' : 'destructive'}
          className="gap-1.5"
          data-testid="device-status"
          data-status={connected === null ? 'unknown' : connected ? 'online' : 'offline'}
        >
          <StatusDot connected={connected} />
          {connected === null ? 'Checking…' : connected ? 'Online' : 'Offline'}
        </Badge>
      </header>
      <ul className="divide-y">
        {device.elements.map((el) => (
          <ElementRow key={el.id} el={el} />
        ))}
      </ul>
    </section>
  )
}

export function DevicesPage() {
  const { devices, isLoading, error, refetch } = useDevices()
  const [q, setQ] = useState('')
  const shown = useMemo(() => {
    const n = q.trim().toLowerCase()
    if (!n) return devices
    return devices
      .map((d) =>
        d.name.toLowerCase().includes(n)
          ? d
          : { ...d, elements: d.elements.filter((e) => e.name.toLowerCase().includes(n)) },
      )
      .filter((d) => d.elements.length > 0)
  }, [devices, q])

  return (
    <Page wide>
      <PageHeader
        title={
          <div className="flex items-center gap-3">
            <img src="/brand/logo-device.svg" alt="" className="size-7 rounded-lg shadow-xs" />
            <span>Devices</span>
          </div>
        }
        description="Devices behind the elements you can access, with live status and readings."
        actions={
          <div className="relative w-64">
            <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
            <Input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="Filter devices or elements…"
              aria-label="Filter devices"
              className="pl-8"
            />
          </div>
        }
      />
      {error ? (
        <ErrorState error={error} onRetry={refetch} />
      ) : isLoading ? (
        <div className="grid gap-4 lg:grid-cols-2">
          {Array.from({ length: 2 }, (_, i) => (
            <Skeleton key={i} className="h-64 rounded-xl" />
          ))}
        </div>
      ) : shown.length === 0 ? (
        <EmptyState
          duck={q ? 'detective' : 'plugged'}
          title={q ? 'No matches' : 'No devices yet'}
          description={
            q ? `Nothing matches “${q}”.` : 'Ask an administrator to grant you access to some elements.'
          }
        />
      ) : (
        <div className="grid items-start gap-4 lg:grid-cols-2">
          {shown.map((d) => (
            <DeviceCard key={d.id} device={d} />
          ))}
        </div>
      )}
    </Page>
  )
}
