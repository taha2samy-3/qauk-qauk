import { act, renderHook } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { toast } from 'sonner'
import type { UseElementResult } from '@/realtime/hooks'
import { usePendingCommand } from './usePendingCommand'

vi.mock('sonner', () => ({
  toast: {
    error: vi.fn(),
    warning: vi.fn(),
  },
}))

vi.mock('@/realtime/hooks', () => ({
  useElementFrames: vi.fn(),
}))

function mockRt(overrides: Partial<UseElementResult> = {}): UseElementResult {
  return {
    value: undefined,
    message: undefined,
    history: [],
    permission: 'RC',
    deviceConnected: true,
    lastEditBy: null,
    lastEditAt: null,
    status: 'subscribed',
    error: null,
    reason: null,
    details: null,
    send: vi.fn().mockReturnValue(true),
    retry: vi.fn(),
    ...overrides,
  }
}

describe('usePendingCommand', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.useFakeTimers()
  })

  it('sets pending state optimistically on command send', () => {
    const rt = mockRt()
    const { result } = renderHook(() => usePendingCommand('el-1', rt, 'Switch'))

    expect(result.current.pending).toBeUndefined()

    act(() => {
      const ok = result.current.command(true)
      expect(ok).toBe(true)
    })

    expect(result.current.pending).toBe(true)
    expect(rt.send).toHaveBeenCalledWith({ value: true })
  })

  it('reverts optimistic pending state and toasts error when delivery_failed error frame arrives', () => {
    let currentRt = mockRt()
    const { result, rerender } = renderHook(({ rt }) => usePendingCommand('el-1', rt, 'Compressor Switch'), {
      initialProps: { rt: currentRt },
    })

    act(() => {
      result.current.command('ON')
    })
    expect(result.current.pending).toBe('ON')

    // Simulate backend delivery_failed error frame arriving on the element
    currentRt = mockRt({
      error: {
        code: 'delivery_failed',
        description: 'MQTT broker unreachable after retries',
      },
    })
    rerender({ rt: currentRt })

    // Optimistic pending state must revert immediately to undefined
    expect(result.current.pending).toBeUndefined()

    // Must toast error to notify the operator
    expect(toast.error).toHaveBeenCalledWith('Compressor Switch: MQTT broker unreachable after retries')
  })

  it('reverts optimistic state when connection or subscription is lost', () => {
    let currentRt = mockRt()
    const { result, rerender } = renderHook(({ rt }) => usePendingCommand('el-1', rt, 'Switch'), {
      initialProps: { rt: currentRt },
    })

    act(() => {
      result.current.command(1)
    })
    expect(result.current.pending).toBe(1)

    // Socket disconnects or status transitions to revoked/subscribing
    currentRt = mockRt({ status: 'subscribing' })
    rerender({ rt: currentRt })

    expect(result.current.pending).toBeUndefined()
  })
})
