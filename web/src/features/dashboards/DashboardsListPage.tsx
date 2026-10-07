import { zodResolver } from '@hookform/resolvers/zod'
import {
  Copy,
  Globe,
  LayoutDashboard,
  Lock,
  MoreHorizontal,
  Pencil,
  Plus,
  Search,
  Trash2,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { Link, useNavigate } from 'react-router'
import { toast } from 'sonner'
import { z } from 'zod'
import {
  useCreateDashboard,
  useDashboards,
  useDeleteDashboard,
  useMe,
  useUpdateDashboard,
} from '@/api/queries'
import type { Dashboard } from '@/api/types'
import { useConfirm } from '@/components/ConfirmDialog'
import { FormError } from '@/components/FormError'
import { Page } from '@/components/layout/AppShell'
import { RelativeTime } from '@/components/RelativeTime'
import { EmptyState, ErrorState, PageHeader } from '@/components/states'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Spinner } from '@/components/ui/spinner'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { applyProblem, toastError } from '@/lib/forms'
import { pluralize } from '@/lib/utils'
import { emptyLayout, migrateLayout } from './layout'

const nameSchema = z.object({
  name: z.string().trim().min(1, 'Give the dashboard a name').max(100, 'At most 100 characters'),
  shared: z.boolean(),
})
type NameValues = z.infer<typeof nameSchema>

