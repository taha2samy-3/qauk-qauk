/**
 * Realtime client for the legacy browser WebSocket protocol (/browser/simple/).
 *
 * - One socket per tab, shared by every widget.
 * - Reference-counted subscriptions: N widgets on one element = 1 subscribe.
 * - Reconnects with exponential backoff + jitter and re-subscribes.
 * - Exposes immutable per-element snapshots for useSyncExternalStore.
 */
import { isRecord } from '@/lib/utils'
import type { Permission } from '@/api/types'

export type ConnectionStatus = 'idle' | 'connecting' | 'open' | 'reconnecting' | 'offline' | 'closed'

export type ElementStatus =
  /** not subscribed (no widget uses it) */
  | 'idle'
  /** waiting for the socket or for the subscribe confirmation */
  | 'subscribing'
  | 'subscribed'
  /** the server force-unsubscribed us (permission revoked, element deleted) */
  | 'revoked'
  /** subscribe was rejected (e.g. permission_denied) */
  | 'error'

export interface Actor {
  userId: string | number | null
  username: string
  /** device frames carry the device id (a string); user frames a numeric user id */
  source: 'device' | 'user'
}

export interface Frame {
  message: unknown
  at: string
  by: Actor
}

export interface RealtimeError {
  code: string
  description: string
}

export interface ElementState {
  elementId: string
  status: ElementStatus
  permission: Permission | null
  details: unknown
  deviceConnected: boolean | null
  /** latest message (raw) */
  message: unknown
  /** replayed history + live frames, oldest first, bounded */
  history: readonly Frame[]
  lastEditBy: Actor | null
  lastEditAt: string | null
  error: RealtimeError | null
  reason: string | null
}

export interface ServerError extends RealtimeError {
  elementId?: string
}

type Listener = () => void

export interface BackoffOptions {
  /** first retry delay (ms) */
  baseMs: number
  /** cap (ms) */
  maxMs: number
}

export interface RealtimeClientOptions {
  url: string | (() => string)
  WebSocketImpl?: typeof WebSocket
  backoff?: Partial<BackoffOptions>
  /** max frames kept per element */
  historyLimit?: number
  random?: () => number
  setTimeout?: (fn: () => void, ms: number) => unknown
  clearTimeout?: (h: unknown) => void
}

const OPEN = 1

export function emptyState(elementId: string): ElementState {
  return {
    elementId,
    status: 'idle',
    permission: null,
    details: null,
    deviceConnected: null,
    message: undefined,
    history: [],
    lastEditBy: null,
    lastEditAt: null,
    error: null,
    reason: null,
  }
}

const IDLE = new Map<string, ElementState>()
function idleState(id: string): ElementState {
  let s = IDLE.get(id)
  if (!s) {
    s = Object.freeze(emptyState(id))
    IDLE.set(id, s)
  }
  return s
}

/** Delay before reconnect attempt `attempt` (0-based): "equal jitter" backoff. */
export function backoffDelay(
  attempt: number,
  opts: BackoffOptions,
  random: () => number = Math.random,
): number {
  const exp = Math.min(opts.maxMs, opts.baseMs * 2 ** attempt)
  return Math.round(exp / 2 + random() * (exp / 2))
}

export class RealtimeClient {
  private readonly opts: Required<Omit<RealtimeClientOptions, 'backoff'>> & { backoff: BackoffOptions }
  private ws: WebSocket | null = null
  private status: ConnectionStatus = 'idle'
  private attempt = 0
  private retryTimer: unknown = null
  private wanted = false
  private readonly refs = new Map<string, number>()
  private readonly states = new Map<string, ElementState>()
  /** unsubscribe acks we expect (client-initiated) per element */
  private readonly pendingUnsub = new Map<string, number>()
  private readonly elementListeners = new Map<string, Set<Listener>>()
  private readonly statusListeners = new Set<Listener>()
  private readonly errorListeners = new Set<(e: ServerError) => void>()
  private readonly frameListeners = new Map<string, Set<(f: Frame) => void>>()
  private nextRetryAt: number | null = null

