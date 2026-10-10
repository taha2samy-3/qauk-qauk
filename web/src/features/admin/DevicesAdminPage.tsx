import { zodResolver } from '@hookform/resolvers/zod'
import { Pencil, Plus, Radio, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { Link } from 'react-router'
import { toast } from 'sonner'
import { z } from 'zod'
import type { Device } from '@/api/types'
import { useConfirm } from '@/components/ConfirmDialog'
import { DataTable, Mono, type Column } from '@/components/DataTable'
import { Page } from '@/components/layout/AppShell'
import { RelativeTime } from '@/components/RelativeTime'
import { PageHeader } from '@/components/states'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { applyProblem, toastError } from '@/lib/forms'
import { adminApi, adminKeys, useAdminDevices, useAdminMutation, useKeys, usePresence } from './api'
import { FormDialog, RowActions } from './FormDialog'

const NONE = '__none__'
const schema = z.object({
  name: z.string().trim().min(1, 'Required').max(50, 'At most 50 characters'),
  description: z.string().max(2000),
  public_key_id: z.string(),
})
type Values = z.infer<typeof schema>

function DeviceDialog({
  device,
  open,
  onOpenChange,
}: {
  device: Device | null
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const keys = useKeys()
  const [formError, setFormError] = useState<string>()
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    values: {
      name: device?.name ?? '',
      description: device?.description ?? '',
      public_key_id: device?.public_key_id || NONE,
    },
  })
  const create = useAdminMutation(adminApi.createDevice, [adminKeys.devices])
  const update = useAdminMutation(adminApi.updateDevice, [adminKeys.devices])
  const { errors, isSubmitting } = form.formState
  const submit = form.handleSubmit(async (v) => {
    setFormError(undefined)
    const key = v.public_key_id === NONE ? '' : v.public_key_id
    try {
      if (device)
        await update.mutateAsync({
          id: device.id,
          name: v.name,
          description: v.description,
          public_key_id: key,
        })
      else
        await create.mutateAsync({
          name: v.name,
          description: v.description,
          ...(key ? { public_key_id: key } : {}),
        })
      toast.success(device ? 'Device updated' : `Device ${v.name} created`)
      onOpenChange(false)
    } catch (e) {
      setFormError(applyProblem(e, form.setError, ['name', 'description', 'public_key_id']))
    }
  })
  return (
    <FormDialog
      open={open}
      onOpenChange={(o) => {
        if (!o) setFormError(undefined)
        onOpenChange(o)
      }}
      title={device ? `Edit ${device.name}` : 'New device'}
      onSubmit={submit}
      submitting={isSubmitting}
      formError={formError}
      submitLabel={device ? 'Save changes' : 'Create device'}
    >
      <Field label="Name" htmlFor="d-name" error={errors.name?.message} required>
        <Input id="d-name" aria-invalid={!!errors.name} {...form.register('name')} />
      </Field>
      <Field label="Description" htmlFor="d-desc" error={errors.description?.message}>
        <Textarea id="d-desc" rows={2} {...form.register('description')} />
      </Field>
      <Field
        label="Public key"
        error={errors.public_key_id?.message}
        description="The key the device signs its connection token with."
      >
        <Controller
          control={form.control}
          name="public_key_id"
          render={({ field }) => (
            <Select value={field.value} onValueChange={field.onChange}>
              <SelectTrigger aria-label="Public key">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NONE}>No key (device cannot connect)</SelectItem>
                {(keys.data ?? []).map((k) => (
                  <SelectItem key={k.id} value={k.id} disabled={!k.is_active}>
                    {k.name} · {k.algorithm}
                    {!k.is_active && ' (inactive)'}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        />
      </Field>
    </FormDialog>
  )
}

export function DevicesAdminPage() {
  const devices = useAdminDevices()
  const keys = useKeys()
  const presence = usePresence(10_000)
  const del = useAdminMutation(adminApi.deleteDevice, [adminKeys.devices, ['admin', 'elements']])
  const confirm = useConfirm()
  const [editing, setEditing] = useState<Device | null>(null)
  const [open, setOpen] = useState(false)
  const keyName = (id: string) => keys.data?.find((k) => k.id === id)?.name
  const online = (id: string) => (presence.data ?? []).find((p) => p.device_id === id)

  const columns: Column<Device>[] = [
    {
      id: 'name',
      header: 'Name',
      sortValue: (d) => d.name.toLowerCase(),
      searchValue: (d) => d.name,
      cell: (d) => (
        <div>
          <div className="font-medium">{d.name}</div>
          {d.description && (
            <div className="text-muted-foreground max-w-xs truncate text-xs">{d.description}</div>
          )}
        </div>
      ),
    },
    {
      id: 'status',
      header: 'Status',
      sortValue: (d) => (online(d.id) ? 0 : 1),
      cell: (d) => {
        const p = online(d.id)
        return p ? (
          <Badge variant="success" title={`Gateway ${p.gateway_id}`}>
            Online · <RelativeTime value={p.connected_at} />
          </Badge>
        ) : (
          <Badge variant="muted">Offline</Badge>
        )
      },
    },
    {
      id: 'key',
      header: 'Key',
      sortValue: (d) => keyName(d.public_key_id) ?? '',
      searchValue: (d) => keyName(d.public_key_id) ?? '',
      cell: (d) =>
        d.public_key_id ? (
          (keyName(d.public_key_id) ?? <Mono>{d.public_key_id.slice(0, 8)}</Mono>)
        ) : (
          <span className="text-destructive">No key</span>
        ),
    },
    {
      id: 'id',
      header: 'Device ID',
      searchValue: (d) => d.id,
      cell: (d) => <Mono className="select-all">{d.id}</Mono>,
    },
    {
      id: 'actions',
      header: <span className="sr-only">Actions</span>,
      headClassName: 'w-28',
      cell: (d) => (
        <RowActions>
          <Button variant="ghost" size="xs" asChild>
            <Link to={`/admin/elements?device=${d.id}`}>Elements</Link>
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={`Edit ${d.name}`}
            onClick={() => {
              setEditing(d)
              setOpen(true)
            }}
          >
            <Pencil />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={`Delete ${d.name}`}
            onClick={() =>
              confirm.ask({
                title: `Delete device ${d.name}?`,
                description:
                  'Its elements, their permissions and styles are deleted too. Connected sessions are closed.',
                destructive: true,
                onConfirm: async () => {
                  try {
                    await del.mutateAsync(d.id)
                    toast.success('Device deleted')
                  } catch (e) {
                    toastError('Could not delete device', e)
                    throw e
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
    <Page>
      <PageHeader
        title={
          <div className="flex items-center gap-3">
            <img src="/brand/logo-device.svg" alt="" className="size-7 rounded-lg shadow-xs" />
            <span>Devices</span>
          </div>
        }
        description="Physical or virtual devices and the keys they authenticate with."
        actions={
          <Button
            onClick={() => {
              setEditing(null)
              setOpen(true)
            }}
          >
            <Plus /> New device
          </Button>
        }
      />
      <DataTable
        label="Devices"
        data={devices.data}
        columns={columns}
        getRowId={(d) => d.id}
        isLoading={devices.isLoading}
        error={devices.error}
        onRetry={() => void devices.refetch()}
        searchPlaceholder="Search devices…"
        initialSort={{ id: 'name', dir: 'asc' }}
        empty={{ icon: Radio, title: 'No devices yet' }}
      />
      <DeviceDialog device={editing} open={open} onOpenChange={setOpen} />
      {confirm.dialog}
    </Page>
  )
}
