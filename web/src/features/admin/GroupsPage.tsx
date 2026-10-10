import { zodResolver } from '@hookform/resolvers/zod'
import { Pencil, Plus, Trash2, UserMinus, UserPlus, UsersRound } from 'lucide-react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { toast } from 'sonner'
import { z } from 'zod'
import type { Group } from '@/api/types'
import { useConfirm } from '@/components/ConfirmDialog'
import { DataTable, type Column } from '@/components/DataTable'
import { Page } from '@/components/layout/AppShell'
import { EmptyState, ErrorState, PageHeader, TableSkeleton } from '@/components/states'
import { Button } from '@/components/ui/button'
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
import { applyProblem, toastError } from '@/lib/forms'
import { adminApi, adminKeys, useAdminMutation, useGroupMembers, useGroups, useUsers } from './api'
import { FormDialog, RowActions } from './FormDialog'

const schema = z.object({ name: z.string().trim().min(1, 'Required').max(150) })

function GroupDialog({
  group,
  open,
  onOpenChange,
}: {
  group: Group | null
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const [formError, setFormError] = useState<string>()
  const form = useForm({ resolver: zodResolver(schema), values: { name: group?.name ?? '' } })
  const create = useAdminMutation(adminApi.createGroup, [adminKeys.groups])
  const rename = useAdminMutation(adminApi.renameGroup, [adminKeys.groups])
  const submit = form.handleSubmit(async ({ name }) => {
    setFormError(undefined)
    try {
      if (group) await rename.mutateAsync({ id: group.id, name })
      else await create.mutateAsync({ name })
      toast.success(group ? 'Group renamed' : `Group ${name} created`)
      onOpenChange(false)
    } catch (e) {
      setFormError(applyProblem(e, form.setError, ['name']))
    }
  })
  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title={group ? 'Rename group' : 'New group'}
      description="Groups let you grant element permissions to many users at once."
      onSubmit={submit}
      submitting={form.formState.isSubmitting}
      formError={formError}
      submitLabel={group ? 'Rename' : 'Create group'}
    >
      <Field label="Name" htmlFor="g-name" error={form.formState.errors.name?.message} required>
        <Input id="g-name" autoFocus aria-invalid={!!form.formState.errors.name} {...form.register('name')} />
      </Field>
    </FormDialog>
  )
}

