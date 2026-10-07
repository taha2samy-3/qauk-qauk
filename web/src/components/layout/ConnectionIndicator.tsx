import { useConnectionStatus } from '@/realtime/hooks'
import { useNow } from '@/lib/useNow'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

const LABEL = {
  idle: 'Connecting',
  connecting: 'Connecting',
  open: 'Live',
  reconnecting: 'Reconnecting',
  offline: 'Offline',
  closed: 'Disconnected',
} as const

export function ConnectionIndicator() {
  const { status, nextRetryAt, retry } = useConnectionStatus()
  const now = useNow()
  const tone =
    status === 'open'
      ? 'ok'
      : status === 'connecting' || status === 'idle' || status === 'reconnecting'
        ? 'warn'
        : 'bad'
  const secs = nextRetryAt ? Math.max(0, Math.ceil((nextRetryAt - now) / 1000)) : null
  const help =
    status === 'open'
      ? 'Realtime connection is up. Widgets update live.'
      : status === 'reconnecting'
        ? `Connection lost. ${secs !== null ? `Retrying in ${secs}s — ` : ''}click to retry now.`
        : status === 'offline'
          ? 'Your browser is offline. We will reconnect when the network is back.'
          : 'Connecting to the realtime gateway…'
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          onClick={() => status !== 'open' && retry()}
          data-testid="connection-status"
          data-status={status}
          aria-label={`Realtime: ${LABEL[status]}`}
          className={cn(
            'focus-visible:ring-ring/50 inline-flex h-7 items-center gap-2 rounded-full border px-2.5 text-xs font-medium transition-colors focus-visible:ring-[3px] focus-visible:outline-none',
            tone === 'ok' && 'border-success/30 bg-success/10 text-success',
            tone === 'warn' && 'border-warning/40 bg-warning/10 dark:text-warning text-amber-700',
            tone === 'bad' && 'border-destructive/30 bg-destructive/10 text-destructive',
          )}
        >
          <span className="relative flex size-2">
            {tone !== 'bad' && (
              <span
                className={cn(
                  'absolute inline-flex size-full animate-ping rounded-full opacity-60',
                  tone === 'ok' ? 'bg-success' : 'bg-warning',
                )}
              />
            )}
            <span
              className={cn(
                'relative inline-flex size-2 rounded-full',
                tone === 'ok' ? 'bg-success' : tone === 'warn' ? 'bg-warning' : 'bg-destructive',
              )}
            />
          </span>
          <span className="hidden sm:inline">
            {LABEL[status]}
            {status === 'reconnecting' && secs ? ` · ${secs}s` : ''}
          </span>
        </button>
      </TooltipTrigger>
      <TooltipContent side="bottom">{help}</TooltipContent>
    </Tooltip>
  )
}
