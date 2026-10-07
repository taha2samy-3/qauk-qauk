import { zodResolver } from '@hookform/resolvers/zod'
import { Lock, Plus, Trash2, User as UserIcon, UsersRound } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Controller, useForm, useWatch } from 'react-hook-form'
import { toast } from 'sonner'
import { z } from 'zod'
import type { PermissionRow } from '@/api/types'
import { useConfirm } from '@/components/ConfirmDialog'
import { DataTable, type Column } from '@/components/DataTable'
import { Page } from '@/components/layout/AppShell'
import { PageHeader } from '@/components/states'
import { Field } from '@/components/ui/field'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { applyProblem, toastError } from '@/lib/forms'
import { cn } from '@/lib/utils'
import {
  adminApi,
  useAdminDevices,
  useAdminElements,
  useAdminMutation,
  useGroups,
  usePermissions,
  useUsers,
} from './api'
import { FormDialog, RowActions } from './FormDialog'

const ANY = '__any__'
const PERMS = { R: 'Read', RC: 'Read & control' } as const

const schema = z.object({
  element_id: z.string().min(1, 'Choose an element'),
  kind: z.enum(['user', 'group']),
  subject: z.string().min(1, 'Choose who gets access'),
  permission: z.enum(['R', 'RC']),
})
type Values = z.infer<typeof schema>

function useElementOptions() {
  const devices = useAdminDevices()
  const elements = useAdminElements()
  return useMemo(() => {
    const byDevice = new Map<string, { id: string; name: string }[]>()
    for (const e of elements.data ?? []) {
      const list = byDevice.get(e.device_id) ?? []
      list.push({ id: e.id, name: e.name })
      byDevice.set(e.device_id, list)
    }
    const deviceName = (id: string) => devices.data?.find((d) => d.id === id)?.name ?? id.slice(0, 8)
    const groups = [...byDevice].map(([id, els]) => ({
      device: deviceName(id),
      elements: els.sort((a, b) => a.name.localeCompare(b.name)),
    }))
    groups.sort((a, b) => a.device.localeCompare(b.device))
    const label = new Map<string, { element: string; device: string }>()
    for (const g of groups) for (const e of g.elements) label.set(e.id, { element: e.name, device: g.device })
    return { groups, label }
  }, [devices.data, elements.data])
}

function PermissionBadge({ p }: { p: string }) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-xs font-medium',
        p === 'RC' ? 'bg-chart-1/15 text-chart-1' : 'bg-muted text-muted-foreground',
      )}
    >
      {p === 'RC' ? 'RC' : 'R'} · {PERMS[p as 'R' | 'RC'] ?? p}
    </span>
  )
}

