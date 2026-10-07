import type { FieldValues, Path, UseFormSetError } from 'react-hook-form'
import { toast } from 'sonner'
import { ApiError, errorMessage } from '@/api/problem'

/**
 * Map an RFC 9457 problem onto a react-hook-form: field errors go to their
 * fields (`body.username` → `username`), anything else is returned as a form-level message.
 */
export function applyProblem<T extends FieldValues>(
  e: unknown,
  setError: UseFormSetError<T>,
  fields: readonly string[],
): string | undefined {
  if (e instanceof ApiError) {
    const fe = e.fieldErrors()
    let mapped = 0
    for (const [k, msg] of Object.entries(fe)) {
      if (fields.includes(k)) {
        setError(k as Path<T>, { type: 'server', message: msg })
        mapped++
      }
    }
    if (mapped > 0 && mapped === Object.keys(fe).length) return undefined
    return mapped > 0 ? (e.detail && e.detail !== 'validation failed' ? e.detail : undefined) : e.description
  }
  return errorMessage(e)
}

export function toastError(title: string, e: unknown) {
  toast.error(title, { description: errorMessage(e) })
}
