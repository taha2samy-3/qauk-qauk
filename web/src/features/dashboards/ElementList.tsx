import { Lock, Search } from 'lucide-react'
import { useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { useDevices } from '@/api/devices'
import { widgetHint, type MyElement } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'
import type { Fit } from '@/widgets/types'

/**
 * Searchable, keyboard-navigable list of the user's elements grouped by device.
 * `fit` optionally ranks/filters elements for a widget type.
 */
export function ElementList({
  value,
  onSelect,
  fit,
  autoFocus,
  className,
  emptyHint,
}: {
  value?: string | null
  onSelect: (el: MyElement) => void
  fit?: (el: MyElement) => Fit
  autoFocus?: boolean
  className?: string
  emptyHint?: ReactNode
}) {
  const { devices, isLoading } = useDevices()
  const [q, setQ] = useState('')
  const [active, setActive] = useState(0)
  const listId = useId()
  const listRef = useRef<HTMLDivElement>(null)

  const groups = useMemo(() => {
    const needle = q.trim().toLowerCase()
    return devices
      .map((d) => ({
        device: d,
        elements: d.elements
          .filter((e) => (fit ? fit(e) !== 'no' : true))
          .filter(
            (e) =>
              !needle ||
              e.name.toLowerCase().includes(needle) ||
              d.name.toLowerCase().includes(needle) ||
              e.description.toLowerCase().includes(needle),
          )
          .sort((a, b) => {
            const fa = fit?.(a) === 'suggested' ? 0 : 1
            const fb = fit?.(b) === 'suggested' ? 0 : 1
            return fa - fb || a.name.localeCompare(b.name)
          }),
      }))
      .filter((g) => g.elements.length > 0)
  }, [devices, q, fit])

  const flat = groups.flatMap((g) => g.elements)
  const activeIdx = Math.min(active, Math.max(0, flat.length - 1))

  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      const next = Math.max(0, Math.min(flat.length - 1, activeIdx + (e.key === 'ArrowDown' ? 1 : -1)))
      setActive(next)
      listRef.current?.querySelector(`[data-index="${next}"]`)?.scrollIntoView({ block: 'nearest' })
    } else if (e.key === 'Enter') {
      e.preventDefault()
      const el = flat[activeIdx]
      if (el) onSelect(el)
    }
  }

  let index = -1
  return (
    <div className={cn('flex min-h-0 flex-col gap-2', className)}>
      <div className="relative">
        <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
        <Input
          role="combobox"
          aria-expanded
          aria-controls={listId}
          aria-activedescendant={flat[activeIdx] ? `${listId}-${flat[activeIdx]!.id}` : undefined}
          aria-label="Search elements"
          placeholder="Search elements or devices…"
          className="pl-8"
          value={q}
          autoFocus={autoFocus}
          onChange={(e) => {
            setQ(e.target.value)
            setActive(0)
          }}
          onKeyDown={onKeyDown}
        />
      </div>
      <div
        ref={listRef}
        id={listId}
        role="listbox"
        aria-label="Elements"
        className="min-h-0 flex-1 overflow-y-auto rounded-lg border"
      >
        {isLoading ? (
          <div className="space-y-2 p-3">
            {Array.from({ length: 4 }, (_, i) => (
              <Skeleton key={i} className="h-9" />
            ))}
          </div>
        ) : groups.length === 0 ? (
          <div className="text-muted-foreground px-4 py-10 text-center text-sm">
            {q ? `No elements match “${q}”.` : (emptyHint ?? 'You do not have access to any elements yet.')}
          </div>
        ) : (
          groups.map((g) => (
            <div key={g.device.id} role="group" aria-label={g.device.name}>
              <div className="bg-popover/95 text-muted-foreground sticky top-0 z-10 border-b px-3 py-1.5 text-[11px] font-semibold tracking-wider uppercase backdrop-blur">
                {g.device.name}
              </div>
              {g.elements.map((el) => {
                index++
                const i = index
                const hint = widgetHint(el)
                const selected = value === el.id
                return (
                  <div
                    key={el.id}
                    id={`${listId}-${el.id}`}
                    role="option"
                    aria-selected={selected}
                    data-index={i}
                    data-active={i === activeIdx}
                    onMouseMove={() => setActive(i)}
                    onClick={() => onSelect(el)}
                    className={cn(
                      'flex cursor-pointer items-center gap-3 px-3 py-2 text-sm',
                      i === activeIdx && 'bg-accent',
                      selected && 'font-medium',
                    )}
                  >
                    <div className="min-w-0 flex-1">
                      <div className="truncate">{el.name}</div>
                      {el.description && (
                        <div className="text-muted-foreground truncate text-xs">{el.description}</div>
                      )}
                    </div>
                    {fit?.(el) === 'suggested' && <Badge variant="success">Suggested</Badge>}
                    {hint && <Badge variant="muted">{hint}</Badge>}
                    {el.permission === 'R' && (
                      <Lock className="text-muted-foreground size-3.5" aria-label="Read-only" />
                    )}
                  </div>
                )
              })}
            </div>
          ))
        )}
      </div>
    </div>
  )
}
