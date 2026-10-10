import { Plus, RadioTower, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { toast } from 'sonner'
import { useConfirm } from '@/components/ConfirmDialog'
import { DataTable, Mono, type Column } from '@/components/DataTable'
import { Page } from '@/components/layout/AppShell'
import { PageHeader } from '@/components/states'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { toastError } from '@/lib/forms'
import { FormDialog, RowActions } from '../FormDialog'
import { useAdminMutation } from '../api'
import {
  formError,
  mqttApi,
  mqttKeys,
  useMqttConnections,
  useMqttStatus,
  type MqttConnection,
  type MqttStatus,
} from './api'
import { MqttNav, StatusDot } from './MqttNav'

type AuthMethod = 'none' | 'password' | 'mtls'

function ConnectionDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const navigate = useNavigate()
  const [name, setName] = useState('')
  const [url, setUrl] = useState('mqtts://broker.example.com:8883')
  const [replicas, setReplicas] = useState('1')
  const [method, setMethod] = useState<AuthMethod>('none')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('env:MQTT_PASSWORD')
  const [ca, setCa] = useState('')
  const [cert, setCert] = useState('')
  const [key, setKey] = useState('')
  const [error, setError] = useState<string>()
  const create = useAdminMutation(mqttApi.createConnection, [mqttKeys.all])

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(undefined)
    const auth: Record<string, string> = { method }
    if (method === 'password') {
      auth.username = username
      if (password) auth.password = password
    }
    const tls: Record<string, string> = {}
    if (ca) tls.ca = ca
    if (cert) tls.cert = cert
    if (key) tls.key = key
    try {
      const c = await create.mutateAsync({
        id: '',
        name,
        broker_url: url,
        replicas: Number(replicas),
        auth,
        tls,
        enabled: true,
      } as MqttConnection)
      toast.success(`Connection ${c.name} created`)
      onOpenChange(false)
      navigate(`/admin/mqtt/${c.id}`)
    } catch (err) {
      setError(formError(err))
    }
  }

  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title="New MQTT connection"
      description="Gateways with the mqtt role connect to this broker as MQTT 5 clients (subscribers). Secrets are references, never values."
      onSubmit={submit}
      submitting={create.isPending}
      formError={error}
      submitLabel="Create connection"
      className="sm:max-w-xl"
    >
      <div className="grid gap-4 sm:grid-cols-[2fr_1fr]">
        <Field label="Name" htmlFor="m-name" required>
          <Input id="m-name" value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label="Replicas" htmlFor="m-rep" description=">1: shared subscription">
          <Input
            id="m-rep"
            inputMode="numeric"
            value={replicas}
            onChange={(e) => setReplicas(e.target.value)}
          />
        </Field>
      </div>
      <Field label="Broker URL" htmlFor="m-url" required description="mqtts://, mqtt://, wss:// or ws://">
        <Input id="m-url" className="font-mono" value={url} onChange={(e) => setUrl(e.target.value)} />
      </Field>
      <Field label="Authentication">
        <Select value={method} onValueChange={(v) => setMethod(v as AuthMethod)}>
          <SelectTrigger aria-label="Authentication">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="none">None</SelectItem>
            <SelectItem value="password">Username and password</SelectItem>
            <SelectItem value="mtls">Client certificate (mTLS)</SelectItem>
          </SelectContent>
        </Select>
      </Field>
      {method === 'password' && (
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Username" htmlFor="m-user">
            <Input id="m-user" value={username} onChange={(e) => setUsername(e.target.value)} />
          </Field>
          <Field label="Password reference" htmlFor="m-pw" description="env:NAME or file:/path">
            <Input
              id="m-pw"
              className="font-mono"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
        </div>
      )}
      <div className="grid gap-4 sm:grid-cols-3">
        <Field label="CA bundle" htmlFor="m-ca" description="file:/… (optional)">
          <Input id="m-ca" className="font-mono" value={ca} onChange={(e) => setCa(e.target.value)} />
        </Field>
        <Field label="Client cert" htmlFor="m-cert">
          <Input id="m-cert" className="font-mono" value={cert} onChange={(e) => setCert(e.target.value)} />
        </Field>
        <Field label="Client key" htmlFor="m-key">
          <Input id="m-key" className="font-mono" value={key} onChange={(e) => setKey(e.target.value)} />
        </Field>
      </div>
    </FormDialog>
  )
}