  constructor(options: RealtimeClientOptions) {
    this.opts = {
      url: options.url,
      WebSocketImpl: options.WebSocketImpl ?? globalThis.WebSocket,
      historyLimit: options.historyLimit ?? 1000,
      random: options.random ?? Math.random,
      setTimeout: options.setTimeout ?? ((fn, ms) => globalThis.setTimeout(fn, ms)),
      clearTimeout: options.clearTimeout ?? ((h) => globalThis.clearTimeout(h as number)),
      backoff: { baseMs: 1000, maxMs: 30_000, ...options.backoff },
    }
  }

  // ---------------------------------------------------------------- lifecycle

  /** Open the socket (idempotent). */
  connect(): void {
    this.wanted = true
    if (this.ws || this.retryTimer !== null) return
    this.open()
  }

  /** Close the socket and stop reconnecting (e.g. on logout). Subscriptions are kept. */
  close(): void {
    this.wanted = false
    this.clearRetry()
    const ws = this.ws
    this.ws = null
    if (ws) {
      ws.onopen = ws.onclose = ws.onerror = ws.onmessage = null
      try {
        ws.close(1000, 'client closed')
      } catch {
        /* ignore */
      }
    }
    this.pendingUnsub.clear()
    for (const id of this.refs.keys()) this.patch(id, { status: 'subscribing' })
    this.setStatus('closed')
  }

  /** Reconnect now, skipping any pending backoff (e.g. when the browser comes back online). */
  retryNow(): void {
    if (!this.wanted || this.status === 'open' || this.status === 'connecting') return
    this.clearRetry()
    this.attempt = 0
    this.open()
  }

  /** Browser went offline: drop the socket and wait for retryNow(). */
  markOffline(): void {
    if (!this.wanted) return
    this.clearRetry()
    this.dropSocket()
    this.setStatus('offline')
  }

  getStatus(): ConnectionStatus {
    return this.status
  }

  getNextRetryAt(): number | null {
    return this.nextRetryAt
  }

  onStatus(fn: Listener): () => void {
    this.statusListeners.add(fn)
    return () => this.statusListeners.delete(fn)
  }

  onError(fn: (e: ServerError) => void): () => void {
    this.errorListeners.add(fn)
    return () => this.errorListeners.delete(fn)
  }

  /** Called for every live/replayed frame of an element (after state is updated). */
  onFrame(elementId: string, fn: (f: Frame) => void): () => void {
    let set = this.frameListeners.get(elementId)
    if (!set) this.frameListeners.set(elementId, (set = new Set()))
    set.add(fn)
    return () => {
      set.delete(fn)
      if (set.size === 0) this.frameListeners.delete(elementId)
    }
  }

  // ------------------------------------------------------------ subscriptions

  /** Reference-counted subscribe. Returns the matching release function. */
  retain(elementId: string): () => void {
    const n = this.refs.get(elementId) ?? 0
    this.refs.set(elementId, n + 1)
    if (n === 0) {
      this.states.set(elementId, { ...emptyState(elementId), status: 'subscribing' })
      this.emit(elementId)
      this.sendSubscribe(elementId)
      this.connect()
    }
    let released = false
    return () => {
      if (released) return
      released = true
      this.release(elementId)
    }
  }

  private release(elementId: string): void {
    const n = this.refs.get(elementId) ?? 0
    if (n > 1) {
      this.refs.set(elementId, n - 1)
      return
    }
    this.refs.delete(elementId)
    this.states.delete(elementId)
    if (this.isOpen()) {
      this.pendingUnsub.set(elementId, (this.pendingUnsub.get(elementId) ?? 0) + 1)
      this.raw({ type: 'unsubscribe', element_id: elementId })
    }
    this.emit(elementId)
  }

  /** Retry a revoked/failed subscription (e.g. after permissions were restored). */
  resubscribe(elementId: string): void {
    if (!this.refs.has(elementId)) return
    this.patch(elementId, { status: 'subscribing', error: null, reason: null })
    this.sendSubscribe(elementId)
  }

