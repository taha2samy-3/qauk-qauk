import * as React from 'react'
import { ToggleGroup as P } from 'radix-ui'
import { cn } from '@/lib/utils'

export function ToggleGroup({ className, ...props }: React.ComponentProps<typeof P.Root>) {
  return (
    <P.Root
      className={cn('bg-muted/50 inline-flex items-center rounded-md border p-0.5', className)}
      {...props}
    />
  )
}
export function ToggleGroupItem({ className, ...props }: React.ComponentProps<typeof P.Item>) {
  return (
    <P.Item
      className={cn(
        'text-muted-foreground hover:text-foreground focus-visible:ring-ring/50 data-[state=on]:bg-card data-[state=on]:text-foreground dark:data-[state=on]:bg-input/50 inline-flex h-6 min-w-8 items-center justify-center rounded px-1.5 text-[11px] font-medium transition-colors focus-visible:ring-[3px] focus-visible:outline-none data-[state=on]:shadow-xs',
        className,
      )}
      {...props}
    />
  )
}
