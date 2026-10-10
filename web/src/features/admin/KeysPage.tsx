import { zodResolver } from '@hookform/resolvers/zod'
import { FileUp, KeyRound, Pencil, Plus, Trash2 } from 'lucide-react'
import { useRef, useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { toast } from 'sonner'
import { z } from 'zod'
import type { Key } from '@/api/types'
import { useConfirm } from '@/components/ConfirmDialog'
import { DataTable, Mono, type Column } from '@/components/DataTable'
import { Page } from '@/components/layout/AppShell'
import { RelativeTime } from '@/components/RelativeTime'
import { PageHeader } from '@/components/states'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { applyProblem, toastError } from '@/lib/forms'
import { adminApi, adminKeys, useAdminDevices, useAdminMutation, useKeys } from './api'
import { FormDialog, RowActions } from './FormDialog'

const createSchema = z.object({
  name: z.string().trim().min(1, 'Required').max(100),
  pem: z
    .string()
    .trim()
    .min(1, 'Paste or upload a PEM public key')
    .max(16384)
    .refine(
      (v) => /-----BEGIN [A-Z ]*PUBLIC KEY-----/.test(v),
      'Expected a PEM public key (-----BEGIN PUBLIC KEY-----)',
    ),
})
const editSchema = z.object({ name: z.string().trim().min(1, 'Required').max(100), is_active: z.boolean() })

function CreateKeyDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const [formError, setFormError] = useState<string>()
  const fileRef = useRef<HTMLInputElement>(null)
  const form = useForm({ resolver: zodResolver(createSchema), defaultValues: { name: '', pem: '' } })
  const create = useAdminMutation(adminApi.createKey, [adminKeys.keys])
  const { errors, isSubmitting } = form.formState
  const submit = form.handleSubmit(async (v) => {
    setFormError(undefined)
    try {
      const k = await create.mutateAsync({ name: v.name, pem: v.pem.trim() + '\n' })
      toast.success(`Key ${k.name} added`, { description: `Detected ${k.algorithm} · ${k.key_size}-bit` })
      form.reset()
      onOpenChange(false)
    } catch (e) {
      setFormError(applyProblem(e, form.setError, ['name', 'pem']))
    }
  })
  return (
    <FormDialog
      open={open}
      onOpenChange={(o) => {
        if (!o) {
          form.reset()
          setFormError(undefined)
        }
        onOpenChange(o)
      }}
      title="Add public key"
      description="Devices sign their JWTs with the matching private key. RSA ≥ 2048 bits (RS256) or ECDSA P-256 (ES256); the algorithm is detected automatically."
      onSubmit={submit}
      submitting={isSubmitting}
      formError={formError}
      submitLabel="Add key"
      className="sm:max-w-xl"
    >
      <Field label="Name" htmlFor="k-name" error={errors.name?.message} required>
        <Input
          id="k-name"
          placeholder="e.g. greenhouse-a-2026"
          aria-invalid={!!errors.name}
          {...form.register('name')}
        />
      </Field>
      <Field label="PEM public key" htmlFor="k-pem" error={errors.pem?.message} required>
        <Textarea
          id="k-pem"
          rows={8}
          spellCheck={false}
          placeholder={'-----BEGIN PUBLIC KEY-----\n…\n-----END PUBLIC KEY-----'}
          className="field-sizing-fixed font-mono text-xs"
          aria-invalid={!!errors.pem}
          {...form.register('pem')}
        />
      </Field>
      <div>
        <input
          ref={fileRef}
          type="file"
          accept=".pem,.pub,.key,.txt,application/x-pem-file"
          className="hidden"
          onChange={async (e) => {
            const f = e.target.files?.[0]
            if (!f) return
            const text = await f.text()
            form.setValue('pem', text, { shouldValidate: true })
            if (!form.getValues('name')) form.setValue('name', f.name.replace(/\.(pem|pub|key|txt)$/i, ''))
            e.target.value = ''
          }}
        />
        <Button variant="outline" size="sm" onClick={() => fileRef.current?.click()}>
          <FileUp /> Upload .pem file
        </Button>
      </div>
    </FormDialog>
  )
}

function EditKeyDialog({ k, onOpenChange }: { k: Key | null; onOpenChange: (o: boolean) => void }) {
  const [formError, setFormError] = useState<string>()
  const form = useForm({
    resolver: zodResolver(editSchema),
    values: { name: k?.name ?? '', is_active: k?.is_active ?? true },
  })
  const update = useAdminMutation(adminApi.updateKey, [adminKeys.keys])
  const submit = form.handleSubmit(async (v) => {
    if (!k) return
    setFormError(undefined)
    try {
      await update.mutateAsync({ id: k.id, ...v })
      toast.success('Key updated')
      onOpenChange(false)
    } catch (e) {
      setFormError(applyProblem(e, form.setError, ['name']))
    }
  })
  return (
    <FormDialog
      open={!!k}
      onOpenChange={onOpenChange}
      title="Edit key"
      onSubmit={submit}
      submitting={form.formState.isSubmitting}
      formError={formError}
    >
      <Field label="Name" htmlFor="ke-name" error={form.formState.errors.name?.message} required>
        <Input id="ke-name" {...form.register('name')} />
      </Field>
      <Controller
        control={form.control}
        name="is_active"
        render={({ field }) => (
          <label className="flex items-center justify-between gap-4 rounded-lg border p-3 text-sm">
            <span>
              <span className="font-medium">Active</span>
              <span className="text-muted-foreground block text-xs">
                Devices using an inactive key are rejected.
              </span>
            </span>
            <Switch checked={field.value} onCheckedChange={field.onChange} aria-label="Active" />
          </label>
        )}
      />
      {k && (
        <pre className="bg-muted text-muted-foreground max-h-40 overflow-auto rounded-md p-3 font-mono text-[11px] leading-relaxed">
          {k.pem}
        </pre>
      )}
    </FormDialog>
  )
}