function GrantDialog({
  open,
  onOpenChange,
  presetElement,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  presetElement?: string
}) {
  const users = useUsers()
  const groups = useGroups()
  const { groups: elementGroups } = useElementOptions()
  const [formError, setFormError] = useState<string>()
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    values: { element_id: presetElement ?? '', kind: 'group', subject: '', permission: 'R' },
  })
  const set = useAdminMutation(adminApi.setPermission, [['admin', 'permissions']])
  const kind = useWatch({ control: form.control, name: 'kind' })
  const { errors, isSubmitting } = form.formState

  const submit = form.handleSubmit(async (v) => {
    setFormError(undefined)
    try {
      await set.mutateAsync({
        element_id: v.element_id,
        permission: v.permission,
        ...(v.kind === 'user' ? { user_id: Number(v.subject) } : { group_id: Number(v.subject) }),
      })
      toast.success('Permission granted')
      onOpenChange(false)
    } catch (e) {
      setFormError(applyProblem(e, form.setError, ['element_id', 'permission']))
    }
  })

  return (
    <FormDialog
      open={open}
      onOpenChange={(o) => {
        if (!o) setFormError(undefined)
        onOpenChange(o)
      }}
      title="Grant permission"
      description="If the subject already has a permission on the element it is replaced."
      onSubmit={submit}
      submitting={isSubmitting}
      formError={formError}
      submitLabel="Grant"
    >
      <Field label="Element" error={errors.element_id?.message} required>
        <Controller
          control={form.control}
          name="element_id"
          render={({ field }) => (
            <Select value={field.value} onValueChange={field.onChange}>
              <SelectTrigger aria-label="Element" aria-invalid={!!errors.element_id}>
                <SelectValue placeholder="Choose an element…" />
              </SelectTrigger>
              <SelectContent>
                {elementGroups.map((g) => (
                  <SelectGroup key={g.device}>
                    <SelectLabel>{g.device}</SelectLabel>
                    {g.elements.map((e) => (
                      <SelectItem key={e.id} value={e.id}>
                        {e.name}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                ))}
              </SelectContent>
            </Select>
          )}
        />
      </Field>
      <Field label="Grant to" error={errors.subject?.message} required>
        <div className="flex gap-2">
          <Controller
            control={form.control}
            name="kind"
            render={({ field }) => (
              <Tabs
                value={field.value}
                onValueChange={(v) => {
                  field.onChange(v)
                  form.setValue('subject', '')
                }}
              >
                <TabsList>
                  <TabsTrigger value="group">
                    <UsersRound className="size-3.5" /> Group
                  </TabsTrigger>
                  <TabsTrigger value="user">
                    <UserIcon className="size-3.5" /> User
                  </TabsTrigger>
                </TabsList>
              </Tabs>
            )}
          />
          <Controller
            control={form.control}
            name="subject"
            render={({ field }) => (
              <Select value={field.value} onValueChange={field.onChange}>
                <SelectTrigger
                  aria-label={kind === 'user' ? 'User' : 'Group'}
                  className="flex-1"
                  aria-invalid={!!errors.subject}
                >
                  <SelectValue placeholder={kind === 'user' ? 'Choose a user…' : 'Choose a group…'} />
                </SelectTrigger>
                <SelectContent>
                  {kind === 'user'
                    ? (users.data ?? []).map((u) => (
                        <SelectItem key={u.id} value={String(u.id)}>
                          {u.username}
                        </SelectItem>
                      ))
                    : (groups.data ?? []).map((g) => (
                        <SelectItem key={g.id} value={String(g.id)}>
                          {g.name}
                        </SelectItem>
                      ))}
                </SelectContent>
              </Select>
            )}
          />
        </div>
      </Field>
      <Field label="Permission">
        <Controller
          control={form.control}
          name="permission"
          render={({ field }) => (
            <div role="radiogroup" aria-label="Permission" className="grid grid-cols-2 gap-2">
              {(['R', 'RC'] as const).map((p) => (
                <button
                  key={p}
                  type="button"
                  role="radio"
                  aria-checked={field.value === p}
                  onClick={() => field.onChange(p)}
                  className={cn(
                    'focus-visible:ring-ring/50 rounded-lg border p-3 text-left text-sm transition-colors focus-visible:ring-[3px] focus-visible:outline-none',
                    field.value === p ? 'border-primary bg-accent' : 'hover:bg-accent/50',
                  )}
                >
                  <div className="font-medium">{PERMS[p]}</div>
                  <div className="text-muted-foreground text-xs">
                    {p === 'R' ? 'See live values and history' : 'Also send commands (switches, sliders)'}
                  </div>
                </button>
              ))}
            </div>
          )}
        />
      </Field>
    </FormDialog>
  )
}

export function PermissionsPage() {
  const [element, setElement] = useState(ANY)
  const [user, setUser] = useState(ANY)
  const [group, setGroup] = useState(ANY)
  const filter = {
    ...(element !== ANY ? { element_id: element } : {}),
    ...(user !== ANY ? { user_id: Number(user) } : {}),
    ...(group !== ANY ? { group_id: Number(group) } : {}),
  }
  const perms = usePermissions(filter)
  const users = useUsers()
  const groups = useGroups()
  const { groups: elementGroups, label } = useElementOptions()
  const set = useAdminMutation(adminApi.setPermission, [['admin', 'permissions']])
  const del = useAdminMutation(adminApi.deletePermission, [['admin', 'permissions']])
  const confirm = useConfirm()
  const [grantOpen, setGrantOpen] = useState(false)

  const subject = (p: PermissionRow) =>
    p.user_id !== null
      ? {
          kind: 'user' as const,
          name: users.data?.find((u) => u.id === p.user_id)?.username ?? `user #${p.user_id}`,
        }
      : {
          kind: 'group' as const,
          name: groups.data?.find((g) => g.id === p.group_id)?.name ?? `group #${p.group_id}`,
        }

  const columns: Column<PermissionRow>[] = [
    {
      id: 'element',
      header: 'Element',
      sortValue: (p) => `${label.get(p.element_id)?.device ?? ''} ${label.get(p.element_id)?.element ?? ''}`,
      searchValue: (p) =>
        `${label.get(p.element_id)?.device ?? ''} ${label.get(p.element_id)?.element ?? ''}`,
      cell: (p) => (
        <div>
          <div className="font-medium">{label.get(p.element_id)?.element ?? p.element_id.slice(0, 8)}</div>
          <div className="text-muted-foreground text-xs">{label.get(p.element_id)?.device}</div>
        </div>
      ),
    },
    {
      id: 'subject',
      header: 'Granted to',
      sortValue: (p) => subject(p).name,
      searchValue: (p) => subject(p).name,
      cell: (p) => {
        const s = subject(p)
        return (
          <span className="inline-flex items-center gap-1.5">
            {s.kind === 'user' ? (
              <UserIcon className="text-muted-foreground size-3.5" />
            ) : (
              <UsersRound className="text-muted-foreground size-3.5" />
            )}
            {s.name}
            <span className="text-muted-foreground text-xs">({s.kind})</span>
          </span>
        )
      },
    },
    {
      id: 'permission',
      header: 'Permission',
      sortValue: (p) => p.permission,
      cell: (p) => (
        <Select
          value={p.permission}
          onValueChange={async (v) => {
            try {
              await set.mutateAsync({
                element_id: p.element_id,
                permission: v as 'R' | 'RC',
                ...(p.user_id !== null ? { user_id: p.user_id } : { group_id: p.group_id! }),
              })
              toast.success('Permission updated')
            } catch (e) {
              toastError('Could not update permission', e)
            }
          }}
        >
          <SelectTrigger
            size="sm"
            className="hover:bg-accent w-auto gap-2 border-transparent bg-transparent px-1 shadow-none"
            aria-label="Change permission"
          >
            <PermissionBadge p={p.permission} />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="R">Read</SelectItem>
            <SelectItem value="RC">Read &amp; control</SelectItem>
          </SelectContent>
        </Select>
      ),
    },
    {
      id: 'actions',
      header: <span className="sr-only">Actions</span>,
      headClassName: 'w-12',
      cell: (p) => (
        <RowActions>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label="Revoke permission"
            onClick={() =>
              confirm.ask({
                title: 'Revoke permission?',
                description: `${subject(p).name} loses ${PERMS[p.permission as 'R' | 'RC'] ?? p.permission} access to ${label.get(p.element_id)?.element ?? 'this element'}. Live subscriptions are closed immediately.`,
                destructive: true,
                confirmLabel: 'Revoke',
                onConfirm: async () => {
                  try {
                    await del.mutateAsync(p.id)
                    toast.success('Permission revoked')
                  } catch (e) {
                    toastError('Could not revoke permission', e)
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

  const filterSelect = (
    value: string,
    onChange: (v: string) => void,
    placeholder: string,
    items: { value: string; label: string }[],
    width = 'w-40',
  ) => (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger className={width} aria-label={`Filter by ${placeholder.toLowerCase()}`}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value={ANY}>Any {placeholder.toLowerCase()}</SelectItem>
        {items.map((i) => (
          <SelectItem key={i.value} value={i.value}>
            {i.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )

  return (
    <Page wide>
      <PageHeader
        title="Permissions"
        description="Who can read (R) or read & control (RC) each element. The highest of a user’s direct and group permissions applies."
        actions={
          <Button onClick={() => setGrantOpen(true)}>
            <Plus /> Grant permission
          </Button>
        }
      />
      <DataTable
        label="Permissions"
        data={perms.data}
        columns={columns}
        getRowId={(p) => p.id}
        isLoading={perms.isLoading}
        error={perms.error}
        onRetry={() => void perms.refetch()}
        searchPlaceholder="Search element or subject…"
        initialSort={{ id: 'element', dir: 'asc' }}
        pageSize={20}
        empty={{
          icon: Lock,
          title: 'No permissions match',
          description: 'Grant a user or group access to an element.',
        }}
        toolbar={
          <>
            {filterSelect(
              element,
              setElement,
              'Element',
              elementGroups.flatMap((g) =>
                g.elements.map((e) => ({ value: e.id, label: `${g.device} · ${e.name}` })),
              ),
              'w-52',
            )}
            {filterSelect(
              user,
              (v) => {
                setUser(v)
                if (v !== ANY) setGroup(ANY)
              },
              'User',
              (users.data ?? []).map((u) => ({ value: String(u.id), label: u.username })),
            )}
            {filterSelect(
              group,
              (v) => {
                setGroup(v)
                if (v !== ANY) setUser(ANY)
              },
              'Group',
              (groups.data ?? []).map((g) => ({ value: String(g.id), label: g.name })),
            )}
          </>
        }
      />
      <GrantDialog
        open={grantOpen}
        onOpenChange={setGrantOpen}
        presetElement={element !== ANY ? element : undefined}
      />
      {confirm.dialog}
    </Page>
  )
}
