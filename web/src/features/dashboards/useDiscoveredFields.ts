import { useMemo } from 'react'
import { discoverFields, type DiscoveredField } from '@/lib/fieldPath'
import { useElement } from '@/realtime/hooks'

/** Attributes seen in the element's recent messages (newest sample wins). */
export function useDiscoveredFields(elementId: string | undefined): DiscoveredField[] {
  const rt = useElement(elementId)
  return useMemo(
    () =>
      discoverFields(
        [...rt.history.slice(-20).map((f) => f.message), rt.message].filter((m) => m !== undefined),
      ),
    [rt.history, rt.message],
  )
}