export function KeysPage() {
  const keys = useKeys()
  const devices = useAdminDevices()
  const del = useAdminMutation(adminApi.deleteKey, [adminKeys.keys, adminKeys.devices])
  const toggle = useAdminMutation(adminApi.updateKey, [adminKeys.keys])
  const confirm = useConfirm()
  const [createOpen, setCreateOpen] = useState(false)
  const [editing, setEditing] = useState<Key | null>(null)

  const usedBy = (id: string) => (devices.data ?? []).filter((d) => d.public_key_id === id)

  const columns: Column<Key>[] = [
    {
      id: 'name',
      header: 'Name',
      sortValue: (k) => k.name.toLowerCase(),
      searchValue: (k) => k.name,
      cell: (k) => <span className="font-medium">{k.name}</span>,
    },
    {
      id: 'alg',
      header: 'Algorithm',
      sortValue: (k) => k.algorithm,
      searchValue: (k) => k.algorithm,
      cell: (k) => (
        <Badge variant="outline" className="font-mono">
          {k.algorithm} · {k.key_size}
        </Badge>
      ),
    },
    {
      id: 'devices',
      header: 'Used by',
      sortValue: (k) => usedBy(k.id).length,
      cell: (k) => {
        const used = usedBy(k.id)
        return used.length ? (
          <span className="text-sm">{used.map((d) => d.name).join(', ')}</span>
        ) : (
          <span className="text-muted-foreground">—</span>
        )
      },
    },
    {
      id: 'active',
      header: 'Active',
      sortValue: (k) => (k.is_active ? 0 : 1),
      cell: (k) => (
        <Switch
          checked={k.is_active}
          aria-label={`${k.name} active`}
          onCheckedChange={async (v) => {
            try {
              await toggle.mutateAsync({ id: k.id, is_active: v })
              toast.success(v ? 'Key activated' : 'Key deactivated')
            } catch (e) {
              toastError('Could not update key', e)
            }
          }}
        />
      ),
    },
    { id: 'id', header: 'ID', cell: (k) => <Mono>{k.id.slice(0, 8)}</Mono> },
    {
      id: 'created',
      header: 'Added',
      sortValue: (k) => k.created_at,
      cell: (k) => <RelativeTime value={k.created_at} className="text-muted-foreground" />,
    },
    {
      id: 'actions',
      header: <span className="sr-only">Actions</span>,
      headClassName: 'w-20',
      cell: (k) => (
        <RowActions>
          <Button variant="ghost" size="icon-xs" aria-label={`Edit ${k.name}`} onClick={() => setEditing(k)}>
            <Pencil />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={`Delete ${k.name}`}
            onClick={() =>
              confirm.ask({
                title: `Delete key ${k.name}?`,
                description: usedBy(k.id).length
                  ? `It is assigned to ${usedBy(k.id)
                      .map((d) => d.name)
                      .join(', ')}. Those devices will no longer be able to connect.`
                  : 'Devices signing with this key will no longer be able to connect.',
                destructive: true,
                onConfirm: async () => {
                  try {
                    await del.mutateAsync(k.id)
                    toast.success('Key deleted')
                  } catch (e) {
                    toastError('Could not delete key', e)
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
            <img src="/brand/logo-keys.svg" alt="" className="size-7 rounded-lg shadow-xs" />
            <span>Keys</span>
          </div>
        }
        description="Public keys that devices use to authenticate."
        actions={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus /> Add key
          </Button>
        }
      />
      <DataTable
        label="Keys"
        data={keys.data}
        columns={columns}
        getRowId={(k) => k.id}
        isLoading={keys.isLoading}
        error={keys.error}
        onRetry={() => void keys.refetch()}
        searchPlaceholder="Search keys…"
        initialSort={{ id: 'name', dir: 'asc' }}
        empty={{
          icon: KeyRound,
          title: 'No keys yet',
          description: 'Add a device public key to let devices connect.',
        }}
      />
      <CreateKeyDialog open={createOpen} onOpenChange={setCreateOpen} />
      <EditKeyDialog k={editing} onOpenChange={(o) => !o && setEditing(null)} />
      {confirm.dialog}
    </Page>
  )
}
