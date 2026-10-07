import * as React from 'react'
import { cn } from '@/lib/utils'
import { Label } from './label'

/** Label + control + description + error, wired for a11y. */
export function Field({
  label,
  htmlFor,
  description,
  error,
  className,
  children,
  required,
}: {
  label?: React.ReactNode
  htmlFor?: string
  description?: React.ReactNode
  error?: string
  className?: string
  children: React.ReactNode
  required?: boolean
}) {
  return (
    <div className={cn('grid gap-1.5', className)}>
      {label && (
        <Label htmlFor={htmlFor}>
          {label}
          {required && <span className="text-destructive">*</span>}
        </Label>
      )}
      {children}
      {description && !error && <p className="text-muted-foreground text-xs">{description}</p>}
      {error && (
        <p role="alert" className="text-destructive text-xs font-medium">
          {error}
        </p>
      )}
    </div>
  )
}