  refCount(elementId: string): number {
    return this.refs.get(elementId) ?? 0
  }

  getSnapshot(elementId: string): ElementState {
    return this.states.get(elementId) ?? idleState(elementId)
  }

  listen(elementId: string, fn: Listener): () => void {
    let set = this.elementListeners.get(elementId)
    if (!set) this.elementListeners.set(elementId, (set = new Set()))
    set.add(fn)
    return () => {
      set.delete(fn)
      if (set.size === 0) this.elementListeners.delete(elementId)
    }
  }

  /**
   * Send a control message. Returns false (and sends nothing) when the socket
   * is down or the user lacks RC on the element.
   */
  send(elementId: string, message: Record<string, unknown>): boolean {
    const st = this.states.get(elementId)
    if (!this.isOpen() || !st || st.status !== 'subscribed' || st.permission !== 'RC') return false
    this.raw({ type: 'message_element', element_id: elementId, message })
    return true
  }

  // ------------------------------------------------------------------ socket

  private isOpen(): boolean {
    return this.ws !== null && this.ws.readyState === OPEN && this.status === 'open'
  }

  private resolveUrl(): string {
    return typeof this.opts.url === 'function' ? this.opts.url() : this.opts.url
  }

  private open(): void {
    this.nextRetryAt = null
    this.setStatus(this.attempt === 0 ? 'connecting' : 'reconnecting')
    let ws: WebSocket
    try {
      ws = new this.opts.WebSocketImpl(this.resolveUrl())
    } catch {
      this.scheduleReconnect()
      return
    }
    this.ws = ws
    ws.onopen = () => {
      if (this.ws !== ws) return
      this.attempt = 0
      this.pendingUnsub.clear()
      this.setStatus('open')
      for (const id of this.refs.keys()) {
        // The server replays history on subscribe: start each element fresh.
        const prev = this.states.get(id)
        this.states.set(id, {
          ...emptyState(id),
          status: 'subscribing',
          // keep the last known values visible while the replay arrives
          message: prev?.message,
          lastEditAt: prev?.lastEditAt ?? null,
          lastEditBy: prev?.lastEditBy ?? null,
        })
        this.emit(id)
        this.raw({ type: 'subscribe', element_id: id })
      }
    }
    ws.onmessage = (ev: MessageEvent) => {
      if (this.ws !== ws) return
      this.handle(ev.data)
    }
    ws.onerror = () => {
      /* onclose follows and handles the retry */
    }
    ws.onclose = () => {
      if (this.ws !== ws) return
      this.ws = null
      this.pendingUnsub.clear()
      for (const id of this.refs.keys()) this.patch(id, { status: 'subscribing' })
      if (this.wanted) this.scheduleReconnect()
      else this.setStatus('closed')
    }
  }

  private dropSocket(): void {
    const ws = this.ws
    this.ws = null
    if (!ws) return
    ws.onopen = ws.onclose = ws.onerror = ws.onmessage = null
    try {
      ws.close()
    } catch {
      /* ignore */
    }
    for (const id of this.refs.keys()) this.patch(id, { status: 'subscribing' })
  }

  private scheduleReconnect(): void {
    this.clearRetry()
    const delay = backoffDelay(this.attempt, this.opts.backoff, this.opts.random)
    this.attempt += 1
    this.nextRetryAt = Date.now() + delay
    this.setStatus('reconnecting')
    this.retryTimer = this.opts.setTimeout(() => {
      this.retryTimer = null
      if (this.wanted) this.open()
    }, delay)
  }

  private clearRetry(): void {
    if (this.retryTimer !== null) this.opts.clearTimeout(this.retryTimer)
    this.retryTimer = null
    this.nextRetryAt = null
  }

  private raw(frame: Record<string, unknown>): void {
    if (this.ws && this.ws.readyState === OPEN) this.ws.send(JSON.stringify(frame))
  }

  private sendSubscribe(elementId: string): void {
    if (this.isOpen()) this.raw({ type: 'subscribe', element_id: elementId })
  }

