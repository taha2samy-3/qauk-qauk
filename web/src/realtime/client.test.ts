import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FakeWebSocket } from '@/test/fakeWebSocket'
import { RealtimeClient, backoffDelay } from './client'

const EL = '01a1159f-d2d0-7e02-867d-4dd7fe247f99'
const EL2 = '01a1159f-d2fe-75bb-ba42-159ecda27b25'

function make(opts: Partial<ConstructorParameters<typeof RealtimeClient>[0]> = {}) {
  return new RealtimeClient({
    url: 'ws://test/browser/simple/',
    WebSocketImpl: FakeWebSocket as unknown as typeof WebSocket,
    random: () => 0.5,
    backoff: { baseMs: 1000, maxMs: 8000 },
    ...opts,
  })
}

function confirm(ws: FakeWebSocket, id = EL, permissions = 'RC', connected = true) {
  ws.serverSend({
    type: 'subscribe',
    element_id: id,
    subscribed: true,
    permissions,
    details: { unit: '°C' },
    connected,
  })
}

beforeEach(() => {
  FakeWebSocket.reset()
  vi.useFakeTimers()
})
afterEach(() => vi.useRealTimers())

describe('RealtimeClient', () => {
  it('opens one socket and subscribes once per element (reference counted)', () => {
    const rt = make()
    const a = rt.retain(EL)
    const b = rt.retain(EL)
    expect(FakeWebSocket.instances).toHaveLength(1)
    const ws = FakeWebSocket.last
    ws.serverOpen()
    expect(ws.sentOfType('subscribe')).toEqual([{ type: 'subscribe', element_id: EL }])
    expect(rt.refCount(EL)).toBe(2)

    a()
    expect(ws.sentOfType('unsubscribe')).toHaveLength(0)
    a() // double release is a no-op
    expect(rt.refCount(EL)).toBe(1)
    b()
    expect(ws.sentOfType('unsubscribe')).toEqual([{ type: 'unsubscribe', element_id: EL }])
    expect(rt.getSnapshot(EL).status).toBe('idle')
  })

  it('tracks subscribe confirmation, history replay and live frames', () => {
    const rt = make()
    rt.retain(EL)
    const ws = FakeWebSocket.last
    ws.serverOpen()
    expect(rt.getSnapshot(EL).status).toBe('subscribing')
    confirm(ws, EL, 'R', false)
    let s = rt.getSnapshot(EL)
    expect(s.status).toBe('subscribed')
    expect(s.permission).toBe('R')
    expect(s.deviceConnected).toBe(false)
    expect(s.details).toEqual({ unit: '°C' })

    ws.serverSend({
      type: 'message_element',
      element_id: EL,
      message: { value: 21.5 },
      auth: { user_id: 'dev-1', username: 'Greenhouse A' },
      last_edit_at: '2026-10-07T10:00:00.000Z',
    })
    ws.serverSend({
      type: 'message_element',
      element_id: EL,
      message: { value: 22 },
      auth: { user_id: 7, username: 'alice' },
      last_edit_at: '2026-10-07T10:00:01.000Z',
    })
    s = rt.getSnapshot(EL)
    expect(s.history).toHaveLength(2)
    expect(s.message).toEqual({ value: 22 })
    expect(s.lastEditBy).toEqual({ userId: 7, username: 'alice', source: 'user' })
    expect(s.history[0]!.by.source).toBe('device')
    expect(s.lastEditAt).toBe('2026-10-07T10:00:01.000Z')

    ws.serverSend({ type: 'element_connection_status', status: 'connected', element_id: EL })
    expect(rt.getSnapshot(EL).deviceConnected).toBe(true)
    ws.serverSend({ type: 'permissions_update', element_id: EL, permissions: 'RC' })
    expect(rt.getSnapshot(EL).permission).toBe('RC')
  })

  it('keeps snapshots referentially stable between changes and notifies listeners', () => {
    const rt = make()
    rt.retain(EL)
    const ws = FakeWebSocket.last
    ws.serverOpen()
    const fn = vi.fn()
    rt.listen(EL, fn)
    const before = rt.getSnapshot(EL)
    expect(rt.getSnapshot(EL)).toBe(before)
    confirm(ws)
    expect(fn).toHaveBeenCalledTimes(1)
    expect(rt.getSnapshot(EL)).not.toBe(before)
  })

  it('bounds the history buffer', () => {
    const rt = make({ historyLimit: 3 })
    rt.retain(EL)
    const ws = FakeWebSocket.last
    ws.serverOpen()
    confirm(ws)
    for (let i = 0; i < 5; i++) {
      ws.serverSend({
        type: 'message_element',
        element_id: EL,
        message: { value: i },
        auth: { user_id: 'd', username: 'd' },
        last_edit_at: `t${i}`,
      })
    }
    expect(rt.getSnapshot(EL).history.map((f) => (f.message as { value: number }).value)).toEqual([2, 3, 4])
  })

  it('only sends control messages with RC on a subscribed element', () => {
    const rt = make()
    rt.retain(EL)
    const ws = FakeWebSocket.last
    expect(rt.send(EL, { value: 1 })).toBe(false) // socket not open
    ws.serverOpen()
    confirm(ws, EL, 'R')
    expect(rt.send(EL, { value: 1 })).toBe(false)
    ws.serverSend({ type: 'permissions_update', element_id: EL, permissions: 'RC' })
    expect(rt.send(EL, { value: 1 })).toBe(true)
    expect(ws.sentOfType('message_element')).toEqual([
      { type: 'message_element', element_id: EL, message: { value: 1 } },
    ])
    expect(rt.send(EL2, { value: 1 })).toBe(false) // not subscribed
  })

  it('reconnects with exponential backoff and re-subscribes everything', () => {
    const rt = make()
    const statuses: string[] = []
    rt.onStatus(() => statuses.push(rt.getStatus()))
    rt.retain(EL)
    rt.retain(EL2)
    let ws = FakeWebSocket.last
    ws.serverOpen()
    confirm(ws, EL)
    ws.serverClose()
    expect(rt.getStatus()).toBe('reconnecting')
    expect(rt.getSnapshot(EL).status).toBe('subscribing')

    // attempt 0 → 1000ms * (0.5 + 0.5*0.5) = 750ms
    vi.advanceTimersByTime(749)
    expect(FakeWebSocket.instances).toHaveLength(1)
    vi.advanceTimersByTime(1)
    expect(FakeWebSocket.instances).toHaveLength(2)

    // fail again: attempt 1 → 2000ms → 1500ms
    FakeWebSocket.last.serverClose()
    vi.advanceTimersByTime(1499)
    expect(FakeWebSocket.instances).toHaveLength(2)
    vi.advanceTimersByTime(1)
    expect(FakeWebSocket.instances).toHaveLength(3)

    ws = FakeWebSocket.last
    ws.serverOpen()
    expect(rt.getStatus()).toBe('open')
    expect(
      ws
        .sentOfType('subscribe')
        .map((f) => f.element_id)
        .sort(),
    ).toEqual([EL, EL2].sort())
    expect(statuses).toEqual(['connecting', 'open', 'reconnecting', 'open'])
  })

  it('computes capped, jittered backoff delays', () => {
    const o = { baseMs: 1000, maxMs: 30_000 }
    expect(backoffDelay(0, o, () => 0)).toBe(500)
    expect(backoffDelay(0, o, () => 1)).toBe(1000)
    expect(backoffDelay(3, o, () => 1)).toBe(8000)
    expect(backoffDelay(20, o, () => 1)).toBe(30_000)
    expect(backoffDelay(20, o, () => 0)).toBe(15_000)
  })

  it('resets the backoff after a successful connection', () => {
    const rt = make()
    rt.retain(EL)
    FakeWebSocket.last.serverClose()
    vi.advanceTimersByTime(750)
    FakeWebSocket.last.serverClose()
    vi.advanceTimersByTime(1500)
    FakeWebSocket.last.serverOpen()
    FakeWebSocket.last.serverClose()
    vi.advanceTimersByTime(750)
    expect(FakeWebSocket.instances).toHaveLength(4)
  })

  it('marks a forced unsubscribe as revoked but ignores acks of our own unsubscribes', () => {
    const rt = make()
    const release = rt.retain(EL)
    const ws = FakeWebSocket.last
    ws.serverOpen()
    confirm(ws)
    release()
    rt.retain(EL) // re-subscribe before the ack arrives
    ws.serverSend({ type: 'unsubscribe', element_id: EL, unsubscribe: true }) // ack of the release
    expect(rt.getSnapshot(EL).status).toBe('subscribing')
    confirm(ws)
    expect(rt.getSnapshot(EL).status).toBe('subscribed')

    ws.serverSend({ type: 'unsubscribe', element_id: EL, unsubscribe: true, reason: 'Permission revoked' })
    const s = rt.getSnapshot(EL)
    expect(s.status).toBe('revoked')
    expect(s.reason).toBe('Permission revoked')
    expect(rt.send(EL, { value: 1 })).toBe(false)

    rt.resubscribe(EL)
    expect(rt.getSnapshot(EL).status).toBe('subscribing')
    expect(ws.sentOfType('subscribe')).toHaveLength(3)
  })

  it('turns a rejected subscribe into an error state and reports server errors', () => {
    const rt = make()
    const errors: unknown[] = []
    rt.onError((e) => errors.push(e))
    rt.retain(EL)
    const ws = FakeWebSocket.last
    ws.serverOpen()
    ws.serverSend({ type: 'error', error_code: 'permission_denied', description: 'nope', element_id: EL })
    expect(rt.getSnapshot(EL).status).toBe('error')
    expect(rt.getSnapshot(EL).error).toEqual({
      code: 'permission_denied',
      description: 'nope',
      elementId: EL,
    })
    expect(errors).toHaveLength(1)
  })

  it('keeps the subscription alive when a command is rejected', () => {
    const rt = make()
    rt.retain(EL)
    const ws = FakeWebSocket.last
    ws.serverOpen()
    confirm(ws)
    ws.serverSend({ type: 'error', error_code: 'unauthorized', description: 'no', element_id: EL })
    expect(rt.getSnapshot(EL).status).toBe('subscribed')
    expect(rt.getSnapshot(EL).error?.code).toBe('unauthorized')
  })

  it('ignores frames for elements nobody subscribed to and malformed frames', () => {
    const rt = make()
    rt.retain(EL)
    const ws = FakeWebSocket.last
    ws.serverOpen()
    ws.onmessage?.(new MessageEvent('message', { data: 'not json' }))
    ws.serverSend({
      type: 'message_element',
      element_id: EL2,
      message: { value: 1 },
      auth: {},
      last_edit_at: 'x',
    })
    expect(rt.getSnapshot(EL2).status).toBe('idle')
  })

  it('stops reconnecting after close() and supports retryNow()/markOffline()', () => {
    const rt = make()
    rt.retain(EL)
    FakeWebSocket.last.serverOpen()
    rt.markOffline()
    expect(rt.getStatus()).toBe('offline')
    vi.advanceTimersByTime(60_000)
    expect(FakeWebSocket.instances).toHaveLength(1)
    rt.retryNow()
    expect(FakeWebSocket.instances).toHaveLength(2)
    rt.close()
    expect(rt.getStatus()).toBe('closed')
    vi.advanceTimersByTime(60_000)
    expect(FakeWebSocket.instances).toHaveLength(2)
  })

  it('invokes frame listeners for replayed and live frames', () => {
    const rt = make()
    rt.retain(EL)
    const ws = FakeWebSocket.last
    ws.serverOpen()
    confirm(ws)
    const seen: unknown[] = []
    rt.onFrame(EL, (f) => seen.push(f.message))
    ws.serverSend({
      type: 'message_element',
      element_id: EL,
      message: { x: '2026-10-07T10:00:00Z', y: 3 },
      auth: { user_id: 'd', username: 'd' },
      last_edit_at: 't',
    })
    expect(seen).toEqual([{ x: '2026-10-07T10:00:00Z', y: 3 }])
  })
})
