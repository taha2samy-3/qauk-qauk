import * as React from 'react'
import { DropdownMenu as P } from 'radix-ui'
import { CheckIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

export const DropdownMenu = P.Root
export const DropdownMenuTrigger = P.Trigger
export const DropdownMenuGroup = P.Group

export function DropdownMenuContent({
  className,
  sideOffset = 4,
  ...props
}: React.ComponentProps<typeof P.Content>) {
  return (
    <P.Portal>
      <P.Content
        sideOffset={sideOffset}
        className={cn(
          'bg-popover text-popover-foreground data-[state=open]:animate-in z-50 max-h-(--radix-dropdown-menu-content-available-height) min-w-[10rem] overflow-x-hidden overflow-y-auto rounded-lg border p-1 shadow-lg',
          className,
        )}
        {...props}
      />
    </P.Portal>
  )
}
export function DropdownMenuItem({
  className,
  variant = 'default',
  ...props
}: React.ComponentProps<typeof P.Item> & { variant?: 'default' | 'destructive' }) {
  return (
    <P.Item
      data-variant={variant}
      className={cn(
        "focus:bg-accent focus:text-accent-foreground data-[variant=destructive]:text-destructive data-[variant=destructive]:focus:bg-destructive/10 [&_svg:not([class*='text-'])]:text-muted-foreground data-[variant=destructive]:[&_svg]:!text-destructive relative flex cursor-default items-center gap-2 rounded-md px-2 py-1.5 text-sm outline-hidden select-none data-[disabled]:pointer-events-none data-[disabled]:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        className,
      )}
      {...props}
    />
  )
}
export function DropdownMenuCheckboxItem({
  className,
  children,
  ...props
}: React.ComponentProps<typeof P.CheckboxItem>) {
  return (
    <P.CheckboxItem
      className={cn(
        'focus:bg-accent relative flex cursor-default items-center gap-2 rounded-md py-1.5 pr-2 pl-8 text-sm outline-hidden select-none',
        className,
      )}
      {...props}
    >
      <span className="absolute left-2 flex size-3.5 items-center justify-center">
        <P.ItemIndicator>
          <CheckIcon className="size-4" />
        </P.ItemIndicator>
      </span>
      {children}
    </P.CheckboxItem>
  )
}
export function DropdownMenuLabel({ className, ...props }: React.ComponentProps<typeof P.Label>) {
  return <P.Label className={cn('px-2 py-1.5 text-sm font-medium', className)} {...props} />
}
export function DropdownMenuSeparator({ className, ...props }: React.ComponentProps<typeof P.Separator>) {
  return <P.Separator className={cn('bg-border -mx-1 my-1 h-px', className)} {...props} />
}
export function DropdownMenuShortcut({ className, ...props }: React.ComponentProps<'span'>) {
  return (
    <span className={cn('text-muted-foreground ml-auto text-xs tracking-widest', className)} {...props} />
  )
}
