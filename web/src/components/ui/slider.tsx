import * as React from 'react'
import { Slider as P } from 'radix-ui'
import { cn } from '@/lib/utils'

export function Slider({ className, ...props }: React.ComponentProps<typeof P.Root>) {
  const count = (props.value ?? props.defaultValue ?? [0]).length
  return (
    <P.Root
      data-slot="slider"
      className={cn(
        'relative flex w-full touch-none items-center select-none data-[disabled]:opacity-50',
        className,
      )}
      {...props}
    >
      <P.Track className="bg-muted relative h-1.5 w-full grow overflow-hidden rounded-full">
        <P.Range className="bg-primary absolute h-full" />
      </P.Track>
      {Array.from({ length: count }, (_, i) => (
        <P.Thumb
          key={i}
          aria-label={props['aria-label']}
          className="border-primary bg-background hover:ring-ring/30 focus-visible:ring-ring/40 block size-4 shrink-0 rounded-full border shadow-sm transition-[color,box-shadow] hover:ring-4 focus-visible:ring-4 focus-visible:outline-hidden disabled:pointer-events-none"
        />
      ))}
    </P.Root>
  )
}