function slotSummary(status: MqttStatus[], c: MqttConnection) {
  const mine = status.filter((s) => s.connection_id === c.id)
  const up = mine.filter((s) => s.connected).length
  const want = c.replicas || 1
  const err = mine.find((s) => !s.connected && s.last_error)?.last_error
  return { up, want, err, mine }
}

export function MqttConnectionsPage() {
  const conns = useMqttConnections()
  const status = useMqttStatus()
  const del = useAdminMutation(mqttApi.deleteConnection, [mqttKeys.all])
  const confirm = useConfirm()
  const [open, setOpen] = useState(false)

  const columns: Column<MqttConnection>[] = [
    {
      id: 'name',
      header: 'Name',
      sortValue: (c) => c.name.toLowerCase(),
      searchValue: (c) => `${c.name} ${c.broker_url}`,
      cell: (c) => (
        <Link to={`/admin/mqtt/${c.id}`} className="font-medium hover:underline">
          {c.name}
        </Link>
      ),
    },
    {
      id: 'broker',
      header: 'Broker',
      cell: (c) => <Mono>{c.broker_url}</Mono>,
    },
    {
      id: 'auth',
      header: 'Auth',
      cell: (c) => {
        const m = ((c.auth ?? {}) as { method?: string }).method ?? 'none'
        return <Badge variant="muted">{m}</Badge>
      },
    },
    {
      id: 'slots',
      header: 'Slots',
      cell: (c) => {
        const s = slotSummary(status.data ?? [], c)
        if (!c.enabled) return <Badge variant="muted">disabled</Badge>
        return (
          <div>
            <StatusDot ok={s.up === s.want} label={`${s.up}/${s.want} connected`} />
            {s.err && <div className="text-destructive max-w-xs truncate text-xs">{s.err}</div>}
          </div>
        )
      },
    },
    {
      id: 'gateways',
      header: 'Owned by',
      cell: (c) => (
        <div className="flex flex-wrap gap-1">
          {slotSummary(status.data ?? [], c).mine.map((s) => (
            <Badge key={s.slot} variant="outline" className="font-mono">
              {s.slot}: {s.gateway_id}
            </Badge>
          ))}
        </div>
      ),
    },
    {
      id: 'actions',
      header: <span className="sr-only">Actions</span>,
      headClassName: 'w-16',
      cell: (c) => (
        <RowActions>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={`Delete ${c.name}`}
            onClick={() =>
              confirm.ask({
                title: `Delete connection ${c.name}?`,
                description: 'Gateways disconnect from the broker, and its rules and grants are deleted.',
                destructive: true,
                onConfirm: async () => {
                  try {
                    await del.mutateAsync(c.id)
                    toast.success('Connection deleted')
                  } catch (err) {
                    toastError('Could not delete the connection', err)
                    throw err
                  }
                },
              })
            }
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
        title="MQTT"
        description="Brokers the gateways subscribe to. Messages become element values through each rule's decoder and field map."
        actions={
          <Button onClick={() => setOpen(true)}>
            <Plus /> New connection
          </Button>
        }
      />
      <MqttNav />
      <DataTable
        label="MQTT connections"
        data={conns.data}
        columns={columns}
        getRowId={(c) => c.id}
        isLoading={conns.isLoading}
        error={conns.error}
        onRetry={() => void conns.refetch()}
        searchPlaceholder="Search connections…"
        empty={{
          icon: RadioTower,
          title: 'No MQTT connections',
          description: 'Add a broker, grant it devices, then add uplink rules.',
        }}
      />
      <ConnectionDialog open={open} onOpenChange={setOpen} />
      {confirm.dialog}
    </Page>
  )
}
