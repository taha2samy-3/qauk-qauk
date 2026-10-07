import { useCallback, useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'
import type { UseElementResult } from '@/realtime/hooks'
import { useElementFrames } from '@/realtime/hooks'
import { messageValue } from '@/realtime/messages'

const CONFIRM_TIMEOUT_MS = 8000

/**
 * Optimistic command helper: after `command(v)` the widget shows `v` as
 * pending until the device echoes a frame with that value, or the timeout
 * expires (then it reverts and warns).
 */
export function usePendingCommand(elementId: string, rt: UseElementResult, label: string) {
  const [pending, setPending] = useState<number | undefined>(undefined)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const errorAtSend = useRef(rt.error)

  const clear = useCallback(() => {
    if (timer.current) clearTimeout(timer.current)
    timer.current = undefined
    setPending(undefined)
  }, [])

  useEffect(() => () => clearTimeout(timer.current), [])

  useElementFrames(elementId, (f) => {
    if (pending === undefined || f.by.source !== 'device') return
    const v = messageValue(f.message)
    if (v !== undefined && Math.abs(v - pending) < 1e-9) clear()
  })

  // A rejected command (error frame) or losing the subscription ends the wait.
  useEffect(() => {
    if (pending === undefined) return
    const rejected = rt.error !== errorAtSend.current && rt.error?.code === 'unauthorized'
    if (rejected || rt.status !== 'subscribed') {
      clear()
      if (rejected && rt.error) toast.error(`${label}: ${rt.error.description}`)
    }
  }, [rt.error, rt.status, pending, clear, label])

  const command = useCallback(
    (value: number) => {
      if (!rt.send({ value })) {
        toast.error(`Could not send to ${label}`, {
          description: 'You may lack control permission or the connection is down.',
        })
        return false
      }
      if (timer.current) clearTimeout(timer.current)
      errorAtSend.current = rt.error
      setPending(value)
      timer.current = setTimeout(() => {
        setPending(undefined)
        toast.warning(`${label} did not confirm the change`, {
          description: 'The device did not echo the new state in time.',
        })
      }, CONFIRM_TIMEOUT_MS)
      return true
    },
    [rt, label],
  )

  return { pending, command }
}
