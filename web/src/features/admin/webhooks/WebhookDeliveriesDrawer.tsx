import { CheckCircle2, AlertCircle, Clock, RotateCw } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Spinner } from '@/components/ui/spinner'
import { useWebhookDeliveries, type WebhookEndpoint, type WebhookDelivery } from './api'

export function WebhookDeliveriesDrawer({
  endpoint,
  open,
  onOpenChange,
}: {
  endpoint: WebhookEndpoint | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { data: deliveries, isLoading, refetch, isFetching } = useWebhookDeliveries(endpoint?.id ?? null)

  const statusBadge = (d: WebhookDelivery) => {
    switch (d.status) {
      case 'delivered':
        return (
          <Badge variant="outline" className="text-emerald-500 border-emerald-500/30 gap-1 bg-emerald-500/5">
            <CheckCircle2 className="size-3" /> Delivered
          </Badge>
        )
      case 'retrying':
        return (
          <Badge variant="outline" className="text-amber-500 border-amber-500/30 gap-1 bg-amber-500/5">
            <Clock className="size-3" /> Retrying ({d.attempts}/{d.max_attempts})
          </Badge>
        )
      case 'failed':
        return (
          <Badge variant="outline" className="text-rose-500 border-rose-500/30 gap-1 bg-rose-500/5">
            <AlertCircle className="size-3" /> Failed
          </Badge>
        )
      default:
        return <Badge variant="secondary">{d.status}</Badge>
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl max-h-[85vh] flex flex-col">
        <DialogHeader>
          <div className="flex items-center justify-between pr-6">
            <div>
              <DialogTitle>Deliveries: {endpoint?.name}</DialogTitle>
              <DialogDescription className="mt-1 font-mono text-xs truncate max-w-md">
                {endpoint?.url}
              </DialogDescription>
            </div>
            <Button
              variant="outline"
              size="sm"
              onClick={() => refetch()}
              disabled={isFetching}
              className="gap-1.5 h-8 text-xs"
            >
              <RotateCw className={`size-3.5 ${isFetching ? 'animate-spin' : ''}`} /> Refresh
            </Button>
          </div>
        </DialogHeader>

        <div className="flex-1 overflow-y-auto pr-1 mt-2">
          {isLoading ? (
            <div className="py-12 flex justify-center">
              <Spinner className="size-6 text-primary" />
            </div>
          ) : !deliveries || deliveries.length === 0 ? (
            <div className="py-12 text-center text-sm text-muted-foreground">
              No delivery attempts recorded for this endpoint yet.
            </div>
          ) : (
            <div className="space-y-3">
              {deliveries.map((d) => (
                <div
                  key={d.id}
                  className="p-3.5 rounded-lg border bg-card/50 hover:bg-card transition-colors text-sm space-y-2"
                >
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      {statusBadge(d)}
                      <span className="font-mono text-xs font-semibold">
                        {d.last_status_code ? `HTTP ${d.last_status_code}` : 'No response'}
                      </span>
                      {d.latency_ms !== undefined && d.latency_ms > 0 && (
                        <span className="text-xs text-muted-foreground">({d.latency_ms}ms)</span>
                      )}
                    </div>
                    <span className="text-xs text-muted-foreground">
                      {new Date(d.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })}
                    </span>
                  </div>

                  <div className="flex items-center gap-2 text-xs text-muted-foreground">
                    <span className="font-semibold text-foreground">{d.event_type}</span>
                    <span>•</span>
                    <span className="capitalize">{d.severity}</span>
                  </div>

                  {d.last_error && (
                    <div className="text-xs font-mono text-rose-500 bg-rose-500/10 p-2 rounded">
                      {d.last_error}
                    </div>
                  )}

                  <details className="text-xs">
                    <summary className="cursor-pointer text-muted-foreground hover:text-foreground select-none font-medium">
                      View Payload
                    </summary>
                    <pre className="mt-2 p-2 rounded bg-muted/60 font-mono text-[11px] overflow-x-auto max-h-40">
                      {JSON.stringify(d.payload, null, 2)}
                    </pre>
                  </details>
                </div>
              ))}
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