  // ---------------------------------------------------------------- frames

  private handle(data: unknown): void {
    if (typeof data !== 'string') return
    let f: unknown
    try {
      f = JSON.parse(data)
    } catch {
      return
    }
    if (!isRecord(f)) return
    const id = typeof f.element_id === 'string' ? f.element_id : undefined
    switch (f.type) {
      case 'subscribe': {
        if (!id || !this.refs.has(id)) return
        this.patch(id, {
          status: 'subscribed',
          permission: f.permissions === 'RC' ? 'RC' : 'R',
          details: f.details ?? null,
          deviceConnected: typeof f.connected === 'boolean' ? f.connected : null,
          history: [],
          error: null,
          reason: null,
        })
        return
      }
      case 'message_element': {
        if (!id || !this.refs.has(id)) return
        const auth = isRecord(f.auth) ? f.auth : {}
        const userId =
          typeof auth.user_id === 'number' || typeof auth.user_id === 'string' ? auth.user_id : null
        const frame: Frame = {
          message: f.message,
          at: typeof f.last_edit_at === 'string' ? f.last_edit_at : new Date().toISOString(),
          by: {
            userId,
            username: typeof auth.username === 'string' ? auth.username : '',
            source: typeof userId === 'number' ? 'user' : 'device',
          },
        }
        const st = this.states.get(id)!
        const history =
          st.history.length >= this.opts.historyLimit
            ? [...st.history.slice(st.history.length - this.opts.historyLimit + 1), frame]
            : [...st.history, frame]
        this.patch(id, { message: frame.message, history, lastEditAt: frame.at, lastEditBy: frame.by })
        this.frameListeners.get(id)?.forEach((fn) => fn(frame))
        return
      }
      case 'permissions_update': {
        if (!id || !this.refs.has(id)) return
        this.patch(id, { permission: f.permissions === 'RC' ? 'RC' : 'R' })
        return
      }
      case 'element_connection_status': {
        if (!id || !this.refs.has(id)) return
        this.patch(id, { deviceConnected: f.status === 'connected' })
        return
      }
      case 'unsubscribe': {
        if (!id) return
        const pending = this.pendingUnsub.get(id) ?? 0
        const forced = typeof f.reason === 'string' && f.reason !== ''
        if (!forced && pending > 0) {
          // ack of our own unsubscribe
          if (pending === 1) this.pendingUnsub.delete(id)
          else this.pendingUnsub.set(id, pending - 1)
          return
        }
        if (!this.refs.has(id)) return
        this.patch(id, {
          status: 'revoked',
          reason: typeof f.reason === 'string' && f.reason ? f.reason : 'Unsubscribed by the server',
        })
        return
      }
      case 'error': {
        const err: ServerError = {
          code: typeof f.error_code === 'string' ? f.error_code : 'error',
          description: typeof f.description === 'string' ? f.description : 'Unknown error',
          elementId: id,
        }
        if (id && this.refs.has(id)) {
          const st = this.states.get(id)!
          // A failed subscribe is terminal; other errors (e.g. a rejected
          // command) are surfaced but keep the subscription alive.
          this.patch(id, st.status === 'subscribing' ? { status: 'error', error: err } : { error: err })
        }
        this.errorListeners.forEach((fn) => fn(err))
        return
      }
      default:
        return
    }
  }

  // ----------------------------------------------------------------- notify

  private patch(id: string, p: Partial<ElementState>): void {
    const st = this.states.get(id)
    if (!st) return
    this.states.set(id, { ...st, ...p })
    this.emit(id)
  }

  private emit(id: string): void {
    this.elementListeners.get(id)?.forEach((fn) => fn())
  }

  private setStatus(s: ConnectionStatus): void {
    if (this.status === s) return
    this.status = s
    this.statusListeners.forEach((fn) => fn())
  }
}

/** ws(s)://<host>/browser/simple/ for the current page. */
export function defaultSocketUrl(): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}/browser/simple/`
}
