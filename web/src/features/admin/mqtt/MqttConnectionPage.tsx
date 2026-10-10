import { ArrowDownToLine, ArrowUpFromLine, Camera, FlaskConical, Plus, Trash2, Users } from 'lucide-react'
import { useState } from 'react'
import { Link, useParams } from 'react-router'
import { toast } from 'sonner'
import { DataTable, Mono, type Column } from '@/components/DataTable'
import { JsonEditor, parseJson } from '@/components/JsonEditor'
import { Page } from '@/components/layout/AppShell'
import { RelativeTime } from '@/components/RelativeTime'
import { ErrorState, PageHeader, TableSkeleton } from '@/components/states'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { toastError } from '@/lib/forms'
import { FormDialog, RowActions } from '../FormDialog'
import { useAdminDevices, useAdminMutation } from '../api'
import {
  deviceSpecLabel,
  fieldMapEntries,
  formError,
  mqttApi,
  payloadPreview,
  mqttKeys,
  useCaptured,
  useDecoders,
  useMqttConnection,
  type MqttDownlink,
  type MqttTestResult,
  type MqttUplink,
} from './api'
import { MqttNav, StatusDot } from './MqttNav'

const NO_DECODER = '__none__'

function Section({
  icon: Icon,
  title,
  description,
  action,
  children,
}: {
  icon: typeof Users
  title: string
  description: string
  action?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <section className="mb-8">
      <div className="mb-3 flex items-end justify-between gap-4">
        <div>
          <h2 className="flex items-center gap-2 text-base font-semibold">
            <Icon className="text-muted-foreground size-4" /> {title}
          </h2>
          <p className="text-muted-foreground text-sm">{description}</p>
        </div>
        {action}
      </div>
      {children}
    </section>
  )
}

