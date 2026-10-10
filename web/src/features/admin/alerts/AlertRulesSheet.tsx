import { Plus, Trash2, AlertTriangle, AlertOctagon, Info } from 'lucide-react'
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
import { toastError } from '@/lib/forms'
import { useAdminMutation } from '../api'
import { alertApi, alertKeys, useElementAlerts, type AlertCondition, type AlertSeverity, type ElementAlertRule } from './api'

const CONDITIONS: { value: AlertCondition; label: string; desc: string }[] = [
  { value: 'above', label: 'Above threshold ( > )', desc: 'Triggers when value rises above threshold' },
  { value: 'below', label: 'Below threshold ( < )', desc: 'Triggers when value drops below threshold' },
  { value: 'outside_range', label: 'Outside range ( < Min or > Max )', desc: 'Triggers when value leaves acceptable range' },
  { value: 'equals', label: 'Equals value ( == )', desc: 'Triggers when value exactly equals target' },
]

export function AlertRulesSheet({
  element,
  open,
  onOpenChange,
}: {
  element: AdminElement | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { data: rules = [], isLoading } = useElementAlerts(element?.id)
  const save = useAdminMutation(alertApi.save, [alertKeys.elementAlerts(element?.id ?? '')])
  const del = useAdminMutation(alertApi.delete, [alertKeys.elementAlerts(element?.id ?? '')])
  const confirm = useConfirm()

  const [name, setName] = useState('')
  const [condition, setCondition] = useState<AlertCondition>('above')
  const [threshold, setThreshold] = useState('35')
  const [thresholdMax, setThresholdMax] = useState('50')
  const [hysteresis, setHysteresis] = useState('0.5')
  const [severity, setSeverity] = useState<AlertSeverity>('critical')
  const [message, setMessage] = useState('')
  const [formError, setFormError] = useState<string>()

  const handleAddRule = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!element) return
    if (!name.trim()) return setFormError('Rule name is required')

    const tVal = Number(threshold)
    if (isNaN(tVal)) return setFormError('Threshold must be a valid number')

    let tMaxVal: number | undefined = undefined
    if (condition === 'outside_range') {
      tMaxVal = Number(thresholdMax)
      if (isNaN(tMaxVal)) return setFormError('Maximum threshold must be a valid number')
      if (tMaxVal <= tVal) return setFormError('Max threshold must be greater than min threshold')
    }

    const hVal = Number(hysteresis) || 0

    try {
      await save.mutateAsync({
        elementId: element.id,
        name,
        condition,
        threshold: tVal,
        threshold_max: tMaxVal,
        hysteresis: hVal,
        severity,
        message,
        enabled: true,
      })
      toast.success('Alert rule created')
      setName('')
      setMessage('')
      setFormError(undefined)
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed to save alert rule'
      setFormError(msg)
    }
  }

  const handleDelete = (rule: ElementAlertRule) => {
    if (!element) return
    confirm.ask({
      title: `Delete alert rule "${rule.name}"?`,
      description: 'Notifications will no longer fire for this condition.',
      destructive: true,
      onConfirm: async () => {
        try {
          await del.mutateAsync({ elementId: element.id, ruleId: rule.id })
          toast.success('Alert rule deleted')
        } catch (err) {
          toastError('Could not delete rule', err)
        }
      },
    })
  }

  const severityBadge = (s: AlertSeverity) => {
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
    }
  }

  const formatConditionText = (r: ElementAlertRule) => {
    switch (r.condition) {
      case 'above':
        return `Value > ${r.threshold}`
      case 'below':
        return `Value < ${r.threshold}`
      case 'outside_range':
        return `Value < ${r.threshold} or > ${r.threshold_max ?? '—'}`
      case 'equals':
        return `Value == ${r.threshold}`
    }
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="sm:max-w-xl flex flex-col">
        <SheetHeader>
          <div className="flex items-center gap-2.5">
            <img src="/brand/logo-alerts.svg" alt="" className="size-6 rounded-md shadow-xs" />
            <SheetTitle>Alert Rules · {element?.name}</SheetTitle>
          </div>
          <SheetDescription>
            Conditions that trigger notifications and webhook deliveries for this element.
          </SheetDescription>
        </SheetHeader>

        <SheetBody className="flex-1 overflow-y-auto space-y-6 pt-2">
          {/* Section 1: Active Rules */}
          <div className="space-y-3">
            <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Configured Rules</h4>
            {isLoading ? (
              <div className="py-6 flex justify-center">
                <Spinner className="size-5 text-primary" />
              </div>
            ) : rules.length === 0 ? (
              <div className="p-4 rounded-lg border border-dashed text-center text-xs text-muted-foreground">
                No alert rules defined yet. Add your first condition below.
              </div>
            ) : (
              <div className="space-y-2">
                {rules.map((r) => (
                  <div
                    key={r.id}
                    className="p-3 rounded-lg border bg-card/60 flex items-center justify-between text-sm hover:bg-card transition-colors"
                  >
                    <div className="space-y-1">
                      <div className="flex items-center gap-2">
                        <span className="font-medium text-foreground">{r.name}</span>
                        {severityBadge(r.severity)}
                      </div>
                      <div className="flex items-center gap-2 text-xs font-mono text-muted-foreground">
                        <span className="font-semibold text-foreground/80">{formatConditionText(r)}</span>
                        {r.hysteresis > 0 && <span>(hysteresis: ±{r.hysteresis})</span>}
                      </div>
                      {r.message && <p className="text-xs text-muted-foreground">{r.message}</p>}
                    </div>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      onClick={() => handleDelete(r)}
                      aria-label={`Delete ${r.name}`}
                      className="text-muted-foreground hover:text-rose-500"
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </div>
                ))}
              </div>
            )}
          </div>

          <hr className="border-border/60" />

          {/* Section 2: Add New Rule Form */}
          <form onSubmit={handleAddRule} className="space-y-4">
            <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Add New Alert Condition</h4>

            {formError && (
              <div className="p-2.5 rounded bg-rose-500/10 text-rose-500 text-xs font-medium">
                {formError}
              </div>
            )}

            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="Rule Name" htmlFor="alert-name" required>
                <Input
                  id="alert-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="e.g. Critical High Temperature"
                />
              </Field>
              <Field label="Severity" htmlFor="alert-severity">
                <Select value={severity} onValueChange={(v) => setSeverity(v as AlertSeverity)}>
                  <SelectTrigger id="alert-severity">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="critical">Critical (Red)</SelectItem>
                    <SelectItem value="warning">Warning (Amber)</SelectItem>
                    <SelectItem value="info">Info (Blue)</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
            </div>

            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="Condition" htmlFor="alert-cond">
                <Select value={condition} onValueChange={(v) => setCondition(v as AlertCondition)}>
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

              <Field
                label={condition === 'outside_range' ? 'Min Threshold' : 'Threshold Value'}
                htmlFor="alert-thresh"
                required
              >
                <Input
                  id="alert-thresh"
                  type="number"
                  step="any"
                  value={threshold}
                  onChange={(e) => setThreshold(e.target.value)}
                />
              </Field>
            </div>

            {condition === 'outside_range' && (
              <Field label="Max Threshold" htmlFor="alert-thresh-max" required>
                <Input
                  id="alert-thresh-max"
                  type="number"
                  step="any"
                  value={thresholdMax}
                  onChange={(e) => setThresholdMax(e.target.value)}
                />
              </Field>
            )}

            <Field
              label="Hysteresis Band"
              htmlFor="alert-hys"
              description="Prevents rapid alarm flapping near threshold boundary"
            >
              <Input
                id="alert-hys"
                type="number"
                step="any"
                value={hysteresis}
                onChange={(e) => setHysteresis(e.target.value)}
              />
            </Field>

            <Field label="Custom Message" htmlFor="alert-msg" description="Optional custom text sent in webhook payload">
              <Input
                id="alert-msg"
                value={message}
                onChange={(e) => setMessage(e.target.value)}
                placeholder="e.g. Temperature reached critical level in cold storage"
              />
            </Field>

            <Button type="submit" disabled={save.isPending} className="w-full gap-2">
              {save.isPending ? <Spinner className="size-4 text-current" /> : <Plus className="size-4" />}
              Save Alert Rule
            </Button>
          </form>
        </SheetBody>
      </SheetContent>
      {confirm.dialog}
    </Sheet>
  )
}
