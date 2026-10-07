import { zodResolver } from '@hookform/resolvers/zod'
import { Pencil, Plus, ShieldCheck, Trash2, Users } from 'lucide-react'
import { useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { toast } from 'sonner'
import { z } from 'zod'
import { useMe } from '@/api/queries'
import type { User } from '@/api/types'
import { useConfirm } from '@/components/ConfirmDialog'
import { DataTable, type Column } from '@/components/DataTable'
import { Page } from '@/components/layout/AppShell'
import { RelativeTime } from '@/components/RelativeTime'
import { PageHeader } from '@/components/states'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Hint } from '@/components/ui/tooltip'
import { applyProblem, toastError } from '@/lib/forms'
import { adminApi, adminKeys, useAdminMutation, useUsers } from './api'
import { FormDialog, RowActions } from './FormDialog'

const schema = (creating: boolean) =>
  z.object({
    username: z.string().trim().min(1, 'Required').max(150),
    email: z.union([z.literal(''), z.email('Enter a valid email').max(254)]),
    password: creating
      ? z.string().min(8, 'At least 8 characters').max(1024)
      : z.union([z.literal(''), z.string().min(8, 'At least 8 characters').max(1024)]),
    is_active: z.boolean(),
    is_admin: z.boolean(),
  })
type Values = z.infer<ReturnType<typeof schema>>

