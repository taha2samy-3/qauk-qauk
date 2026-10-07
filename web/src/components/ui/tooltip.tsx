import * as React from 'react'
import { Tooltip as P } from 'radix-ui'
import { cn } from '@/lib/utils'

export const TooltipProvider = P.Provider
export const Tooltip = P.Root
export const TooltipTrigger = P.Trigger

export function TooltipContent({
  className,
  sideOffset = 6,
  children,
  ...props
}: React.ComponentProps<typeof P.Content>) {
  return (
    <P.Portal>
      <P.Content
        sideOffset={sideOffset}
        className={cn(
          'bg-foreground text-background data-[state=delayed-open]:animate-in z-50 max-w-xs rounded-md px-2.5 py-1.5 text-xs shadow-md',
          className,
        )}
        {...props}
      >
        {children}
      </P.Content>
    </P.Portal>
  )
}

/** Convenience wrapper: <Hint label="...">trigger</Hint> */
export function Hint({
  label,
  children,
  side,
}: {
  label: React.ReactNode
  children: React.ReactNode
  side?: React.ComponentProps<typeof P.Content>['side']
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>{children}</TooltipTrigger>
      <TooltipContent side={side}>{label}</TooltipContent>
    </Tooltip>
  )
}
