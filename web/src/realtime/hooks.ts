import * as React from 'react'
import type { Permission } from '@/api/types'
import { useRealtimeClient } from './context'
import {
  emptyState,
  type Actor,
  type ConnectionStatus,
  type ElementState,
  type ElementStatus,
  type Frame,
  type RealtimeError,
} from './client'
import { messageValue } from './messages'

export interface UseElementResult {
  /** numeric value of the latest message ({value} or {y}) */
  value: number | undefined
  /** latest raw message */
  message: unknown
  history: readonly Frame[]
  permission: Permission | null
  deviceConnected: boolean | null
  lastEditBy: Actor | null
  lastEditAt: string | null
  status: ElementStatus
  error: RealtimeError | null
  reason: string | null
  details: unknown
  /** send a control message; false when not possible (no RC / socket down) */
  send: (message: Record<string, unknown>) => boolean
  retry: () => void
}

const NONE = emptyState('')

/** Live state of one element. Pass undefined to subscribe to nothing. */
export function useElement(elementId: string | undefined | null): UseElementResult {
  const rt = useRealtimeClient()

  React.useEffect(() => (elementId ? rt.retain(elementId) : undefined), [rt, elementId])

  const subscribe = React.useCallback(
    (fn: () => void) => (elementId ? rt.listen(elementId, fn) : () => {}),
    [rt, elementId],
  )
  const getSnapshot = React.useCallback(() => (elementId ? rt.getSnapshot(elementId) : NONE), [rt, elementId])
  const st: ElementState = React.useSyncExternalStore(subscribe, getSnapshot, getSnapshot)

  const send = React.useCallback(
    (m: Record<string, unknown>) => (elementId ? rt.send(elementId, m) : false),
    [rt, elementId],
  )
  const retry = React.useCallback(() => {
    if (elementId) rt.resubscribe(elementId)
  }, [rt, elementId])

  return {
    value: messageValue(st.message),
    message: st.message,
    history: st.history,
    permission: st.permission,
    deviceConnected: st.deviceConnected,
    lastEditBy: st.lastEditBy,
    lastEditAt: st.lastEditAt,
    status: st.status,
    error: st.error,
    reason: st.reason,
    details: st.details,
    send,
    retry,
  }
}

/** Socket-level status for the app shell indicator. */
export function useConnectionStatus(): {
  status: ConnectionStatus
  nextRetryAt: number | null
  retry: () => void
} {
  const rt = useRealtimeClient()
  const status = React.useSyncExternalStore(
    React.useCallback((fn) => rt.onStatus(fn), [rt]),
    () => rt.getStatus(),
  )
  return { status, nextRetryAt: rt.getNextRetryAt(), retry: () => rt.retryNow() }
}

/** Call `fn` for each frame of an element (replay + live). */
export function useElementFrames(elementId: string | undefined | null, fn: (f: Frame) => void): void {
  const rt = useRealtimeClient()
  const ref = React.useRef(fn)
  React.useLayoutEffect(() => {
    ref.current = fn
  })
  React.useEffect(
    () => (elementId ? rt.onFrame(elementId, (f) => ref.current(f)) : undefined),
    [rt, elementId],
  )
}

/**
 * Live state of several elements (e.g. the extra series of a chart). Returns
 * a stable array that only changes when one of the elements changes.
 */
export function useElementStates(ids: readonly string[]): readonly ElementState[] {
  const rt = useRealtimeClient()
  const key = ids.join(',')
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const stable = React.useMemo(() => ids.slice(), [key])
  React.useEffect(() => {
    const releases = stable.map((id) => rt.retain(id))
    return () => releases.forEach((r) => r())
  }, [rt, stable])
  const cache = React.useRef<readonly ElementState[]>([])
  const subscribe = React.useCallback(
    (fn: () => void) => {
      const offs = stable.map((id) => rt.listen(id, fn))
      return () => offs.forEach((o) => o())
    },
    [rt, stable],
  )
  const getSnapshot = React.useCallback(() => {
    const next = stable.map((id) => rt.getSnapshot(id))
    const prev = cache.current
    if (prev.length === next.length && prev.every((st, i) => st === next[i])) return prev
    cache.current = next
    return next
  }, [rt, stable])
  return React.useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
}
