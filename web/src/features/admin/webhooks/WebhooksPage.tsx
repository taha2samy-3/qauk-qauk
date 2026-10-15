import { Webhook, Plus, Send, History, Pencil, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { useConfirm } from '@/components/ConfirmDialog'
import { DataTable, type Column } from '@/components/DataTable'
import { Page } from '@/components/layout/AppShell'
import { PageHeader } from '@/components/states'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Hint } from '@/components/ui/tooltip'
import { toastError } from '@/lib/forms'
import { useAdminMutation } from '../api'
import { RowActions } from '../FormDialog'
import { useWebhooks, webhookApi, webhookKeys, type WebhookEndpoint, type WebhookSaved } from './api'
import { WebhookDeliveriesDrawer } from './WebhookDeliveriesDrawer'
import { WebhookDialog, WebhookSecretDialog } from './WebhookDialog'

export function WebhooksPage() {
  const webhooks = useWebhooks()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<WebhookEndpoint | null>(null)
  const [deliveriesEndpoint, setDeliveriesEndpoint] = useState<WebhookEndpoint | null>(null)
  const [testingId, setTestingId] = useState<string | null>(null)
  const [revealed, setRevealed] = useState<WebhookSaved | null>(null)

  const del = useAdminMutation(webhookApi.delete, [webhookKeys.all])
  const confirm = useConfirm()

  const handleTest = async (ep: WebhookEndpoint) => {
    setTestingId(ep.id)
    try {
      const res = await webhookApi.test({ id: ep.id, severity: 'info' })
      if (res.success) {
        toast.success(`Test delivered to ${ep.name} (HTTP ${res.status_code}, ${res.latency_ms} ms)`)
      } else {
        toast.error(`Test to ${ep.name} failed: ${res.error || `HTTP ${res.status_code}`}`, {
          description: res.response ? res.response.slice(0, 300) : undefined,
        })
      }
    } catch (err) {
      toastError('Could not send the test', err)
    } finally {
      setTestingId(null)
    }
  }

  const formatBadge = (f: string) => {
    switch (f) {
      case 'slack':
        return <Badge variant="outline" className="border-emerald-500/30 text-emerald-600 bg-emerald-500/5">Slack</Badge>
      case 'discord':
        return <Badge variant="outline" className="border-indigo-500/30 text-indigo-600 bg-indigo-500/5">Discord</Badge>
      case 'teams':
        return <Badge variant="outline" className="border-blue-500/30 text-blue-600 bg-blue-500/5">Teams</Badge>
      case 'telegram':
        return <Badge variant="outline" className="border-sky-500/30 text-sky-600 bg-sky-500/5">Telegram</Badge>
      case 'custom':
        return <Badge variant="outline" className="border-purple-500/30 text-purple-600 bg-purple-500/5">Custom</Badge>
      default:
        return <Badge variant="secondary">Standard</Badge>
    }
  }

  const severityBadge = (s: string) => {
    switch (s) {
      case 'info':
        return <span key={s} className="inline-block px-1.5 py-0.5 rounded text-[11px] font-medium bg-sky-500/10 text-sky-600">Info</span>
      case 'warning':
        return <span key={s} className="inline-block px-1.5 py-0.5 rounded text-[11px] font-medium bg-amber-500/10 text-amber-600">Warning</span>
      case 'critical':
        return <span key={s} className="inline-block px-1.5 py-0.5 rounded text-[11px] font-medium bg-rose-500/10 text-rose-600">Critical</span>
      default:
        return null
    }
  }

  const columns: Column<WebhookEndpoint>[] = [
    {
      id: 'name',
      header: 'Endpoint',
      sortValue: (w) => w.name,
      searchValue: (w) => w.name,
      cell: (w) => (
        <div className="flex flex-col">
          <div className="flex items-center gap-2">
            <span className="font-medium text-foreground">{w.name}</span>
            {formatBadge(w.format)}
          </div>
          <span className="font-mono text-xs text-muted-foreground truncate max-w-sm mt-0.5" title={w.url}>
            {w.url}
            {w.format === 'telegram' && w.options.chat_id && <> · chat {w.options.chat_id}</>}
          </span>
        </div>
      ),
    },
    {
      id: 'severities',
      header: 'Subscribed Levels',
      cell: (w) => (
        <div className="flex flex-wrap gap-1">
          {(w.severities ?? ['info', 'warning', 'critical']).map((s) => severityBadge(s))}
        </div>
      ),
    },
    {
      id: 'status',
      header: 'Status',
      sortValue: (w) => (w.enabled ? 1 : 0),
      cell: (w) => (
        <div className="flex flex-col items-start gap-0.5">
          {w.enabled ? (
            <Badge variant="outline" className="text-emerald-500 border-emerald-500/30 bg-emerald-500/5">
              Active
            </Badge>
          ) : (
            <Badge variant="secondary">Disabled</Badge>
          )}
          {!w.secret_set && <span className="text-[11px] text-muted-foreground">unsigned</span>}
        </div>
      ),
    },
    {
      id: 'actions',
      header: <span className="sr-only">Actions</span>,
      headClassName: 'w-24 text-right',
      cell: (w) => (
        <RowActions>
          <Hint label="Send test notification">
            <span>
              <Button
                variant="ghost"
                size="icon-xs"
                disabled={testingId === w.id}
                onClick={() => void handleTest(w)}
                aria-label={`Send test notification to ${w.name}`}
              >
                <Send className={`size-3.5 ${testingId === w.id ? 'animate-pulse text-primary' : ''}`} />
              </Button>
            </span>
          </Hint>
          <Hint label="Delivery history">
            <span>
              <Button
                variant="ghost"
                size="icon-xs"
                onClick={() => setDeliveriesEndpoint(w)}
                aria-label={`View delivery history for ${w.name}`}
              >
                <History className="size-3.5" />
              </Button>
            </span>
          </Hint>
          <Hint label="Edit webhook">
            <span>
              <Button
                variant="ghost"
                size="icon-xs"
                onClick={() => {
                  setEditing(w)
                  setDialogOpen(true)
                }}
                aria-label={`Edit ${w.name}`}
              >
                <Pencil className="size-3.5" />
              </Button>
            </span>
          </Hint>
          <Hint label="Delete webhook">
            <span>
              <Button
                variant="ghost"
                size="icon-xs"
                onClick={() =>
                  confirm.ask({
                    title: `Delete ${w.name}?`,
                    description: 'This will remove the webhook endpoint and its delivery history. This cannot be undone.',
                    destructive: true,
                    onConfirm: async () => {
                      try {
                        await del.mutateAsync(w.id)
                        toast.success('Webhook endpoint deleted')
                      } catch (e) {
                        toastError('Could not delete webhook', e)
                      }
                    },
                  })
                }
                aria-label={`Delete ${w.name}`}
              >
                <Trash2 className="size-3.5" />
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
        title={
          <span className="flex items-center gap-2.5">
            <img src="/brand/logo-alerts.svg" alt="" className="size-7 rounded-lg shadow-xs" />
            <span>Webhooks</span>
          </span>
        }
        description="Deliver real-time alerts, warnings, and system info to Slack, Discord, Microsoft Teams, Telegram, or any custom API."
        actions={
          <Button
            onClick={() => {
              setEditing(null)
              setDialogOpen(true)
            }}
          >
            <Plus /> New webhook
          </Button>
        }
      />

      <DataTable
        label="Webhooks"
        data={webhooks.data}
        columns={columns}
        getRowId={(w) => w.id}
        isLoading={webhooks.isLoading}
        error={webhooks.error}
        onRetry={() => void webhooks.refetch()}
        empty={{
          icon: Webhook,
          title: 'No webhook endpoints configured',
          description: 'Create your first webhook to receive notifications in Slack, Discord, Teams, or external APIs.',
        }}
      />

      <WebhookDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        endpoint={editing}
        onSaved={(saved) => {
          toast.success(editing ? 'Webhook saved' : 'Webhook created')
          if (saved.secret) setRevealed(saved)
        }}
      />
      <WebhookSecretDialog saved={revealed} onClose={() => setRevealed(null)} />

      <WebhookDeliveriesDrawer
        open={!!deliveriesEndpoint}
        onOpenChange={(open) => !open && setDeliveriesEndpoint(null)}
        endpoint={deliveriesEndpoint}
      />

      {confirm.dialog}
    </Page>
  )
}
