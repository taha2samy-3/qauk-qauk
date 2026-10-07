import type { LucideIcon } from 'lucide-react'
import { AlertTriangle, RefreshCw } from 'lucide-react'
import type { ReactNode } from 'react'
import { errorMessage } from '@/api/problem'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

/** Brand illustrations in `public/ducks/` (source: `branding/ducks/`). */
export type DuckPose =
  'hello' | 'sleeping' | 'detective' | 'builder' | 'analyst' | 'guardian' | 'plugged' | 'celebrate' | 'lost'

export function Duck({ pose, className }: { pose: DuckPose; className?: string }) {
  return (
    <img
      src={`${import.meta.env.BASE_URL}ducks/duck-${pose}.svg`}
      alt=""
      aria-hidden="true"
      draggable={false}
      data-testid={`duck-${pose}`}
      className={cn('size-36 select-none', className)}
    />
  )
}

export function EmptyState({
  icon: Icon,
  duck,
  title,
  description,
  action,
  className,
}: {
  icon?: LucideIcon
  /** an illustration instead of the icon */
  duck?: DuckPose
  title: ReactNode
  description?: ReactNode
  action?: ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center gap-3 rounded-xl border border-dashed px-6 py-14 text-center',
        className,
      )}
    >
      {duck ? (
        <Duck pose={duck} className="-mb-2" />
      ) : (
        Icon && (
          <div className="bg-muted text-muted-foreground flex size-11 items-center justify-center rounded-full">
            <Icon className="size-5" />
          </div>
        )
      )}
      <div className="space-y-1">
        <h3 className="font-semibold">{title}</h3>
        {description && <p className="text-muted-foreground mx-auto max-w-sm text-sm">{description}</p>}
      </div>
      {action && <div className="mt-1 flex flex-wrap justify-center gap-2">{action}</div>}
    </div>
  )
}

export function ErrorState({
  error,
  onRetry,
  title = 'Something went wrong',
  duck,
  className,
}: {
  error: unknown
  onRetry?: () => void
  title?: string
  duck?: DuckPose
  className?: string
}) {
  return (
    <div
      role="alert"
      className={cn(
        'border-destructive/30 bg-destructive/5 flex flex-col items-center justify-center gap-3 rounded-xl border px-6 py-10 text-center',
        className,
      )}
    >
      {duck ? (
        <Duck pose={duck} className="-mb-2 size-32" />
      ) : (
        <AlertTriangle className="text-destructive size-6" />
      )}
      <div className="space-y-1">
        <h3 className="font-semibold">{title}</h3>
        <p className="text-muted-foreground mx-auto max-w-md text-sm">{errorMessage(error)}</p>
      </div>
      {onRetry && (
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RefreshCw /> Try again
        </Button>
      )}
    </div>
  )
}

export function TableSkeleton({ rows = 5, cols = 4 }: { rows?: number; cols?: number }) {
  return (
    <div className="divide-y" aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }, (_, r) => (
        <div key={r} className="flex gap-4 px-3 py-3.5">
          {Array.from({ length: cols }, (_, c) => (
            <Skeleton key={c} className={cn('h-4', c === 0 ? 'w-1/4' : 'flex-1')} />
          ))}
        </div>
      ))}
    </div>
  )
}

export function PageHeader({
  title,
  description,
  actions,
  children,
}: {
  title: ReactNode
  description?: ReactNode
  actions?: ReactNode
  children?: ReactNode
}) {
  return (
    <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
      <div className="min-w-0 space-y-1">
        <h1 className="truncate text-2xl font-semibold tracking-tight">{title}</h1>
        {description && <p className="text-muted-foreground text-sm">{description}</p>}
        {children}
      </div>
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}