function MembersSheet({ group, onOpenChange }: { group: Group | null; onOpenChange: (o: boolean) => void }) {
  const members = useGroupMembers(group?.id)
  const users = useUsers()
  const key = adminKeys.members(group?.id ?? -1)
  const add = useAdminMutation(adminApi.addMember, [key])
  const remove = useAdminMutation(adminApi.removeMember, [key])
  const [pick, setPick] = useState<string>('')
  const memberIds = new Set((members.data ?? []).map((u) => u.id))
  const candidates = (users.data ?? []).filter((u) => !memberIds.has(u.id))

  return (
    <Sheet open={!!group} onOpenChange={onOpenChange}>
      <SheetContent>
        <SheetHeader>
          <SheetTitle>Members of {group?.name}</SheetTitle>
          <SheetDescription>Members inherit the group’s element permissions.</SheetDescription>
        </SheetHeader>
        <SheetBody className="space-y-4">
          <form
            className="flex gap-2"
            onSubmit={async (e) => {
              e.preventDefault()
              if (!group || !pick) return
              try {
                await add.mutateAsync({ id: group.id, user_id: Number(pick) })
                setPick('')
                toast.success('Member added')
              } catch (err) {
                toastError('Could not add member', err)
              }
            }}
          >
            <Select value={pick} onValueChange={setPick}>
              <SelectTrigger aria-label="User to add" className="flex-1">
                <SelectValue placeholder={candidates.length ? 'Choose a user…' : 'Everyone is a member'} />
              </SelectTrigger>
              <SelectContent>
                {candidates.map((u) => (
                  <SelectItem key={u.id} value={String(u.id)}>
                    {u.username}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button type="submit" disabled={!pick || add.isPending}>
              <UserPlus /> Add
            </Button>
          </form>
          <div className="overflow-hidden rounded-lg border">
            {members.isError ? (
              <ErrorState
                error={members.error}
                onRetry={() => void members.refetch()}
                className="m-3 border-0"
              />
            ) : members.isLoading ? (
              <TableSkeleton rows={3} cols={2} />
            ) : (members.data ?? []).length === 0 ? (
              <EmptyState icon={UsersRound} title="No members yet" className="m-3 border-0 py-8" />
            ) : (
              <ul className="divide-y">
                {members.data!.map((u) => (
                  <li key={u.id} className="flex items-center justify-between gap-2 px-3 py-2 text-sm">
                    <span>
                      <span className="font-medium">{u.username}</span>
                      {u.email && <span className="text-muted-foreground ml-2 text-xs">{u.email}</span>}
                    </span>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      aria-label={`Remove ${u.username}`}
                      onClick={async () => {
                        if (!group) return
                        try {
                          await remove.mutateAsync({ id: group.id, user_id: u.id })
                          toast.success(`${u.username} removed`)
                        } catch (e) {
                          toastError('Could not remove member', e)
                        }
                      }}
                    >
                      <UserMinus />
                    </Button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </SheetBody>
      </SheetContent>
    </Sheet>
  )
}

export function GroupsPage() {
  const groups = useGroups()
  const del = useAdminMutation(adminApi.deleteGroup, [adminKeys.groups])
  const confirm = useConfirm()
  const [editing, setEditing] = useState<Group | null>(null)
  const [open, setOpen] = useState(false)
  const [membersOf, setMembersOf] = useState<Group | null>(null)

  const columns: Column<Group>[] = [
    {
      id: 'name',
      header: 'Name',
      sortValue: (g) => g.name.toLowerCase(),
      searchValue: (g) => g.name,
      cell: (g) => <span className="font-medium">{g.name}</span>,
    },
    {
      id: 'members',
      header: 'Members',
      cell: (g) => (
        <Button variant="outline" size="xs" onClick={() => setMembersOf(g)}>
          <UsersRound /> Manage members
        </Button>
      ),
    },
    {
      id: 'actions',
      header: <span className="sr-only">Actions</span>,
      headClassName: 'w-20',
      cell: (g) => (
        <RowActions>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={`Rename ${g.name}`}
            onClick={() => {
              setEditing(g)
              setOpen(true)
            }}
          >
            <Pencil />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={`Delete ${g.name}`}
            onClick={() =>
              confirm.ask({
                title: `Delete group ${g.name}?`,
                description: 'Members lose the permissions granted through this group.',
                destructive: true,
                onConfirm: async () => {
                  try {
                    await del.mutateAsync(g.id)
                    toast.success('Group deleted')
                  } catch (e) {
                    toastError('Could not delete group', e)
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
            <img src="/brand/logo-groups.svg" alt="" className="size-7 rounded-lg shadow-xs" />
            <span>Groups</span>
          </div>
        }
        description="Collections of users that share element permissions."
        actions={
          <Button
            onClick={() => {
              setEditing(null)
              setOpen(true)
            }}
          >
            <Plus /> New group
          </Button>
        }
      />
      <DataTable
        label="Groups"
        data={groups.data}
        columns={columns}
        getRowId={(g) => g.id}
        isLoading={groups.isLoading}
        error={groups.error}
        onRetry={() => void groups.refetch()}
        searchPlaceholder="Search groups…"
        initialSort={{ id: 'name', dir: 'asc' }}
        empty={{
          icon: UsersRound,
          title: 'No groups yet',
          description: 'Create a group to grant permissions to several users at once.',
        }}
      />
      <GroupDialog group={editing} open={open} onOpenChange={setOpen} />
      <MembersSheet group={membersOf} onOpenChange={(o) => !o && setMembersOf(null)} />
      {confirm.dialog}
    </Page>
  )
}
