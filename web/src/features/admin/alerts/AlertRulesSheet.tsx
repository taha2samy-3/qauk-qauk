import { Plus, Trash2, AlertTriangle, AlertOctagon, Info, Pencil, BellRing } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import type { AdminElement } from '@/api/types'
import { useConfirm } from '@/components/ConfirmDialog'
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
import { Spinner } from '@/components/ui/spinner'
import { Switch } from '@/components/ui/switch'
import { toastError } from '@/lib/forms'
import { useAdminMutation } from '../api'
import {
  NEW_RULE,
  alertApi,
  alertKeys,
  describeResolve,
  describeRule,
  ruleBody,
  ruleForm,
  useElementAlerts,
  type AlertCondition,
  type AlertRuleForm,
  type AlertSeverity,
  type ElementAlertRule,
} from './api'

const CONDITIONS: { value: AlertCondition; label: string }[] = [
  { value: 'above', label: 'Above ( > )' },
  { value: 'below', label: 'Below ( < )' },
  { value: 'outside_range', label: 'Outside a range' },
  { value: 'equals', label: 'Equals ( = )' },
]

function SeverityBadge({ s }: { s: string }) {
  switch (s) {
    case 'info':
      return (
        <Badge variant="outline" className="border-sky-500/30 text-sky-600 bg-sky-500/5 gap-1 text-[11px]">
          <Info className="size-3" /> Info
        </Badge>
      )
    case 'warning':
      return (
        <Badge variant="outline" className="border-amber-500/30 text-amber-600 bg-amber-500/5 gap-1 text-[11px]">
          <AlertTriangle className="size-3" /> Warning
        </Badge>
      )
    case 'critical':
      return (
        <Badge variant="outline" className="border-rose-500/30 text-rose-600 bg-rose-500/5 gap-1 text-[11px]">
          <AlertOctagon className="size-3" /> Critical
        </Badge>
      )
    default:
      return <Badge variant="outline">{s}</Badge>
  }
}

export function AlertRulesSheet({
  element,
  open,
  onOpenChange,
}: {
  element: AdminElement | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="sm:max-w-xl flex flex-col">
        {element && <AlertRules key={element.id} element={element} />}
      </SheetContent>
    </Sheet>
  )
}

