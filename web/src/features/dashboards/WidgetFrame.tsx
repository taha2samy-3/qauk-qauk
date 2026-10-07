import {
  AlertTriangle,
  Copy,
  GripVertical,
  Link2Off,
  Lock,
  MoreHorizontal,
  RefreshCw,
  Settings2,
  ShieldOff,
  Trash2,
  WifiOff,
} from 'lucide-react'
import type { ReactNode } from 'react'
import { RelativeTime } from '@/components/RelativeTime'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Skeleton } from '@/components/ui/skeleton'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { formatDateTime } from '@/lib/time'
import { cn } from '@/lib/utils'
import type { Actor } from '@/realtime/client'

export interface WidgetFrameProps {
  id: string
  type: string
  title: string
  subtitle?: string
  editing: boolean
  readOnly?: boolean
  offline?: boolean
  stale?: boolean
  lastEditAt?: string | null
  lastEditBy?: Actor | null
  actions?: ReactNode
  onConfigure?: () => void
  onDuplicate?: () => void
  onRemove?: () => void
  children: ReactNode
}

export function WidgetFrame(p: WidgetFrameProps) {
  return (
    <section
      aria-label={p.title}
      data-testid="widget"
      data-widget-id={p.id}
      data-widget-type={p.type}
      className={cn(
        'group/widget bg-card text-card-foreground @container flex h-full flex-col overflow-hidden rounded-xl border shadow-xs transition-shadow',
        p.editing && 'ring-ring/40 hover:ring-2',
      )}
    >
      <header
        className={cn(
          'flex h-10 shrink-0 items-center gap-1.5 pr-1.5 pl-3',
          p.editing && 'widget-drag-handle cursor-grab active:cursor-grabbing',
        )}
      >
        {p.editing && <GripVertical className="text-muted-foreground/70 -ml-1 size-4 shrink-0" aria-hidden />}
        <h3 className="min-w-0 truncate text-[13px] font-medium" title={p.title}>
          {p.title}
        </h3>
        {p.readOnly && (
          <Tooltip>
            <TooltipTrigger asChild>
              <span
                className="no-drag text-muted-foreground inline-flex shrink-0"
                aria-label="Read-only"
                data-testid="badge-readonly"
                tabIndex={0}
              >
                <Lock className="size-3.5" />
              </span>
            </TooltipTrigger>
            <TooltipContent>Read-only: you can view this element but not control it</TooltipContent>
          </Tooltip>
        )}
        {p.offline && (
          <Badge variant="destructive" className="shrink-0" data-testid="badge-offline">
            <WifiOff /> Offline
          </Badge>
        )}
        {p.stale && !p.offline && (
          <Badge variant="warning" className="shrink-0">
            Stale
          </Badge>
        )}
        <div className="no-drag ml-auto flex shrink-0 items-center gap-1">
          {p.actions}
          {p.editing && (
            <>
              <Button
                variant="ghost"
                size="icon-xs"
                onClick={p.onConfigure}
                aria-label={`Configure ${p.title}`}
                data-testid="widget-configure"
              >
                <Settings2 />
              </Button>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    aria-label={`More actions for ${p.title}`}
                    data-testid="widget-menu"
                  >
                    <MoreHorizontal />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem onSelect={p.onConfigure}>
                    <Settings2 /> Configure
                  </DropdownMenuItem>
                  <DropdownMenuItem onSelect={p.onDuplicate}>
                    <Copy /> Duplicate
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem variant="destructive" onSelect={p.onRemove}>
                    <Trash2 /> Remove
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </>
          )}
        </div>
      </header>
      <div
        className={cn(
          'relative min-h-0 flex-1 px-3',
          p.editing && 'pointer-events-none select-none [&_.no-drag]:pointer-events-auto',
        )}
      >
        {p.children}
      </div>
      <footer className="text-muted-foreground flex h-7 shrink-0 items-center justify-between gap-2 px-3 text-[11px]">
        <span className="truncate">{p.subtitle}</span>
        {p.lastEditAt ? (
          <Tooltip>
            <TooltipTrigger asChild>
              <span className="no-drag tabular shrink-0" tabIndex={0} data-testid="last-updated">
                <RelativeTime value={p.lastEditAt} />
              </span>
            </TooltipTrigger>
            <TooltipContent>
              Last update {formatDateTime(p.lastEditAt)}
              {p.lastEditBy?.username && (
                <>
                  <br />
                  by {p.lastEditBy.username} ({p.lastEditBy.source === 'device' ? 'device' : 'user'})
                </>
              )}
            </TooltipContent>
          </Tooltip>
        ) : (
          <span className="shrink-0">No data yet</span>
        )}
      </footer>
    </section>
  )
}

/** Centered message used for the widget's non-data states. */
export function WidgetMessage({
  icon: Icon,
  tone = 'muted',
  title,
  description,
  action,
}: {
  icon: typeof AlertTriangle
  tone?: 'muted' | 'error'
  title: string
  description?: string
  action?: ReactNode
}) {
  return (
    <div
      className="flex h-full flex-col items-center justify-center gap-1.5 px-2 text-center"
      role={tone === 'error' ? 'alert' : undefined}
    >
      <Icon className={cn('size-5', tone === 'error' ? 'text-destructive' : 'text-muted-foreground')} />
      <div className="text-sm font-medium">{title}</div>
      {description && (
        <div className="text-muted-foreground line-clamp-3 max-w-xs text-xs">{description}</div>
      )}
      {action && <div className="no-drag mt-1">{action}</div>}
    </div>
  )
}

export const WidgetStates = {
  unbound: (onConfigure?: () => void) => (
    <WidgetMessage
      icon={Link2Off}
      title="No element selected"
      description={onConfigure ? 'Choose which element this widget shows.' : 'This widget is not configured.'}
      action={
        onConfigure && (
          <Button size="xs" variant="outline" onClick={onConfigure}>
            Choose element
          </Button>
        )
      }
    />
  ),
  unavailable: () => (
    <WidgetMessage
      icon={ShieldOff}
      title="Element unavailable"
      description="You do not have access to this element, or it was deleted."
    />
  ),
  revoked: (reason: string | null, retry: () => void) => (
    <WidgetMessage
      icon={ShieldOff}
      tone="error"
      title="Permission revoked"
      description={reason ?? 'Your access to this element was removed.'}
      action={
        <Button size="xs" variant="outline" onClick={retry}>
          <RefreshCw /> Retry
        </Button>
      }
    />
  ),
  error: (description: string | undefined, retry: () => void) => (
    <WidgetMessage
      icon={AlertTriangle}
      tone="error"
      title="Not subscribed"
      description={description}
      action={
        <Button size="xs" variant="outline" onClick={retry}>
          <RefreshCw /> Retry
        </Button>
      }
    />
  ),
  loading: () => (
    <div className="flex h-full flex-col justify-center gap-2 py-2" aria-busy="true" aria-label="Loading">
      <Skeleton className="h-6 w-1/2" />
      <Skeleton className="h-full max-h-24 w-full" />
    </div>
  ),
}
