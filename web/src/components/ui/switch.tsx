import * as React from 'react'
import { Switch as P } from 'radix-ui'
import { cn } from '@/lib/utils'

export function Switch({
  className,
  size = 'default',
  ...props
}: React.ComponentProps<typeof P.Root> & { size?: 'default' | 'lg' }) {
  return (
    <P.Root
      data-slot="switch"
      data-size={size}
      className={cn(
        'peer focus-visible:ring-ring/50 data-[state=checked]:bg-primary data-[state=unchecked]:bg-input dark:data-[state=unchecked]:bg-input/80 inline-flex shrink-0 items-center rounded-full border border-transparent shadow-xs transition-all outline-none focus-visible:ring-[3px] disabled:cursor-not-allowed disabled:opacity-50 data-[size=default]:h-5 data-[size=default]:w-9 data-[size=lg]:h-8 data-[size=lg]:w-14',
        className,
      )}
      {...props}
    >
      <P.Thumb
        className={cn(
          'bg-background dark:data-[state=checked]:bg-primary-foreground pointer-events-none block rounded-full shadow-sm ring-0 transition-transform data-[state=unchecked]:translate-x-0.5',
          size === 'lg'
            ? 'size-7 data-[state=checked]:translate-x-[calc(100%-1px)]'
            : 'size-4 data-[state=checked]:translate-x-[calc(100%-2px)]',
        )}
      />
    </P.Root>
  )
}