function UserDialog({
  user,
  open,
  onOpenChange,
}: {
  user: User | null
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const creating = !user
  const [formError, setFormError] = useState<string>()
  const form = useForm<Values>({
    resolver: zodResolver(schema(creating)),
    values: {
      username: user?.username ?? '',
      email: user?.email ?? '',
      password: '',
      is_active: user?.is_active ?? true,
      is_admin: user?.is_admin ?? false,
    },
  })
  const create = useAdminMutation(adminApi.createUser, [adminKeys.users])
  const update = useAdminMutation(adminApi.updateUser, [adminKeys.users])
  const { errors, isSubmitting } = form.formState

  const submit = form.handleSubmit(async (v) => {
    setFormError(undefined)
    try {
      if (creating) await create.mutateAsync(v)
      else {
        const { password, ...rest } = v
        await update.mutateAsync({ id: user.id, ...rest, ...(password ? { password } : {}) })
      }
      toast.success(creating ? `User ${v.username} created` : 'User updated')
      onOpenChange(false)
    } catch (e) {
      setFormError(applyProblem(e, form.setError, ['username', 'email', 'password']))
    }
  })

  return (
    <FormDialog
      open={open}
      onOpenChange={(o) => {
        if (!o) setFormError(undefined)
        onOpenChange(o)
      }}
      title={creating ? 'New user' : `Edit ${user.username}`}
      onSubmit={submit}
      submitting={isSubmitting}
      formError={formError}
      submitLabel={creating ? 'Create user' : 'Save changes'}
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Username" htmlFor="u-username" error={errors.username?.message} required>
          <Input
            id="u-username"
            autoComplete="off"
            aria-invalid={!!errors.username}
            {...form.register('username')}
          />
        </Field>
        <Field label="Email" htmlFor="u-email" error={errors.email?.message}>
          <Input id="u-email" type="email" aria-invalid={!!errors.email} {...form.register('email')} />
        </Field>
      </div>
      <Field
        label={creating ? 'Password' : 'New password'}
        htmlFor="u-password"
        error={errors.password?.message}
        description={creating ? 'At least 8 characters.' : 'Leave empty to keep the current password.'}
        required={creating}
      >
        <Input
          id="u-password"
          type="password"
          autoComplete="new-password"
          aria-invalid={!!errors.password}
          {...form.register('password')}
        />
      </Field>
      <div className="grid gap-2 rounded-lg border p-3">
        <Controller
          control={form.control}
          name="is_active"
          render={({ field }) => (
            <label className="flex items-center justify-between gap-4 text-sm">
              <span>
                <span className="font-medium">Active</span>
                <span className="text-muted-foreground block text-xs">Inactive users cannot sign in.</span>
              </span>
              <Switch checked={field.value} onCheckedChange={field.onChange} aria-label="Active" />
            </label>
          )}
        />
        <Controller
          control={form.control}
          name="is_admin"
          render={({ field }) => (
            <label className="flex items-center justify-between gap-4 text-sm">
              <span>
                <span className="font-medium">Administrator</span>
                <span className="text-muted-foreground block text-xs">Full access to the admin section.</span>
              </span>
              <Switch checked={field.value} onCheckedChange={field.onChange} aria-label="Administrator" />
            </label>
          )}
        />
      </div>
    </FormDialog>
  )
}

export function UsersPage() {
  const me = useMe()
  const users = useUsers()
  const del = useAdminMutation(adminApi.deleteUser, [adminKeys.users])
  const confirm = useConfirm()
  const [editing, setEditing] = useState<User | null>(null)
  const [open, setOpen] = useState(false)

  const columns: Column<User>[] = [
    {
      id: 'username',
      header: 'Username',
      sortValue: (u) => u.username.toLowerCase(),
      searchValue: (u) => u.username,
      cell: (u) => (
        <div className="flex items-center gap-2 font-medium">
          {u.username}
          {u.id === me.data?.id && <Badge variant="muted">you</Badge>}
        </div>
      ),
    },
    {
      id: 'email',
      header: 'Email',
      sortValue: (u) => u.email,
      searchValue: (u) => u.email,
      cell: (u) => u.email || <span className="text-muted-foreground">—</span>,
    },
    {
      id: 'role',
      header: 'Role',
      sortValue: (u) => (u.is_admin ? 0 : 1),
      cell: (u) =>
        u.is_admin ? (
          <Badge variant="secondary">
            <ShieldCheck /> Admin
          </Badge>
        ) : (
          <span className="text-muted-foreground">User</span>
        ),
    },
    {
      id: 'status',
      header: 'Status',
      sortValue: (u) => (u.is_active ? 0 : 1),
      cell: (u) => (
        <Badge variant={u.is_active ? 'success' : 'muted'}>{u.is_active ? 'Active' : 'Inactive'}</Badge>
      ),
    },
    {
      id: 'last_login',
      header: 'Last login',
      sortValue: (u) => u.last_login_at ?? '',
      cell: (u) =>
        u.last_login_at ? (
          <RelativeTime value={u.last_login_at} />
        ) : (
          <span className="text-muted-foreground">never</span>
        ),
    },
    {
      id: 'created',
      header: 'Created',
      sortValue: (u) => u.created_at,
      cell: (u) => <RelativeTime value={u.created_at} className="text-muted-foreground" />,
    },
    {
      id: 'actions',
      header: <span className="sr-only">Actions</span>,
      headClassName: 'w-20',
      cell: (u) => (
        <RowActions>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={`Edit ${u.username}`}
            onClick={() => {
              setEditing(u)
              setOpen(true)
            }}
          >
            <Pencil />
          </Button>
          <Hint label={u.id === me.data?.id ? 'You cannot delete yourself' : `Delete ${u.username}`}>
            <span>
              <Button
                variant="ghost"
                size="icon-xs"
                aria-label={`Delete ${u.username}`}
                disabled={u.id === me.data?.id}
                onClick={() =>
                  confirm.ask({
                    title: `Delete user ${u.username}?`,
                    description:
                      'Their dashboards, sessions and direct permissions are removed. This cannot be undone.',
                    destructive: true,
                    onConfirm: async () => {
                      try {
                        await del.mutateAsync(u.id)
                        toast.success('User deleted')
                      } catch (e) {
                        toastError('Could not delete user', e)
                        throw e
                      }
                    },
                  })
                }
              >
                <Trash2 />
              </Button>
            </span>
          </Hint>
        </RowActions>
      ),
    },
  ]

  return (
    <Page>
      <PageHeader
        title="Users"
        description="People who can sign in to Quack Quack."
        actions={
          <Button
            onClick={() => {
              setEditing(null)
              setOpen(true)
            }}
          >
            <Plus /> New user
          </Button>
        }
      />
      <DataTable
        label="Users"
        data={users.data}
        columns={columns}
        getRowId={(u) => u.id}
        isLoading={users.isLoading}
        error={users.error}
        onRetry={() => void users.refetch()}
        searchPlaceholder="Search users…"
        initialSort={{ id: 'username', dir: 'asc' }}
        empty={{ icon: Users, title: 'No users' }}
      />
      <UserDialog user={editing} open={open} onOpenChange={setOpen} />
      {confirm.dialog}
    </Page>
  )
}
