import { useState } from 'react'
import { Copy, Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { FormDialog } from '../FormDialog'
import { useAdminMutation } from '../api'
import {
  SEVERITIES,
  formValues,
  webhookApi,
  webhookBody,
  webhookKeys,
  type WebhookEndpoint,
  type WebhookFormat,
  type WebhookFormValues,
  type WebhookSaved,
  type WebhookSeverity,
} from './api'

const FORMATS: { value: WebhookFormat; label: string }[] = [
  { value: 'standard', label: 'Standard (CloudEvents JSON)' },
  { value: 'slack', label: 'Slack' },
  { value: 'discord', label: 'Discord' },
  { value: 'teams', label: 'Microsoft Teams' },
  { value: 'telegram', label: 'Telegram bot' },
  { value: 'custom', label: 'Custom template' },
]

const URL_HINT: Record<WebhookFormat, string> = {
  standard: 'Your receiver’s https URL.',
  slack: 'The incoming webhook URL (https://hooks.slack.com/services/…).',
  discord: 'The channel webhook URL (https://discord.com/api/webhooks/…).',
  teams: 'The Workflows webhook URL of the channel.',
  telegram: 'https://api.telegram.org/bot<token>/sendMessage',
  custom: 'Your receiver’s https URL.',
}

function WebhookForm({
  endpoint,
  open,
  onOpenChange,
  onSaved,
}: {
  endpoint: WebhookEndpoint | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: (saved: WebhookSaved) => void
}) {
  const [v, setV] = useState<WebhookFormValues>(() => formValues(endpoint))
  const [error, setError] = useState<string>()
  const set = (u: Partial<WebhookFormValues>) => setV((cur) => ({ ...cur, ...u }))

  const create = useAdminMutation(webhookApi.create, [webhookKeys.all])
  const update = useAdminMutation(webhookApi.update, [webhookKeys.all])

  const toggleSeverity = (sev: WebhookSeverity) =>
    set({ severities: v.severities.includes(sev) ? v.severities.filter((s) => s !== sev) : [...v.severities, sev] })
  const setHeader = (i: number, u: Partial<WebhookFormValues['headers'][number]>) =>
    set({ headers: v.headers.map((h, j) => (j === i ? { ...h, ...u } : h)) })

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    const { body, error: problem } = webhookBody(v, !!endpoint)
    if (problem !== undefined) return setError(problem)
    setError(undefined)
    try {
      const saved = endpoint
        ? await update.mutateAsync({ id: endpoint.id, ...body })
        : await create.mutateAsync(body)
      onOpenChange(false)
      onSaved(saved)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not save the webhook')
    }
  }

  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title={endpoint ? `Edit ${endpoint.name}` : 'New webhook'}
      description="Where alert notifications go, in which format, and for which severities."
      submitting={create.isPending || update.isPending}
      formError={error}
      submitLabel={endpoint ? 'Save changes' : 'Create webhook'}
      className="sm:max-w-xl"
      onSubmit={handleSubmit}
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Name" htmlFor="wh-name" required>
          <Input id="wh-name" value={v.name} onChange={(e) => set({ name: e.target.value })} placeholder="e.g. Ops Slack" />
        </Field>
        <Field label="Format" htmlFor="wh-format">
          <Select value={v.format} onValueChange={(f) => set({ format: f as WebhookFormat })}>
            <SelectTrigger id="wh-format">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {FORMATS.map((f) => (
                <SelectItem key={f.value} value={f.value}>
                  {f.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      </div>

      <Field
        label="URL"
        htmlFor="wh-url"
        required={!endpoint}
        description={
          <>
            {URL_HINT[v.format]} It can hold a token, so it is stored encrypted and never shown again; an{' '}
            <code>env:NAME</code> or <code>file:/path</code> reference keeps it out of the database.
          </>
        }
      >
        <Input
          id="wh-url"
          className="font-mono text-xs"
          value={v.url}
          onChange={(e) => set({ url: e.target.value })}
          placeholder={endpoint ? `${endpoint.url} (unchanged)` : 'https://…'}
          autoComplete="off"
        />
      </Field>

      {v.format === 'telegram' && (
        <Field label="Chat ID" htmlFor="wh-chat" required description="The chat, group or channel the bot posts to, e.g. -1001234567890.">
          <Input id="wh-chat" className="font-mono text-xs" value={v.chatId} onChange={(e) => set({ chatId: e.target.value })} />
        </Field>
      )}

      <fieldset>
        <legend className="text-sm font-medium mb-1.5">Severities</legend>
        <div className="flex gap-4">
          {SEVERITIES.map((s) => (
            <label key={s} className="flex items-center gap-2 text-sm cursor-pointer select-none">
              <input
                type="checkbox"
                checked={v.severities.includes(s)}
                onChange={() => toggleSeverity(s)}
                className="rounded border-input text-primary focus:ring-primary"
              />
              <span className="capitalize">{s}</span>
            </label>
          ))}
        </div>
      </fieldset>

      <Field
        label="Signing secret"
        htmlFor="wh-secret"
        description={
          endpoint
            ? endpoint.secret_set
              ? 'Every request is signed (Standard Webhooks headers). Leave empty to keep the secret, paste one, or generate a new one.'
              : 'No secret: requests aren’t signed. Paste one or generate one.'
            : 'Every request is signed (Standard Webhooks headers). Leave empty to generate one; it is shown once after saving.'
        }
      >
        <div className="flex gap-2">
          <Input
            id="wh-secret"
            className="font-mono text-xs"
            value={v.newSecret ? '' : v.secret}
            disabled={v.newSecret}
            onChange={(e) => set({ secret: e.target.value })}
            placeholder={v.newSecret ? 'A new secret is generated on save' : 'whsec_… or env:NAME'}
            autoComplete="off"
          />
          {endpoint && (
            <label className="flex shrink-0 items-center gap-1.5 text-xs cursor-pointer select-none">
              <input type="checkbox" checked={v.newSecret} onChange={(e) => set({ newSecret: e.target.checked })} />
              Generate new
            </label>
          )}
        </div>
      </Field>

      {v.format === 'custom' && (
        <Field
          label="Template"
          htmlFor="wh-template"
          required
          description={
            <>
              Go template; the output must be valid JSON. Use <code>{'{{json .Title}}'}</code> for each value so it is
              quoted and escaped. Fields: .Title .Message .Severity .State .Value .ElementName .DeviceName .Timestamp
            </>
          }
        >
          <Textarea
            id="wh-template"
            rows={4}
            className="font-mono text-xs"
            value={v.customTemplate}
            onChange={(e) => set({ customTemplate: e.target.value })}
            placeholder='{"alert": {{json .Title}}, "level": {{json .Severity}}}'
          />
        </Field>
      )}

      <fieldset className="grid gap-2">
        <legend className="text-sm font-medium mb-1">Extra headers</legend>
        {v.headers.map((h, i) => (
          <div key={i} className="flex gap-2">
            <Input
              aria-label="Header name"
              className="font-mono text-xs"
              value={h.name}
              disabled={h.stored}
              placeholder="Authorization"
              onChange={(e) => setHeader(i, { name: e.target.value })}
            />
            <Input
              aria-label={`Value of ${h.name || 'the header'}`}
              className="font-mono text-xs"
              value={h.value}
              placeholder={h.stored ? '(unchanged)' : 'Bearer … or env:NAME'}
              onChange={(e) => setHeader(i, { value: e.target.value })}
              autoComplete="off"
            />
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={`Remove ${h.name || 'header'}`}
              onClick={() => set({ headers: v.headers.filter((_, j) => j !== i) })}
            >
              <Trash2 className="size-4" />
            </Button>
          </div>
        ))}
        <div>
          <Button
            type="button"
            variant="outline"
            size="xs"
            onClick={() => set({ headers: [...v.headers, { name: '', value: '', stored: false }] })}
          >
            <Plus className="size-3.5" /> Add header
          </Button>
        </div>
        <p className="text-muted-foreground text-xs">Values are stored encrypted and never shown again.</p>
      </fieldset>

      <label className="flex items-center gap-2 text-sm cursor-pointer mt-1">
        <input
          type="checkbox"
          checked={v.enabled}
          onChange={(e) => set({ enabled: e.target.checked })}
          className="rounded border-input text-primary focus:ring-primary"
        />
        <span>Enabled</span>
      </label>
    </FormDialog>
  )
}

export function WebhookDialog({
  endpoint,
  open,
  onOpenChange,
  onSaved,
}: {
  endpoint: WebhookEndpoint | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: (saved: WebhookSaved) => void
}) {
  if (!open) return null
  return (
    <WebhookForm key={endpoint?.id ?? 'new'} endpoint={endpoint} open={open} onOpenChange={onOpenChange} onSaved={onSaved} />
  )
}

/** Shows a just-generated signing secret: the only time it is readable. */
export function WebhookSecretDialog({ saved, onClose }: { saved: WebhookSaved | null; onClose: () => void }) {
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(saved?.secret ?? '')
      toast.success('Secret copied')
    } catch {
      toast.error('Could not copy; select the text and copy it')
    }
  }
  return (
    <Dialog open={!!saved?.secret} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Signing secret for {saved?.name}</DialogTitle>
          <DialogDescription>
            Copy it now into your receiver: it isn’t shown again. Receivers verify the webhook-signature header with it
            (Standard Webhooks).
          </DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-2">
          <code className="bg-muted flex-1 break-all rounded-md px-2.5 py-2 font-mono text-xs select-all">{saved?.secret}</code>
          <Button type="button" variant="outline" size="icon" aria-label="Copy the secret" onClick={() => void copy()}>
            <Copy className="size-4" />
          </Button>
        </div>
        <DialogFooter>
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
