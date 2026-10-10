import { useCallback, useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'
import type { UseElementResult } from '@/realtime/hooks'
import { useElementFrames } from '@/realtime/hooks'
import { readRaw } from '@/realtime/messages'
import { buildMessage } from '@/lib/fieldPath'
import { normalize } from '@/lib/valueMap'

const CONFIRM_TIMEOUT_MS = 8000

/**
 * Optimistic command helper: after `command(v)` the widget shows `v` as
 * pending until the device echoes a frame with that value, or the timeout
 * expires (then it reverts and warns).
 *
 * With `field` the command is sent in the device's own shape
 * ({"relay": "OFF"}) and the echo is read from the same attribute.
 */
export function usePendingCommand(elementId: string, rt: UseElementResult, label: string, field?: string) {
  const [pending, setPending] = useState<unknown>(undefined)
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
    if (normalize(readRaw(f.message, field)) === normalize(pending)) clear()
  })

  // A rejected command (error frame) or losing the subscription ends the wait.
  useEffect(() => {
    if (pending === undefined) return
    const isCmdError =
      rt.error !== errorAtSend.current &&
      (rt.error?.code === 'delivery_failed' ||
        rt.error?.code === 'unauthorized' ||
        rt.error?.code === 'permission_denied' ||
        rt.error?.code === 'rate_limited')
    if (isCmdError || rt.status !== 'subscribed') {
      clear()
      if (isCmdError && rt.error) {
        toast.error(`${label}: ${rt.error.description || 'Command delivery failed'}`)
      }
    }
  }, [rt.error, rt.status, pending, clear, label])

  const command = useCallback(
    (value: unknown) => {
      if (!rt.send(buildMessage(field ?? 'value', value))) {
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
    [rt, label, field],
  )

  return { pending, command }
}
