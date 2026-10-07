import type { components } from './schema'

type ErrorModel = components['schemas']['ErrorModel']
export type ErrorDetail = components['schemas']['ErrorDetail']

/** An RFC 9457 problem+json response, as a throwable Error. */
export class ApiError extends Error {
  readonly status: number
  readonly title: string
  readonly detail?: string
  readonly errors: ErrorDetail[]

  constructor(status: number, body: Partial<ErrorModel> | undefined, fallback?: string) {
    const title = body?.title || fallback || `Request failed (${status})`
    super(body?.detail || title)
    this.name = 'ApiError'
    this.status = status
    this.title = title
    this.detail = body?.detail
    this.errors = body?.errors ?? []
  }

  /** Field errors keyed by the last segment of `location` (body.username → username). */
  fieldErrors(): Record<string, string> {
    const out: Record<string, string> = {}
    for (const e of this.errors) {
      if (!e.location) continue
      const key = e.location.replace(/^(body|query|path)\.?/, '').replace(/\[\d+\]/g, '') || e.location
      out[key] ??= e.message ?? 'Invalid value'
    }
    return out
  }

  /** A one-line human message: detail, plus field messages when present. */
  get description(): string {
    const fields = this.errors
      .map((e) => (e.location ? `${e.location.replace(/^body\./, '')}: ${e.message}` : e.message))
      .filter(Boolean)
    if (fields.length)
      return `${this.detail && this.detail !== 'validation failed' ? this.detail + ' — ' : ''}${fields.join('; ')}`
    return this.detail || this.title
  }
}

export function isApiError(e: unknown): e is ApiError {
  return e instanceof ApiError
}

export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) return e.description
  if (e instanceof Error) return e.message
  return String(e)
}