function AlertRules({ element }: { element: AdminElement }) {
  const { data: rules = [], isLoading } = useElementAlerts(element.id)
  const keys = [alertKeys.elementAlerts(element.id)]
  const save = useAdminMutation(alertApi.save, keys)
  const del = useAdminMutation(alertApi.delete, keys)
  const confirm = useConfirm()

  const [form, setForm] = useState<AlertRuleForm>(NEW_RULE)
  const [formError, setFormError] = useState<string>()
  const set = (u: Partial<AlertRuleForm>) => setForm((f) => ({ ...f, ...u }))
  const editing = form.id !== undefined

  const reset = () => {
    setForm(NEW_RULE)
    setFormError(undefined)
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    const { body, error } = ruleBody(form)
    if (error !== undefined) return setFormError(error)
    try {
      await save.mutateAsync({ elementId: element.id, ...body })
      toast.success(editing ? 'Alert rule saved' : 'Alert rule created')
      reset()
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Could not save the rule')
    }
  }

  const setEnabled = async (r: ElementAlertRule, enabled: boolean) => {
    const { body, error } = ruleBody({ ...ruleForm(r), enabled })
    if (error !== undefined) return toast.error(error)
    try {
      await save.mutateAsync({ elementId: element.id, ...body })
      toast.success(`${r.name} ${enabled ? 'enabled' : 'disabled'}`)
    } catch (err) {
      toastError('Could not change the rule', err)
    }
  }

  const handleDelete = (rule: ElementAlertRule) =>
    confirm.ask({
      title: `Delete the alert rule “${rule.name}”?`,
      description: 'It stops notifying; if it is firing, no “resolved” notification is sent.',
      destructive: true,
      onConfirm: async () => {
        try {
          await del.mutateAsync({ elementId: element.id, ruleId: rule.id })
          if (form.id === rule.id) reset()
          toast.success('Alert rule deleted')
        } catch (err) {
          toastError('Could not delete the rule', err)
        }
      },
    })

  return (
    <>
      <SheetHeader>
        <div className="flex items-center gap-2.5">
          <img src="/brand/logo-alerts.svg" alt="" className="size-6 rounded-md shadow-xs" />
          <SheetTitle>Alert rules · {element.name}</SheetTitle>
        </div>
        <SheetDescription>
          Each rule watches this element’s value (a number, or the message’s <code>value</code> field, after the
          pipeline) and notifies the webhooks subscribed to its severity once when it fires and once when it resolves.
        </SheetDescription>
      </SheetHeader>

      <SheetBody className="flex-1 overflow-y-auto space-y-6 pt-2">
        <div className="space-y-3">
          <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Rules</h4>
          {isLoading ? (
            <div className="py-6 flex justify-center">
              <Spinner className="size-5 text-primary" />
            </div>
          ) : rules.length === 0 ? (
            <div className="p-4 rounded-lg border border-dashed text-center text-xs text-muted-foreground">
              No alert rules yet. Add one below.
            </div>
          ) : (
            <div className="space-y-2">
              {rules.map((r) => {
                const resolve = describeResolve(r)
                return (
                  <div
                    key={r.id}
                    className={`p-3 rounded-lg border bg-card/60 flex items-center justify-between gap-3 text-sm transition-colors hover:bg-card ${
                      form.id === r.id ? 'border-primary/60' : ''
                    } ${r.enabled ? '' : 'opacity-70'}`}
                  >
                    <div className="space-y-1 min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-medium text-foreground">{r.name}</span>
                        <SeverityBadge s={r.severity} />
                        {r.firing && r.enabled && (
                          <Badge variant="destructive" className="gap-1 text-[11px]">
                            <BellRing className="size-3" /> Firing
                          </Badge>
                        )}
                      </div>
                      <div className="flex flex-wrap items-center gap-x-2 text-xs font-mono text-muted-foreground">
                        <span className="font-semibold text-foreground/80">{describeRule(r)}</span>
                        {resolve && <span>({resolve})</span>}
                      </div>
                      {r.message && <p className="text-xs text-muted-foreground">{r.message}</p>}
                    </div>
                    <div className="flex shrink-0 items-center gap-1">
                      <Switch
                        checked={r.enabled}
                        disabled={save.isPending}
                        onCheckedChange={(on) => void setEnabled(r, on)}
                        aria-label={`${r.name} enabled`}
                      />
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        onClick={() => {
                          setForm(ruleForm(r))
                          setFormError(undefined)
                        }}
                        aria-label={`Edit ${r.name}`}
                      >
                        <Pencil className="size-3.5" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        onClick={() => handleDelete(r)}
                        aria-label={`Delete ${r.name}`}
                        className="text-muted-foreground hover:text-rose-500"
                      >
                        <Trash2 className="size-3.5" />
                      </Button>
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        </div>

        <hr className="border-border/60" />

        <form onSubmit={handleSubmit} className="space-y-4" noValidate>
          <div className="flex items-center justify-between">
            <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              {editing ? `Edit ${rules.find((r) => r.id === form.id)?.name ?? 'rule'}` : 'New rule'}
            </h4>
            {editing && (
              <Button type="button" variant="ghost" size="xs" onClick={reset}>
                Cancel edit
              </Button>
            )}
          </div>

          {formError && (
            <div role="alert" className="p-2.5 rounded bg-rose-500/10 text-rose-500 text-xs font-medium">
              {formError}
            </div>
          )}

          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Name" htmlFor="alert-name" required>
              <Input
                id="alert-name"
                value={form.name}
                onChange={(e) => set({ name: e.target.value })}
                placeholder="e.g. High temperature"
              />
            </Field>
            <Field label="Severity" htmlFor="alert-severity">
              <Select value={form.severity} onValueChange={(v) => set({ severity: v as AlertSeverity })}>
                <SelectTrigger id="alert-severity">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="critical">Critical</SelectItem>
                  <SelectItem value="warning">Warning</SelectItem>
                  <SelectItem value="info">Info</SelectItem>
                </SelectContent>
              </Select>
            </Field>
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Condition" htmlFor="alert-cond">
              <Select value={form.condition} onValueChange={(v) => set({ condition: v as AlertCondition })}>
                <SelectTrigger id="alert-cond">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {CONDITIONS.map((c) => (
                    <SelectItem key={c.value} value={c.value}>
                      {c.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field label={form.condition === 'outside_range' ? 'Minimum' : 'Threshold'} htmlFor="alert-thresh" required>
              <Input
                id="alert-thresh"
                inputMode="decimal"
                value={form.threshold}
                onChange={(e) => set({ threshold: e.target.value })}
              />
            </Field>
          </div>

          {form.condition === 'outside_range' && (
            <Field label="Maximum" htmlFor="alert-thresh-max" required>
              <Input
                id="alert-thresh-max"
                inputMode="decimal"
                value={form.thresholdMax}
                onChange={(e) => set({ thresholdMax: e.target.value })}
              />
            </Field>
          )}

          <Field
            label="Hysteresis"
            htmlFor="alert-hys"
            description="How far back past the threshold the value must go before the alert resolves (against flapping)."
          >
            <Input
              id="alert-hys"
              inputMode="decimal"
              value={form.hysteresis}
              onChange={(e) => set({ hysteresis: e.target.value })}
            />
          </Field>

          <Field label="Message" htmlFor="alert-msg" description="Optional text sent with the notification.">
            <Input
              id="alert-msg"
              value={form.message}
              onChange={(e) => set({ message: e.target.value })}
              placeholder="e.g. Cold storage is too warm"
            />
          </Field>

          <label className="flex items-center gap-2 text-sm">
            <Switch checked={form.enabled} onCheckedChange={(on) => set({ enabled: on })} />
            Enabled
          </label>

          <Button type="submit" disabled={save.isPending} className="w-full gap-2">
            {save.isPending ? <Spinner className="size-4 text-current" /> : editing ? null : <Plus className="size-4" />}
            {editing ? 'Save rule' : 'Add rule'}
          </Button>
        </form>
      </SheetBody>
      {confirm.dialog}
    </>
  )
}
