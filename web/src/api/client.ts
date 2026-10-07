import createClient, { type Middleware } from 'openapi-fetch'
import type { paths } from './schema'
import { ApiError } from './problem'

type Listener = () => void
const unauthorizedListeners = new Set<Listener>()

/** Subscribe to "session expired" (any 401 outside the auth endpoints). */
export function onUnauthorized(fn: Listener): () => void {
  unauthorizedListeners.add(fn)
  return () => unauthorizedListeners.delete(fn)
}

const authWatch: Middleware = {
  onResponse({ response, request }) {
    if (response.status === 401 && !new URL(request.url).pathname.startsWith('/api/v1/auth/')) {
      unauthorizedListeners.forEach((fn) => fn())
    }
    return response
  },
}

/**
 * Typed client generated from the backend's OpenAPI document. Same-origin
 * (Vite proxy in dev, Go server in prod), so the session cookie and the
 * server's cross-origin protection work without extra headers.
 */
export const api = createClient<paths>({ baseUrl: '', credentials: 'same-origin' })
api.use(authWatch)

type FetchResult<T> = { data?: T; error?: unknown; response: Response }

/** Resolve an openapi-fetch call to its data, or throw an ApiError. */
export async function unwrap<T>(p: Promise<FetchResult<T>>): Promise<T> {
  let res: FetchResult<T>
  try {
    res = await p
  } catch (e) {
    throw new ApiError(
      0,
      { title: 'Network error', detail: 'Could not reach the server. Check your connection.' },
      String(e),
    )
  }
  if (res.error !== undefined || !res.response.ok) {
    const body = (typeof res.error === 'object' && res.error !== null ? res.error : undefined) as
      ConstructorParameters<typeof ApiError>[1] | undefined
    throw new ApiError(res.response.status, body, res.response.statusText)
  }
  return res.data as T
}
