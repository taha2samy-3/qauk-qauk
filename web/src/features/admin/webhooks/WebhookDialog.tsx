import { useState } from 'react'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { FormDialog } from '../FormDialog'
import { useAdminMutation } from '../api'
import { webhookApi, webhookKeys, type WebhookEndpoint, type WebhookFormat, type WebhookSeverity } from './api'

const FORMATS: { value: WebhookFormat; label: string; desc: string }[] = [
  { value: 'standard', label: 'Standard Webhook (HMAC-SHA256)', desc: 'CloudEvents 1.0 JSON + Standard Webhooks headers' },
  { value: 'slack', label: 'Slack', desc: 'Incoming webhook formatted with Slack Block Kit' },
  { value: 'discord', label: 'Discord', desc: 'Discord webhook with rich embed cards' },
  { value: 'teams', label: 'Microsoft Teams', desc: 'Adaptive Cards 1.5 format for Teams Workflows' },
  { value: 'telegram', label: 'Telegram Bot', desc: 'Telegram Bot API sendMessage formatted payload' },
  { value: 'custom', label: 'Custom Template', desc: 'Custom body template and headers' },
]

function WebhookForm({
  endpoint,
  open,
  onOpenChange,
}: {
  endpoint: WebhookEndpoint | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [name, setName] = useState(endpoint?.name ?? '')
  const [url, setUrl] = useState(endpoint?.url ?? '')
  const [format, setFormat] = useState<WebhookFormat>(endpoint?.format ?? 'standard')
  const [secret, setSecret] = useState(endpoint?.secret ?? '')
  const [severities, setSeverities] = useState<WebhookSeverity[]>(endpoint?.severities ?? ['info', 'warning', 'critical'])
  const [headersStr, setHeadersStr] = useState(JSON.stringify(endpoint?.headers ?? {}, null, 2))
  const [customTemplate, setCustomTemplate] = useState(endpoint?.custom_template ?? '')
  const [enabled, setEnabled] = useState(endpoint?.enabled ?? true)
  const [error, setError] = useState<string>()

  const create = useAdminMutation(webhookApi.create, [webhookKeys.list()])
  const update = useAdminMutation(webhookApi.update, [webhookKeys.list()])

  const toggleSeverity = (sev: WebhookSeverity) => {
    if (severities.includes(sev)) {
      if (severities.length === 1) return
      setSeverities(severities.filter((s) => s !== sev))
    } else {
      setSeverities([...severities, sev])
    }
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!name.trim()) return setError('Name is required')
    if (!url.trim()) return setError('URL is required')

    let parsedHeaders = {}
    if (headersStr.trim()) {
      try {
        parsedHeaders = JSON.parse(headersStr) as Record<string, string>
      } catch (err: unknown) {
        const msg = err instanceof Error ? err.message : String(err)
        return setError(`Invalid Headers JSON: ${msg}`)
      }
    }

    try {
      if (endpoint) {
        await update.mutateAsync({
          id: endpoint.id,
          name,
          url,
          format,
          secret: secret || endpoint.secret,
          severities,
          headers: parsedHeaders as Record<string, string>,
          custom_template: format === 'custom' ? customTemplate : undefined,
          enabled,
        })
      } else {
        await create.mutateAsync({
          name,
          url,
          format,
          secret: secret || undefined,
          severities,
          headers: parsedHeaders as Record<string, string>,
          custom_template: format === 'custom' ? customTemplate : undefined,
          enabled,
        })
      }
      onOpenChange(false)
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed to save webhook endpoint'
      setError(msg)
    }
  }

  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title={endpoint ? 'Edit Webhook Endpoint' : 'New Webhook Endpoint'}
      description="Configure target endpoint, platform format, and subscribed severity levels."
      submitting={create.isPending || update.isPending}
      formError={error}
      submitLabel={endpoint ? 'Save Changes' : 'Create Webhook'}
      className="sm:max-w-xl"
      onSubmit={handleSubmit}
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Name" htmlFor="wh-name" required>
          <Input id="wh-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Slack Incident Channel" />
        </Field>
        <Field label="Format" htmlFor="wh-format">
          <Select value={format} onValueChange={(v) => setFormat(v as WebhookFormat)}>
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

      <Field label="Endpoint URL" htmlFor="wh-url" required description="Must be an accessible HTTP or HTTPS URL">
        <Input id="wh-url" type="url" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://..." />
      </Field>

      <div>
        <label className="text-sm font-medium mb-1.5 block">Subscribed Severities</label>
        <div className="flex gap-4">
          {(['info', 'warning', 'critical'] as WebhookSeverity[]).map((s) => {
            const checked = severities.includes(s)
            return (
              <label key={s} className="flex items-center gap-2 text-sm cursor-pointer select-none">
                <input
                  type="checkbox"
                  checked={checked}
                  onChange={() => toggleSeverity(s)}
                  className="rounded border-input text-primary focus:ring-primary"
                />
                <span className="capitalize">{s}</span>
              </label>
            )
          })}
        </div>
      </div>

      {format === 'standard' && (
        <Field
          label="Signing Secret"
          htmlFor="wh-secret"
          description={endpoint ? 'Leave blank to retain current secret' : 'Auto-generated with whsec_ prefix if blank'}
        >
          <Input
            id="wh-secret"
            className="font-mono text-xs"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            placeholder="whsec_..."
          />
        </Field>
      )}

      {format === 'custom' && (
        <Field
          label="Custom Template"
          htmlFor="wh-template"
          description="Go text/template syntax with {{.Title}}, {{.Message}}, {{.Severity}}, {{.Value}}"
        >
          <Textarea
            id="wh-template"
            rows={4}
            className="font-mono text-xs"
            value={customTemplate}
            onChange={(e) => setCustomTemplate(e.target.value)}
            placeholder='{"alert": "{{.Title}}", "level": "{{.Severity}}"}'
          />
        </Field>
      )}

      <Field label="Custom Headers (JSON)" htmlFor="wh-headers" description='Optional static headers: {"Authorization": "Bearer ..."}'>
        <Textarea
          id="wh-headers"
          rows={2}
          className="font-mono text-xs"
          value={headersStr}
          onChange={(e) => setHeadersStr(e.target.value)}
        />
      </Field>

      <label className="flex items-center gap-2 text-sm cursor-pointer mt-1">
        <input
          type="checkbox"
          checked={enabled}
          onChange={(e) => setEnabled(e.target.checked)}
          className="rounded border-input text-primary focus:ring-primary"
        />
        <span>Enable this webhook endpoint</span>
      </label>
    </FormDialog>
  )
}

export function WebhookDialog({
  endpoint,
  open,
  onOpenChange,
}: {
  endpoint: WebhookEndpoint | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  if (!open) return null
  return <WebhookForm key={endpoint?.id ?? 'new'} endpoint={endpoint} open={open} onOpenChange={onOpenChange} />
}
