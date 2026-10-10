import { zodResolver } from '@hookform/resolvers/zod'
import { Bell, Boxes, Paintbrush, Pencil, Plus, Trash2, Workflow } from 'lucide-react'
import { useState } from 'react'
import { Controller, useForm, useWatch } from 'react-hook-form'
import { useSearchParams } from 'react-router'
import { toast } from 'sonner'
import { z } from 'zod'
import type { AdminElement, Style } from '@/api/types'
import { useConfirm } from '@/components/ConfirmDialog'
import { DataTable, Mono, type Column } from '@/components/DataTable'
import { JsonEditor, parseJson } from '@/components/JsonEditor'
import { Page } from '@/components/layout/AppShell'
import { RelativeTime } from '@/components/RelativeTime'
import { EmptyState, ErrorState, PageHeader, TableSkeleton } from '@/components/states'
import { Badge } from '@/components/ui/badge'
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
import { Textarea } from '@/components/ui/textarea'
import { applyProblem, toastError } from '@/lib/forms'
import { isRecord } from '@/lib/utils'
import { adminApi, adminKeys, useAdminDevices, useAdminElements, useAdminMutation, useStyles } from './api'
import { AlertRulesSheet } from './alerts/AlertRulesSheet'
import { FormDialog, RowActions } from './FormDialog'
import { PipelineSheet } from './PipelineSheet'

const ALL = '__all__'
const jsonObject = z.string().superRefine((v, ctx) => {
  const p = parseJson(v)
  if (!p.ok) ctx.addIssue({ code: 'custom', message: `Invalid JSON: ${p.error}` })
  else if (!isRecord(p.value)) ctx.addIssue({ code: 'custom', message: 'Must be a JSON object' })
})
const pretty = (v: unknown) => (v === null || v === undefined ? '{}' : JSON.stringify(v, null, 2))

const elementSchema = z.object({
  device_id: z.string().min(1, 'Choose a device'),
  name: z.string().trim().min(1, 'Required').max(50, 'At most 50 characters'),
  description: z.string().max(2000),
  points: z
    .number({ error: 'Enter a number' })
    .int('Whole numbers only')
    .min(0, 'Min 0')
    .max(1000, 'Max 1000'),
  details: jsonObject,
  // Rate limit: empty = server default (QUACK_ELEMENT_MSG_RATE).
  msg_rate: z
    .string()
    .refine((v) => v.trim() === '' || (Number(v) > 0 && Number(v) <= 1000), 'Between 0 and 1000, or empty'),
  msg_burst: z
    .string()
    .refine(
      (v) => v.trim() === '' || (Number.isInteger(Number(v)) && Number(v) >= 1 && Number(v) <= 1000),
      'A whole number from 1 to 1000, or empty',
    ),
  over_limit: z.enum(['drop', 'latest']),
})
type ElementValues = z.infer<typeof elementSchema>

const DETAILS_TEMPLATE = '{\n  "title": "",\n  "unit": ""\n}'

