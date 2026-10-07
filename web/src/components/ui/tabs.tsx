import * as React from 'react'
import { Tabs as P } from 'radix-ui'
import { cn } from '@/lib/utils'

export const Tabs = P.Root
export function TabsList({ className, ...props }: React.ComponentProps<typeof P.List>) {
  return (
    <P.List
      className={cn(
        'bg-muted text-muted-foreground inline-flex h-9 w-fit items-center justify-center rounded-lg p-[3px]',
        className,
      )}
      {...props}
    />
  )
}
export function TabsTrigger({ className, ...props }: React.ComponentProps<typeof P.Trigger>) {
  return (
    <P.Trigger
      className={cn(
        'focus-visible:ring-ring/50 data-[state=active]:bg-card data-[state=active]:text-foreground dark:data-[state=active]:bg-input/40 inline-flex h-full flex-1 items-center justify-center gap-1.5 rounded-md px-2.5 text-sm font-medium whitespace-nowrap transition-[color,box-shadow] focus-visible:ring-[3px] focus-visible:outline-none disabled:pointer-events-none disabled:opacity-50 data-[state=active]:shadow-sm',
        className,
      )}
      {...props}
    />
  )
}
export function TabsContent({ className, ...props }: React.ComponentProps<typeof P.Content>) {
  return <P.Content className={cn('flex-1 outline-none', className)} {...props} />
}