function NameDialog({
  open,
  onOpenChange,
  title,
  description,
  initial,
  submitLabel,
  showShared,
  onSubmit,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  title: string
  description?: string
  initial: NameValues
  submitLabel: string
  showShared?: boolean
  onSubmit: (v: NameValues) => Promise<void>
}) {
  const [formError, setFormError] = useState<string>()
  const form = useForm<NameValues>({ resolver: zodResolver(nameSchema), values: initial })
  const { errors, isSubmitting } = form.formState
  const submit = form.handleSubmit(async (v) => {
    setFormError(undefined)
    try {
      await onSubmit(v)
      onOpenChange(false)
    } catch (e) {
      setFormError(applyProblem(e, form.setError, ['name']))
    }
  })
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) setFormError(undefined)
        onOpenChange(o)
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description && <DialogDescription>{description}</DialogDescription>}
        </DialogHeader>
        <form onSubmit={submit} className="grid gap-4" noValidate>
          <FormError message={formError} />
          <Field label="Name" htmlFor="dash-name" error={errors.name?.message}>
            <Input
              id="dash-name"
              autoFocus
              placeholder="e.g. Greenhouse overview"
              aria-invalid={!!errors.name}
              {...form.register('name')}
            />
          </Field>
          {showShared && (
            <label className="flex items-start justify-between gap-4 rounded-lg border p-3">
              <span className="space-y-0.5">
                <span className="block text-sm font-medium">Share with everyone</span>
                <span className="text-muted-foreground block text-xs">
                  Others can view it read-only; element permissions still apply.
                </span>
              </span>
              <Controller
                control={form.control}
                name="shared"
                render={({ field }) => (
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    aria-label="Share with everyone"
                  />
                )}
              />
            </label>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={isSubmitting} data-testid="dashboard-name-submit">
              {isSubmitting && <Spinner className="text-current" />} {submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

type Filter = 'all' | 'mine' | 'shared'

export function DashboardsListPage() {
  const me = useMe()
  const list = useDashboards()
  const create = useCreateDashboard()
  const update = useUpdateDashboard()
  const del = useDeleteDashboard()
  const navigate = useNavigate()
  const confirm = useConfirm()
  const [createOpen, setCreateOpen] = useState(false)
  const [renaming, setRenaming] = useState<Dashboard | null>(null)
  const [q, setQ] = useState('')
  const [filter, setFilter] = useState<Filter>('all')

  const myId = me.data?.id
  const rows = useMemo(() => {
    const needle = q.trim().toLowerCase()
    return (list.data ?? [])
      .filter((d) =>
        filter === 'mine' ? d.owner_id === myId : filter === 'shared' ? d.owner_id !== myId : true,
      )
      .filter(
        (d) =>
          !needle || d.name.toLowerCase().includes(needle) || d.owner_name.toLowerCase().includes(needle),
      )
      .sort((a, b) => b.updated_at.localeCompare(a.updated_at))
  }, [list.data, q, filter, myId])

  const duplicate = async (d: Dashboard) => {
    try {
      const copy = await create.mutateAsync({
        name: `${d.name} (copy)`.slice(0, 100),
        shared: false,
        layout: d.layout ?? emptyLayout(),
      })
      toast.success('Dashboard duplicated', {
        action: { label: 'Open', onClick: () => navigate(`/dashboards/${copy.id}`) },
      })
    } catch (e) {
      toastError('Could not duplicate the dashboard', e)
    }
  }

  const setShared = async (d: Dashboard, shared: boolean) => {
    try {
      await update.mutateAsync({ id: d.id, name: d.name, shared, layout: d.layout })
      toast.success(shared ? 'Dashboard shared with everyone' : 'Dashboard is now private')
    } catch (e) {
      toastError('Could not update sharing', e)
    }
  }

  const remove = (d: Dashboard) =>
    confirm.ask({
      title: `Delete “${d.name}”?`,
      description:
        'This permanently deletes the dashboard and its layout. Devices and data are not affected.',
      destructive: true,
      onConfirm: async () => {
        try {
          await del.mutateAsync(d.id)
          toast.success('Dashboard deleted')
        } catch (e) {
          toastError('Could not delete the dashboard', e)
          throw e
        }
      },
    })

  return (
    <Page wide>
      <PageHeader
        title="Dashboards"
        description="Live views of your devices. Build your own, or open ones shared with you."
        actions={
          <Button onClick={() => setCreateOpen(true)} data-testid="new-dashboard">
            <Plus /> New dashboard
          </Button>
        }
      />

      <div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <Tabs value={filter} onValueChange={(v) => setFilter(v as Filter)}>
          <TabsList>
            <TabsTrigger value="all">All</TabsTrigger>
            <TabsTrigger value="mine">Mine</TabsTrigger>
            <TabsTrigger value="shared">Shared with me</TabsTrigger>
          </TabsList>
        </Tabs>
        <div className="relative w-full sm:max-w-xs">
          <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
          <Input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search dashboards…"
            aria-label="Search dashboards"
            className="pl-8"
          />
        </div>
      </div>

      {list.isError ? (
        <ErrorState error={list.error} onRetry={() => void list.refetch()} />
      ) : list.isLoading ? (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 6 }, (_, i) => (
            <Skeleton key={i} className="h-36 rounded-xl" />
          ))}
        </div>
      ) : rows.length === 0 ? (
        q || filter !== 'all' ? (
          <EmptyState
            duck="detective"
            title="No dashboards found"
            description="Try a different search or filter."
          />
        ) : (
          <EmptyState
            duck="analyst"
            title="Create your first dashboard"
            description="Dashboards combine live gauges, charts and controls for your devices. Drag widgets around to build the layout you need."
            action={
              <Button onClick={() => setCreateOpen(true)}>
                <Plus /> New dashboard
              </Button>
            }
            className="py-20"
          />
        )
      ) : (
        <ul className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3" aria-label="Dashboards">
          {rows.map((d) => {
            const mine = d.owner_id === myId
            const count = migrateLayout(d.layout).layout.widgets.length
            return (
              <li key={d.id} className="group relative" data-testid="dashboard-card" data-name={d.name}>
                <Link
                  to={`/dashboards/${d.id}`}
                  className="bg-card hover:border-ring/40 focus-visible:ring-ring/50 flex h-full flex-col gap-4 rounded-xl border p-5 shadow-xs transition-all hover:shadow-md focus-visible:ring-[3px] focus-visible:outline-none"
                >
                  <div className="flex items-start gap-3 pr-8">
                    <span className="bg-primary/10 text-primary flex size-9 shrink-0 items-center justify-center rounded-lg">
                      <LayoutDashboard className="size-4" />
                    </span>
                    <div className="min-w-0">
                      <h2 className="truncate font-semibold">{d.name}</h2>
                      <p className="text-muted-foreground text-xs">{pluralize(count, 'widget')}</p>
                    </div>
                  </div>
                  <div className="text-muted-foreground mt-auto flex flex-wrap items-center gap-1.5 text-xs">
                    {d.shared && (
                      <Badge variant="secondary">
                        <Globe /> Shared
                      </Badge>
                    )}
                    {!mine && (
                      <Badge variant="outline">
                        <Lock /> Owned by {d.owner_name} · read-only
                      </Badge>
                    )}
                    <span className="ml-auto">
                      Updated <RelativeTime value={d.updated_at} />
                    </span>
                  </div>
                </Link>
                <div className="absolute top-4 right-4">
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        aria-label={`Actions for ${d.name}`}
                        data-testid="dashboard-actions"
                      >
                        <MoreHorizontal />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end" className="w-52">
                      {mine && (
                        <DropdownMenuItem onSelect={() => setRenaming(d)}>
                          <Pencil /> Rename
                        </DropdownMenuItem>
                      )}
                      <DropdownMenuItem onSelect={() => void duplicate(d)}>
                        <Copy /> Duplicate
                      </DropdownMenuItem>
                      {mine && (
                        <>
                          <DropdownMenuCheckboxItem
                            checked={d.shared}
                            onCheckedChange={(v) => void setShared(d, v)}
                          >
                            Shared with everyone
                          </DropdownMenuCheckboxItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            variant="destructive"
                            onSelect={() => remove(d)}
                            data-testid="delete-dashboard"
                          >
                            <Trash2 /> Delete
                          </DropdownMenuItem>
                        </>
                      )}
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>
              </li>
            )
          })}
        </ul>
      )}

      <NameDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        title="New dashboard"
        description="You can add widgets right after creating it."
        initial={{ name: '', shared: false }}
        submitLabel="Create"
        showShared
        onSubmit={async (v) => {
          const d = await create.mutateAsync({ name: v.name, shared: v.shared, layout: emptyLayout() })
          navigate(`/dashboards/${d.id}`)
        }}
      />
      <NameDialog
        open={!!renaming}
        onOpenChange={(o) => !o && setRenaming(null)}
        title="Rename dashboard"
        initial={{ name: renaming?.name ?? '', shared: renaming?.shared ?? false }}
        submitLabel="Rename"
        onSubmit={async (v) => {
          if (!renaming) return
          await update.mutateAsync({
            id: renaming.id,
            name: v.name,
            shared: renaming.shared,
            layout: renaming.layout,
          })
          toast.success('Dashboard renamed')
        }}
      />
      {confirm.dialog}
    </Page>
  )
}