function ElementDialog({
  element,
  defaultDevice,
  open,
  onOpenChange,
}: {
  element: AdminElement | null
  defaultDevice?: string
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const devices = useAdminDevices()
  const [formError, setFormError] = useState<string>()
  const form = useForm<ElementValues>({
    resolver: zodResolver(elementSchema),
    values: {
      device_id: element?.device_id ?? defaultDevice ?? '',
      name: element?.name ?? '',
      description: element?.description ?? '',
      points: element?.points ?? 100,
      details: element ? pretty(element.details) : DETAILS_TEMPLATE,
      msg_rate: element?.msg_rate != null ? String(element.msg_rate) : '',
      msg_burst: element?.msg_burst != null ? String(element.msg_burst) : '',
      over_limit: element?.over_limit ?? 'drop',
    },
  })
  const create = useAdminMutation(adminApi.createElement, [['admin', 'elements']])
  const update = useAdminMutation(adminApi.updateElement, [['admin', 'elements']])
  const { errors, isSubmitting } = form.formState
  const overLimit = useWatch({ control: form.control, name: 'over_limit' })

  const submit = form.handleSubmit(async (v) => {
    setFormError(undefined)
    const details = (parseJson(v.details) as { value: unknown }).value
    // 0 resets a limit to the server default
    const limits = {
      msg_rate: v.msg_rate.trim() === '' ? 0 : Number(v.msg_rate),
      msg_burst: v.msg_burst.trim() === '' ? 0 : Number(v.msg_burst),
      over_limit: v.over_limit,
    }
    try {
      if (element)
        await update.mutateAsync({
          id: element.id,
          name: v.name,
          description: v.description,
          points: v.points,
          details,
          ...limits,
        })
      else
        await create.mutateAsync({
          device_id: v.device_id,
          name: v.name,
          description: v.description,
          points: v.points,
          details,
          ...limits,
        })
      toast.success(element ? 'Element updated' : `Element ${v.name} created`)
      onOpenChange(false)
    } catch (e) {
      setFormError(
        applyProblem(e, form.setError, [
          'device_id',
          'name',
          'description',
          'points',
          'details',
          'msg_rate',
          'msg_burst',
          'over_limit',
        ]),
      )
    }
  })

  return (
    <FormDialog
      open={open}
      onOpenChange={(o) => {
        if (!o) setFormError(undefined)
        onOpenChange(o)
      }}
      title={element ? `Edit ${element.name}` : 'New element'}
      description="An element is one value or control on a device (a sensor, a chart series, a switch…)."
      onSubmit={submit}
      submitting={isSubmitting}
      formError={formError}
      submitLabel={element ? 'Save changes' : 'Create element'}
      className="sm:max-w-xl"
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          label="Device"
          error={errors.device_id?.message}
          required
          description={element ? 'Elements cannot be moved between devices.' : undefined}
        >
          <Controller
            control={form.control}
            name="device_id"
            render={({ field }) => (
              <Select value={field.value} onValueChange={field.onChange} disabled={!!element}>
                <SelectTrigger aria-label="Device" aria-invalid={!!errors.device_id}>
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
            )}
          />
        </Field>
        <Field label="Name" htmlFor="e-name" error={errors.name?.message} required>
          <Input id="e-name" aria-invalid={!!errors.name} {...form.register('name')} />
        </Field>
      </div>
      <Field label="Description" htmlFor="e-desc" error={errors.description?.message}>
        <Textarea id="e-desc" rows={2} {...form.register('description')} />
      </Field>
      <Field
        label="History points"
        htmlFor="e-points"
        error={errors.points?.message}
        description="How many recent values are replayed to new subscribers (0–1000)."
      >
        <Input
          id="e-points"
          type="number"
          min={0}
          max={1000}
          className="w-32"
          aria-invalid={!!errors.points}
          {...form.register('points', { valueAsNumber: true })}
        />
      </Field>
      <fieldset className="grid gap-4 rounded-lg border p-4 sm:grid-cols-3">
        <legend className="px-1 text-sm font-medium">Rate limit</legend>
        <Field
          label="Messages / second"
          htmlFor="e-rate"
          error={errors.msg_rate?.message}
          description="Empty = server default."
        >
          <Input
            id="e-rate"
            inputMode="decimal"
            placeholder="default"
            aria-invalid={!!errors.msg_rate}
            {...form.register('msg_rate')}
          />
        </Field>
        <Field
          label="Burst"
          htmlFor="e-burst"
          error={errors.msg_burst?.message}
          description="Empty = the rate."
        >
          <Input
            id="e-burst"
            inputMode="numeric"
            placeholder="= rate"
            aria-invalid={!!errors.msg_burst}
            {...form.register('msg_burst')}
          />
        </Field>
        <Field
          label="Over the limit"
          error={errors.over_limit?.message}
          description={
            overLimit === 'latest'
              ? 'Keep the newest value and send it when allowed.'
              : 'Discard extra messages.'
          }
        >
          <Controller
            control={form.control}
            name="over_limit"
            render={({ field }) => (
              <Select value={field.value} onValueChange={field.onChange}>
                <SelectTrigger aria-label="Over the limit">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="drop">Drop</SelectItem>
                  <SelectItem value="latest">Keep latest</SelectItem>
                </SelectContent>
              </Select>
            )}
          />
        </Field>
      </fieldset>
      <Field
        label="Details (JSON)"
        htmlFor="e-details"
        error={errors.details?.message}
        description="Widget configuration, e.g. title, unit, minValue/maxValue, min/max/step."
      >
        <Controller
          control={form.control}
          name="details"
          render={({ field }) => (
            <JsonEditor
              id="e-details"
              value={field.value}
              onChange={field.onChange}
              invalid={!!errors.details}
            />
          )}
        />
      </Field>
    </FormDialog>
  )
}

const styleSchema = z.object({ name: z.string().trim().min(1, 'Required').max(100), details: jsonObject })

function StylesSheet({
  element,
  onOpenChange,
}: {
  element: AdminElement | null
  onOpenChange: (o: boolean) => void
}) {
  const styles = useStyles(element?.id)
  const key = adminKeys.styles(element?.id ?? '')
  const create = useAdminMutation(adminApi.createStyle, [key])
  const update = useAdminMutation(adminApi.updateStyle, [key])
  const del = useAdminMutation(adminApi.deleteStyle, [key])
  const [editing, setEditing] = useState<Style | 'new' | null>(null)
  const [formError, setFormError] = useState<string>()
  const current = editing && editing !== 'new' ? editing : null
  const form = useForm({
    resolver: zodResolver(styleSchema),
    values: {
      name: current?.name ?? (editing === 'new' ? 'widget' : ''),
      details: current ? pretty(current.details) : '{\n  "widget": "sensor"\n}',
    },
  })

  const submit = form.handleSubmit(async (v) => {
    if (!element) return
    setFormError(undefined)
    const details = (parseJson(v.details) as { value: unknown }).value
    try {
      if (current) await update.mutateAsync({ id: current.id, name: v.name, details })
      else await create.mutateAsync({ elementId: element.id, name: v.name, details })
      toast.success(current ? 'Style updated' : 'Style added')
      setEditing(null)
    } catch (e) {
      setFormError(applyProblem(e, form.setError, ['name', 'details']))
    }
  })

  return (
    <Sheet
      open={!!element}
      onOpenChange={(o) => {
        if (!o) setEditing(null)
        onOpenChange(o)
      }}
    >
      <SheetContent className="sm:max-w-lg">
        <SheetHeader>
          <SheetTitle>Styles · {element?.name}</SheetTitle>
          <SheetDescription>
            Presentation hints. A style named <code className="font-mono">widget</code> with{' '}
            <code className="font-mono">{'{"widget":"sensor|chart|switch|slider"}'}</code> picks the default
            dashboard widget.
          </SheetDescription>
        </SheetHeader>
        <SheetBody className="space-y-4">
          {editing ? (
            <form onSubmit={submit} className="grid gap-4 rounded-lg border p-4" noValidate>
              <h3 className="text-sm font-semibold">
                {current ? `Edit style “${current.name}”` : 'New style'}
              </h3>
              {formError && <p className="text-destructive text-sm">{formError}</p>}
              <Field label="Name" htmlFor="s-name" error={form.formState.errors.name?.message} required>
                <Input id="s-name" {...form.register('name')} />
              </Field>
              <Field
                label="Details (JSON)"
                htmlFor="s-details"
                error={form.formState.errors.details?.message}
              >
                <Controller
                  control={form.control}
                  name="details"
                  render={({ field }) => (
                    <JsonEditor id="s-details" rows={6} value={field.value} onChange={field.onChange} />
                  )}
                />
              </Field>
              <div className="flex justify-end gap-2">
                <Button variant="outline" onClick={() => setEditing(null)}>
                  Cancel
                </Button>
                <Button type="submit" disabled={form.formState.isSubmitting}>
                  {current ? 'Save style' : 'Add style'}
                </Button>
              </div>
            </form>
          ) : (
            <Button variant="outline" size="sm" onClick={() => setEditing('new')}>
              <Plus /> Add style
            </Button>
          )}
          {styles.isError ? (
            <ErrorState error={styles.error} onRetry={() => void styles.refetch()} />
          ) : styles.isLoading ? (
            <TableSkeleton rows={2} cols={2} />
          ) : (styles.data ?? []).length === 0 ? (
            <EmptyState icon={Paintbrush} title="No styles" className="py-8" />
          ) : (
            <ul className="space-y-2">
              {styles.data!.map((s) => (
                <li key={s.id} className="rounded-lg border p-3">
                  <div className="mb-2 flex items-center justify-between gap-2">
                    <span className="text-sm font-medium">{s.name}</span>
                    <RowActions>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        aria-label={`Edit style ${s.name}`}
                        onClick={() => setEditing(s)}
                      >
                        <Pencil />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        aria-label={`Delete style ${s.name}`}
                        onClick={async () => {
                          try {
                            await del.mutateAsync(s.id)
                            toast.success('Style deleted')
                          } catch (e) {
                            toastError('Could not delete style', e)
                          }
                        }}
                      >
                        <Trash2 />
                      </Button>
                    </RowActions>
                  </div>
                  <pre className="bg-muted overflow-auto rounded px-2 py-1.5 font-mono text-[11px]">
                    {JSON.stringify(s.details)}
                  </pre>
                </li>
              ))}
            </ul>
          )}
        </SheetBody>
      </SheetContent>
    </Sheet>
  )
}

export function ElementsPage() {
  const [params, setParams] = useSearchParams()
  const deviceFilter = params.get('device') ?? undefined
  const devices = useAdminDevices()
  const elements = useAdminElements(deviceFilter)
  const del = useAdminMutation(adminApi.deleteElement, [['admin', 'elements']])
  const confirm = useConfirm()
  const [editing, setEditing] = useState<AdminElement | null>(null)
  const [open, setOpen] = useState(false)
  const [stylesOf, setStylesOf] = useState<AdminElement | null>(null)
  const [pipelineOf, setPipelineOf] = useState<AdminElement | null>(null)
  const [alertsOf, setAlertsOf] = useState<AdminElement | null>(null)
  const deviceName = (id: string) => devices.data?.find((d) => d.id === id)?.name ?? id.slice(0, 8)

  const columns: Column<AdminElement>[] = [
    {
      id: 'name',
      header: 'Name',
      sortValue: (e) => e.name.toLowerCase(),
      searchValue: (e) => `${e.name} ${e.description}`,
      cell: (e) => (
        <div>
          <div className="flex items-center gap-2">
            <span className="font-medium">{e.name}</span>
            {e.pipeline_version != null && e.pipeline_steps != null && e.pipeline_steps > 0 && (
              <Badge variant="secondary" className="gap-1 font-mono text-[10px]">
                <Workflow className="size-3" /> {e.pipeline_steps} steps · v{e.pipeline_version}
              </Badge>
            )}
          </div>
          {e.description && (
            <div className="text-muted-foreground max-w-xs truncate text-xs">{e.description}</div>
          )}
        </div>
      ),
    },
    {
      id: 'device',
      header: 'Device',
      sortValue: (e) => deviceName(e.device_id),
      searchValue: (e) => deviceName(e.device_id),
      cell: (e) => deviceName(e.device_id),
    },
    {
      id: 'points',
      header: 'Points',
      sortValue: (e) => e.points,
      cell: (e) => <span className="tabular">{e.points}</span>,
      className: 'text-right',
      headClassName: 'text-right',
    },
    {
      id: 'limit',
      header: 'Rate limit',
      sortValue: (e) => e.msg_rate ?? 0,
      cell: (e) =>
        e.msg_rate == null ? (
          <span className="text-muted-foreground">default</span>
        ) : (
          <span className="tabular whitespace-nowrap">
            {e.msg_rate}/s
            {e.over_limit === 'latest' && (
              <Badge variant="muted" className="ml-1.5">
                latest
              </Badge>
            )}
          </span>
        ),
    },
    {
      id: 'details',
      header: 'Details',
      cell: (e) => {
        const keys = isRecord(e.details) ? Object.keys(e.details) : []
        return keys.length ? (
          <div className="flex max-w-xs flex-wrap gap-1">
            {keys.slice(0, 4).map((k) => (
              <Badge key={k} variant="muted" className="font-mono">
                {k}
              </Badge>
            ))}
            {keys.length > 4 && <Badge variant="muted">+{keys.length - 4}</Badge>}
          </div>
        ) : (
          <span className="text-muted-foreground">—</span>
        )
      },
    },
    {
      id: 'id',
      header: 'ID',
      searchValue: (e) => e.id,
      cell: (e) => <Mono className="select-all">{e.id.slice(0, 8)}</Mono>,
    },
    {
      id: 'created',
      header: 'Created',
      sortValue: (e) => e.created_at,
      cell: (e) => <RelativeTime value={e.created_at} className="text-muted-foreground" />,
    },
    {
      id: 'actions',
      header: <span className="sr-only">Actions</span>,
      headClassName: 'w-28',
      cell: (e) => (
        <RowActions>
          <Button variant="ghost" size="xs" onClick={() => setAlertsOf(e)}>
            <Bell className="size-3.5 mr-1" /> Alerts
          </Button>
          <Button variant="ghost" size="xs" onClick={() => setPipelineOf(e)}>
            <Workflow className="size-3.5 mr-1" /> Pipeline
          </Button>
          <Button variant="ghost" size="xs" onClick={() => setStylesOf(e)}>
            <Paintbrush /> Styles
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={`Edit ${e.name}`}
            onClick={() => {
              setEditing(e)
              setOpen(true)
            }}
          >
            <Pencil />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={`Delete ${e.name}`}
            onClick={() =>
              confirm.ask({
                title: `Delete element ${e.name}?`,
                description:
                  'Its permissions and styles are deleted, and subscribers are disconnected. Dashboards keep an “unavailable” widget.',
                destructive: true,
                onConfirm: async () => {
                  try {
                    await del.mutateAsync(e.id)
                    toast.success('Element deleted')
                  } catch (err) {
                    toastError('Could not delete element', err)
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
        title="Elements"
        description="Values and controls exposed by devices."
        actions={
          <Button
            onClick={() => {
              setEditing(null)
              setOpen(true)
            }}
          >
            <Plus /> New element
          </Button>
        }
      />
      <DataTable
        label="Elements"
        data={elements.data}
        columns={columns}
        getRowId={(e) => e.id}
        isLoading={elements.isLoading}
        error={elements.error}
        onRetry={() => void elements.refetch()}
        searchPlaceholder="Search elements…"
        initialSort={{ id: 'device', dir: 'asc' }}
        empty={{
          icon: Boxes,
          title: 'No elements',
          description: deviceFilter ? 'This device has no elements yet.' : undefined,
        }}
        toolbar={
          <Select
            value={deviceFilter ?? ALL}
            onValueChange={(v) => {
              const next = new URLSearchParams(params)
              if (v === ALL) next.delete('device')
              else next.set('device', v)
              setParams(next, { replace: true })
            }}
          >
            <SelectTrigger className="w-52" aria-label="Filter by device">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>All devices</SelectItem>
              {(devices.data ?? []).map((d) => (
                <SelectItem key={d.id} value={d.id}>
                  {d.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        }
      />
      <ElementDialog element={editing} defaultDevice={deviceFilter} open={open} onOpenChange={setOpen} />
      <StylesSheet element={stylesOf} onOpenChange={(o) => !o && setStylesOf(null)} />
      <PipelineSheet element={pipelineOf} onOpenChange={(o) => !o && setPipelineOf(null)} />
      <AlertRulesSheet element={alertsOf} open={!!alertsOf} onOpenChange={(o) => !o && setAlertsOf(null)} />
      {confirm.dialog}
    </Page>
  )
}