function GrantDialog({
  id,
  open,
  onOpenChange,
}: {
  id: string
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const devices = useAdminDevices()
  const [device, setDevice] = useState('')
  const [ext, setExt] = useState('')
  const [error, setError] = useState<string>()
  const grant = useAdminMutation(mqttApi.grant, [mqttKeys.connection(id)])
  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Grant a device"
      description="The connection may write only to granted devices. The external id is how the device appears in topics or payloads."
      submitting={grant.isPending}
      formError={error}
      submitLabel="Grant"
      onSubmit={async (e) => {
        e.preventDefault()
        try {
          await grant.mutateAsync({ id, device_id: device, external_id: ext })
          toast.success('Device granted')
          onOpenChange(false)
        } catch (err) {
          setError(formError(err))
        }
      }}
    >
      <Field label="Device" required>
        <Select value={device} onValueChange={setDevice}>
          <SelectTrigger aria-label="Device">
            <SelectValue placeholder="Choose a device…" />
          </SelectTrigger>
          <SelectContent>
            {(devices.data ?? []).map((d) => (
              <SelectItem key={d.id} value={d.id}>
                {d.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <Field
        label="External id"
        htmlFor="g-ext"
        required
        description="e.g. cold-room-1 in quack/demo/cold-room-1/up"
      >
        <Input id="g-ext" className="font-mono" value={ext} onChange={(e) => setExt(e.target.value)} />
      </Field>
    </FormDialog>
  )
}

const FIELD_MAP_TEMPLATE = '[\n  {"element": "Temperature", "value": "t"}\n]'

function UplinkDialog({
  id,
  open,
  onOpenChange,
}: {
  id: string
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const decoders = useDecoders()
  const [filter, setFilter] = useState('sensors/+/up')
  const [format, setFormat] = useState('json')
  const [decoder, setDecoder] = useState(NO_DECODER)
  const [segment, setSegment] = useState('1')
  const [fieldMap, setFieldMap] = useState(FIELD_MAP_TEMPLATE)
  const [error, setError] = useState<string>()
  const create = useAdminMutation(mqttApi.createUplink, [mqttKeys.connection(id)])
  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title="New uplink rule"
      description="Messages on the topic filter go through the decoder (if any) and the field map. One message can feed many elements."
      submitting={create.isPending}
      formError={error}
      submitLabel="Create rule"
      className="sm:max-w-xl"
      onSubmit={async (e) => {
        e.preventDefault()
        const fm = parseJson(fieldMap)
        if (!fm.ok) return setError(`Field map: ${fm.error}`)
        try {
          await create.mutateAsync({
            connectionId: id,
            id: '',
            topic_filter: filter,
            qos: 1,
            format: format as MqttUplink['format'],
            decoder_id: decoder === NO_DECODER ? undefined : decoder,
            device: { segment: Number(segment) },
            field_map: fm.value,
            enabled: true,
          } as MqttUplink & { connectionId: string })
          toast.success('Uplink rule created')
          onOpenChange(false)
        } catch (err) {
          setError(formError(err))
        }
      }}
    >
      <div className="grid gap-4 sm:grid-cols-[2fr_1fr]">
        <Field label="Topic filter" htmlFor="u-filter" required description="+ and # allowed">
          <Input
            id="u-filter"
            className="font-mono"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </Field>
        <Field label="Device from topic level" htmlFor="u-seg" description="0-based">
          <Input
            id="u-seg"
            inputMode="numeric"
            value={segment}
            onChange={(e) => setSegment(e.target.value)}
          />
        </Field>
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Payload format">
          <Select value={format} onValueChange={setFormat}>
            <SelectTrigger aria-label="Payload format">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {['json', 'text', 'number', 'bytes'].map((f) => (
                <SelectItem key={f} value={f}>
                  {f}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field label="Decoder">
          <Select value={decoder} onValueChange={setDecoder}>
            <SelectTrigger aria-label="Decoder">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NO_DECODER}>None (the payload is the data)</SelectItem>
              {(decoders.data ?? []).map((d) => (
                <SelectItem key={d.id} value={d.id}>
                  {d.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      </div>
      <Field
        label="Field map (JSON)"
        htmlFor="u-map"
        description='[{"element": name, "value": path, "wrap": "value"|"raw", "when": "exists"}]'
      >
        <JsonEditor id="u-map" rows={6} value={fieldMap} onChange={setFieldMap} />
      </Field>
    </FormDialog>
  )
}

function DownlinkDialog({
  id,
  open,
  onOpenChange,
}: {
  id: string
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const [ext, setExt] = useState('')
  const [element, setElement] = useState('')
  const [topic, setTopic] = useState('devices/{device}/cmd/{element}')
  const [template, setTemplate] = useState('{"value": "{{value}}"}')
  const [error, setError] = useState<string>()
  const create = useAdminMutation(mqttApi.createDownlink, [mqttKeys.connection(id)])
  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title="New downlink"
      description="Dashboard commands for this element are published here, once, by the gateway that owns slot 0."
      submitting={create.isPending}
      formError={error}
      submitLabel="Create downlink"
      onSubmit={async (e) => {
        e.preventDefault()
        const tpl = parseJson(template)
        if (!tpl.ok) return setError(`Template: ${tpl.error}`)
        try {
          await create.mutateAsync({
            connectionId: id,
            id: '',
            device_external_id: ext,
            element,
            topic_template: topic,
            encoder: { template: tpl.value },
            qos: 1,
            retain: false,
          } as MqttDownlink & { connectionId: string })
          toast.success('Downlink created')
          onOpenChange(false)
        } catch (err) {
          setError(formError(err))
        }
      }}
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Device external id" htmlFor="d-ext" required>
          <Input id="d-ext" className="font-mono" value={ext} onChange={(e) => setExt(e.target.value)} />
        </Field>
        <Field label="Element" htmlFor="d-el" required>
          <Input id="d-el" value={element} onChange={(e) => setElement(e.target.value)} />
        </Field>
      </div>
      <Field label="Topic template" htmlFor="d-topic" required description="{device} {element} {user}">
        <Input id="d-topic" className="font-mono" value={topic} onChange={(e) => setTopic(e.target.value)} />
      </Field>
      <Field
        label="Payload template (JSON)"
        htmlFor="d-tpl"
        description='"{{value}}" and "{{message}}" are replaced'
      >
        <JsonEditor id="d-tpl" rows={3} value={template} onChange={setTemplate} />
      </Field>
    </FormDialog>
  )
}

function TestPanel({ rule }: { rule: MqttUplink }) {
  const [topic, setTopic] = useState(rule.topic_filter.replaceAll('+', 'device-1').replace('#', 'x'))
  const [payload, setPayload] = useState(rule.format === 'bytes' ? 'hex:2701' : '{"t": 21.5, "h": 48}')
  const [result, setResult] = useState<MqttTestResult>()
  const [error, setError] = useState<string>()
  const test = useAdminMutation(mqttApi.test, [])
  return (
    <Card className="grid gap-3 p-4" data-testid="mqtt-test">
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Topic" htmlFor="t-topic" description="a topic this rule matches">
          <Input
            id="t-topic"
            className="font-mono"
            value={topic}
            onChange={(e) => setTopic(e.target.value)}
          />
        </Field>
        <Field label="Payload" htmlFor="t-payload" description="text/JSON, or hex:… / base64:…">
          <Input
            id="t-payload"
            className="font-mono"
            value={payload}
            onChange={(e) => setPayload(e.target.value)}
          />
        </Field>
      </div>
      <div>
        <Button
          size="sm"
          onClick={async () => {
            setError(undefined)
            try {
              setResult(
                await test.mutateAsync({
                  format: rule.format,
                  device: rule.device,
                  field_map: rule.field_map,
                  decoder_id: rule.decoder_id ?? undefined,
                  topic,
                  payload,
                } as Parameters<typeof mqttApi.test>[0]),
              )
            } catch (err) {
              setError(formError(err))
            }
          }}
        >
          <FlaskConical /> Run the pipeline
        </Button>
        <span className="text-muted-foreground ml-3 text-xs">Nothing is published.</span>
      </div>
      {error && <p className="text-destructive text-sm">{error}</p>}
      {result && (
        <div className="grid gap-2 text-sm">
          {(result.values ?? []).map((v, i) => (
            <div
              key={i}
              className="bg-muted flex items-center justify-between rounded px-3 py-2 font-mono text-xs"
            >
              <span>
                {v.device_external_id} → <b>{v.element}</b>
              </span>
              <span>{JSON.stringify(v.message)}</span>
            </div>
          ))}
          {(result.rejections ?? []).map((r, i) => (
            <div key={i} className={result.fatal ? 'text-destructive' : 'text-muted-foreground'}>
              {r}
            </div>
          ))}
        </div>
      )}
    </Card>
  )
}

function CaptureSheet({
  rule,
  onOpenChange,
}: {
  rule: MqttUplink | null
  onOpenChange: (o: boolean) => void
}) {
  const captured = useCaptured(rule?.id)
  const capture = useAdminMutation(mqttApi.capture, [])
  return (
    <Sheet open={!!rule} onOpenChange={onOpenChange}>
      <SheetContent className="sm:max-w-xl">
        <SheetHeader>
          <SheetTitle>Rule · {rule?.topic_filter}</SheetTitle>
          <SheetDescription>Capture the next messages, and try the pipeline on a sample.</SheetDescription>
        </SheetHeader>
        <SheetBody className="space-y-5">
          {rule && <TestPanel key={rule.id} rule={rule} />}
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={async () => {
                try {
                  await capture.mutateAsync({ id: rule!.id, seconds: 300 })
                  toast.success('Capturing for 5 minutes')
                } catch (err) {
                  toastError('Could not start capture', err)
                }
              }}
            >
              <Camera /> Capture for 5 min
            </Button>
            {rule?.capture_until && (
              <span className="text-muted-foreground text-xs">
                until <RelativeTime value={rule.capture_until} />
              </span>
            )}
          </div>
          <ul className="space-y-2" data-testid="captured">
            {(captured.data ?? []).map((m, i) => (
              <li key={i} className="rounded-lg border p-3">
                <div className="mb-1 flex justify-between text-xs">
                  <Mono>{m.topic}</Mono>
                  <RelativeTime value={m.time} className="text-muted-foreground" />
                </div>
                <pre className="bg-muted overflow-auto rounded px-2 py-1.5 font-mono text-[11px]">
                  {payloadPreview(m.payload)}
                </pre>
              </li>
            ))}
            {captured.data?.length === 0 && (
              <li className="text-muted-foreground text-sm">Nothing captured yet.</li>
            )}
          </ul>
        </SheetBody>
      </SheetContent>
    </Sheet>
  )
}

export function MqttConnectionPage() {
  const { id = '' } = useParams()
  const conn = useMqttConnection(id)
  const devices = useAdminDevices()
  const decoders = useDecoders()
  const [grantOpen, setGrantOpen] = useState(false)
  const [uplinkOpen, setUplinkOpen] = useState(false)
  const [downlinkOpen, setDownlinkOpen] = useState(false)
  const [ruleOf, setRuleOf] = useState<MqttUplink | null>(null)
  const revoke = useAdminMutation(mqttApi.revoke, [mqttKeys.connection(id)])
  const delUplink = useAdminMutation(mqttApi.deleteUplink, [mqttKeys.connection(id)])
  const delDownlink = useAdminMutation(mqttApi.deleteDownlink, [mqttKeys.connection(id)])
  const deviceName = (d: string) => devices.data?.find((x) => x.id === d)?.name ?? d.slice(0, 8)
  const decoderName = (d?: string | null) =>
    d ? (decoders.data?.find((x) => x.id === d)?.name ?? d.slice(0, 8)) : null

  if (conn.isError) return <ErrorState error={conn.error} onRetry={() => void conn.refetch()} />
  if (!conn.data) return <TableSkeleton />
  const c = conn.data
  const auth = ((c.auth ?? {}) as { method?: string }).method ?? 'none'

  const uplinkCols: Column<MqttUplink>[] = [
    { id: 'filter', header: 'Topic filter', cell: (u) => <Mono>{u.topic_filter}</Mono> },
    { id: 'format', header: 'Format', cell: (u) => <Badge variant="muted">{u.format}</Badge> },
    {
      id: 'decoder',
      header: 'Decoder',
      cell: (u) =>
        decoderName(u.decoder_id) ? (
          <Badge variant="secondary">{decoderName(u.decoder_id)}</Badge>
        ) : (
          <span className="text-muted-foreground">—</span>
        ),
    },
    {
      id: 'device',
      header: 'Device',
      cell: (u) => <span className="text-sm">{deviceSpecLabel(u.device)}</span>,
    },
    {
      id: 'map',
      header: 'Elements',
      cell: (u) => (
        <div className="flex max-w-sm flex-wrap gap-1">
          {fieldMapEntries(u.field_map).map((m) => (
            <Badge key={m.element} variant="outline">
              {m.value || '·'} → {m.element}
            </Badge>
          ))}
        </div>
      ),
    },
    {
      id: 'actions',
      header: <span className="sr-only">Actions</span>,
      headClassName: 'w-36',
      cell: (u) => (
        <RowActions>
          <Button variant="ghost" size="xs" onClick={() => setRuleOf(u)}>
            <FlaskConical /> Test & capture
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={`Delete rule ${u.topic_filter}`}
            onClick={() => delUplink.mutateAsync(u.id).catch((e) => toastError('Could not delete', e))}
          >
            <Trash2 />
          </Button>
        </RowActions>
      ),
    },
  ]

  const downlinkCols: Column<MqttDownlink>[] = [
    { id: 'device', header: 'Device', cell: (d) => <Mono>{d.device_external_id}</Mono> },
    { id: 'element', header: 'Element', cell: (d) => <span className="font-medium">{d.element}</span> },
    { id: 'topic', header: 'Topic', cell: (d) => <Mono>{d.topic_template}</Mono> },
    { id: 'payload', header: 'Payload', cell: (d) => <Mono>{JSON.stringify(d.encoder)}</Mono> },
    {
      id: 'actions',
      header: <span className="sr-only">Actions</span>,
      headClassName: 'w-12',
      cell: (d) => (
        <RowActions>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={`Delete downlink ${d.element}`}
            onClick={() => delDownlink.mutateAsync(d.id).catch((e) => toastError('Could not delete', e))}
          >
            <Trash2 />
          </Button>
        </RowActions>
      ),
    },
  ]

  return (
    <Page wide>
      <PageHeader
        title={c.name}
        description={
          <span>
            <Link to="/admin/mqtt" className="hover:underline">
              MQTT
            </Link>{' '}
            · <Mono>{c.broker_url}</Mono> · auth <b>{auth}</b> · {c.replicas || 1} slot
            {(c.replicas || 1) > 1 ? 's (shared subscription)' : ''}
          </span>
        }
      />
      <MqttNav />
      <div className="mb-8 grid gap-3 sm:grid-cols-2 lg:grid-cols-3" data-testid="mqtt-slots">
        {Array.from({ length: c.replicas || 1 }, (_, slot) => {
          const s = (c.status ?? []).find((x) => x.slot === slot)
          const n = (s?.counters ?? {}) as Record<string, number>
          return (
            <Card key={slot} className="p-4">
              <div className="mb-2 flex items-center justify-between">
                <span className="text-sm font-semibold">Slot {slot}</span>
                <StatusDot ok={!!s?.connected} label={s?.connected ? 'connected' : 'not connected'} />
              </div>
              <div className="text-muted-foreground space-y-1 text-xs">
                <div>
                  client id <Mono>{`${c.client_id_prefix}-${slot}`}</Mono>
                </div>
                <div>owner {s ? <Mono>{s.gateway_id}</Mono> : '—'}</div>
                {s && (
                  <div className="tabular">
                    {n.received ?? 0} received · {n.published ?? 0} values · {n.rejected ?? 0} rejected
                  </div>
                )}
                {s?.last_error && !s.connected && <div className="text-destructive">{s.last_error}</div>}
              </div>
            </Card>
          )
        })}
      </div>

      <Section
        icon={Users}
        title="Granted devices"
        description="Only these devices can be written, addressed by external id."
        action={
          <Button size="sm" variant="outline" onClick={() => setGrantOpen(true)}>
            <Plus /> Grant device
          </Button>
        }
      >
        <DataTable
          label="Granted devices"
          data={c.devices ?? []}
          getRowId={(d) => d.device_id}
          columns={[
            { id: 'ext', header: 'External id', cell: (d) => <Mono>{d.external_id}</Mono> },
            {
              id: 'device',
              header: 'Device',
              cell: (d) => <span className="font-medium">{deviceName(d.device_id)}</span>,
            },
            {
              id: 'actions',
              header: <span className="sr-only">Actions</span>,
              headClassName: 'w-12',
              cell: (d) => (
                <RowActions>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    aria-label={`Revoke ${d.external_id}`}
                    onClick={() =>
                      revoke
                        .mutateAsync({ id, device_id: d.device_id })
                        .catch((e) => toastError('Could not revoke', e))
                    }
                  >
                    <Trash2 />
                  </Button>
                </RowActions>
              ),
            },
          ]}
          empty={{ icon: Users, title: 'No devices granted' }}
        />
      </Section>

      <Section
        icon={ArrowDownToLine}
        title="Uplink rules"
        description="From the broker to elements."
        action={
          <Button size="sm" variant="outline" onClick={() => setUplinkOpen(true)}>
            <Plus /> New rule
          </Button>
        }
      >
        <DataTable
          label="Uplink rules"
          data={c.uplinks ?? []}
          getRowId={(u) => u.id}
          columns={uplinkCols}
          empty={{ icon: ArrowDownToLine, title: 'No uplink rules' }}
        />
      </Section>

      <Section
        icon={ArrowUpFromLine}
        title="Downlinks"
        description="Dashboard commands, published to the broker."
        action={
          <Button size="sm" variant="outline" onClick={() => setDownlinkOpen(true)}>
            <Plus /> New downlink
          </Button>
        }
      >
        <DataTable
          label="Downlinks"
          data={c.downlinks ?? []}
          getRowId={(d) => d.id}
          columns={downlinkCols}
          empty={{ icon: ArrowUpFromLine, title: 'No downlinks' }}
        />
      </Section>

      <GrantDialog id={id} open={grantOpen} onOpenChange={setGrantOpen} />
      <UplinkDialog id={id} open={uplinkOpen} onOpenChange={setUplinkOpen} />
      <DownlinkDialog id={id} open={downlinkOpen} onOpenChange={setDownlinkOpen} />
      <CaptureSheet
        rule={ruleOf ? ((c.uplinks ?? []).find((u) => u.id === ruleOf.id) ?? ruleOf) : null}
        onOpenChange={(o) => !o && setRuleOf(null)}
      />
    </Page>
  )
}
