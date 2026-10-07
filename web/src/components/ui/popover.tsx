import * as React from 'react'
import { Popover as P } from 'radix-ui'
import { cn } from '@/lib/utils'

export const Popover = P.Root
export const PopoverTrigger = P.Trigger
export const PopoverAnchor = P.Anchor

export function PopoverContent({
  className,
  align = 'center',
  sideOffset = 4,
  ...props
}: React.ComponentProps<typeof P.Content>) {
  return (
    <P.Portal>
      <P.Content
        align={align}
        sideOffset={sideOffset}
        className={cn(
          'bg-popover text-popover-foreground data-[state=open]:animate-in z-50 w-72 rounded-lg border p-4 shadow-lg outline-hidden',
          className,
        )}
        {...props}
      />
    </P.Portal>
  )
}
