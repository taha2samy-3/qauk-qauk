import {
  ArrowDown,
  ArrowUp,
  ChevronLeft,
  ChevronRight,
  ChevronsUpDown,
  Search,
  type LucideIcon,
} from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { cn } from '@/lib/utils'
import { EmptyState, ErrorState, TableSkeleton } from './states'

export interface Column<T> {
  id: string
  header: ReactNode
  cell: (row: T) => ReactNode
  /** enables sorting on this column */
  sortValue?: (row: T) => string | number | boolean | null | undefined
  /** included in the search text */
  searchValue?: (row: T) => string | null | undefined
  className?: string
  headClassName?: string
}

type Sort = { id: string; dir: 'asc' | 'desc' }

export function DataTable<T>({
  data,
  columns,
  getRowId,
  isLoading,
  error,
  onRetry,
  searchPlaceholder = 'Search…',
  toolbar,
  empty,
  pageSize = 15,
  initialSort,
  rowClassName,
  label,
}: {
  data: T[] | undefined
  columns: Column<T>[]
  getRowId: (row: T) => string | number
  isLoading?: boolean
  error?: unknown
  onRetry?: () => void
  searchPlaceholder?: string
  toolbar?: ReactNode
  empty?: { icon?: LucideIcon; title: string; description?: string; action?: ReactNode }
  pageSize?: number
  initialSort?: Sort
  rowClassName?: (row: T) => string | undefined
  label: string
}) {
  const [q, setQ] = useState('')
  const [sort, setSort] = useState<Sort | undefined>(initialSort)
  const [page, setPage] = useState(0)

  const filtered = useMemo(() => {
    const rows = data ?? []
    const needle = q.trim().toLowerCase()
    const searchable = columns.filter((c) => c.searchValue)
    let out = needle
      ? rows.filter((r) => searchable.some((c) => (c.searchValue!(r) ?? '').toLowerCase().includes(needle)))
      : rows
    const col = sort && columns.find((c) => c.id === sort.id)
    if (col?.sortValue) {
      const dir = sort!.dir === 'asc' ? 1 : -1
      out = [...out].sort((a, b) => {
        const va = col.sortValue!(a)
        const vb = col.sortValue!(b)
        if (va === vb) return 0
        if (va === null || va === undefined) return 1
        if (vb === null || vb === undefined) return -1
        if (typeof va === 'string' && typeof vb === 'string') return va.localeCompare(vb) * dir
        return (va < vb ? -1 : 1) * dir
      })
    }
    return out
  }, [data, q, sort, columns])

  const pages = Math.max(1, Math.ceil(filtered.length / pageSize))
  const current = Math.min(page, pages - 1)
  const rows = filtered.slice(current * pageSize, current * pageSize + pageSize)

  const toggleSort = (id: string) => {
    setSort((s) => (s?.id !== id ? { id, dir: 'asc' } : s.dir === 'asc' ? { id, dir: 'desc' } : undefined))
    setPage(0)
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div className="relative w-full sm:max-w-xs">
          <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
          <Input
            value={q}
            onChange={(e) => {
              setQ(e.target.value)
              setPage(0)
            }}
            placeholder={searchPlaceholder}
            aria-label={`Search ${label}`}
            className="pl-8"
          />
        </div>
        {toolbar && <div className="flex flex-wrap items-center gap-2">{toolbar}</div>}
      </div>

      <div className="bg-card overflow-hidden rounded-xl border shadow-xs">
        {error ? (
          <ErrorState error={error} onRetry={onRetry} className="m-4 border-0" />
        ) : isLoading && !data ? (
          <TableSkeleton cols={Math.min(columns.length, 5)} />
        ) : filtered.length === 0 ? (
          q ? (
            <EmptyState
              duck="detective"
              title="No matches"
              description={`Nothing matches “${q}”.`}
              className="m-4 border-0"
            />
          ) : (
            <EmptyState {...(empty ?? { title: 'Nothing here yet' })} className="m-4 border-0" />
          )
        ) : (
          <Table aria-label={label}>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                {columns.map((c) => {
                  const active = sort?.id === c.id
                  return (
                    <TableHead
                      key={c.id}
                      className={c.headClassName}
                      aria-sort={active ? (sort!.dir === 'asc' ? 'ascending' : 'descending') : undefined}
                    >
                      {c.sortValue ? (
                        <button
                          type="button"
                          onClick={() => toggleSort(c.id)}
                          className="hover:bg-accent hover:text-foreground focus-visible:ring-ring/50 -mx-1.5 inline-flex items-center gap-1 rounded px-1.5 py-1 focus-visible:ring-[3px] focus-visible:outline-none"
                        >
                          {c.header}
                          {active ? (
                            sort!.dir === 'asc' ? (
                              <ArrowUp className="size-3" />
                            ) : (
                              <ArrowDown className="size-3" />
                            )
                          ) : (
                            <ChevronsUpDown className="size-3 opacity-40" />
                          )}
                        </button>
                      ) : (
                        c.header
                      )}
                    </TableHead>
                  )
                })}
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((r) => (
                <TableRow key={getRowId(r)} className={rowClassName?.(r)}>
                  {columns.map((c) => (
                    <TableCell key={c.id} className={c.className}>
                      {c.cell(r)}
                    </TableCell>
                  ))}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </div>

      {filtered.length > 0 && (
        <div className="text-muted-foreground flex items-center justify-between text-xs">
          <span>
            {filtered.length === (data?.length ?? 0)
              ? `${filtered.length} ${filtered.length === 1 ? 'row' : 'rows'}`
              : `${filtered.length} of ${data?.length ?? 0} rows`}
          </span>
          {pages > 1 && (
            <div className="flex items-center gap-2">
              <span>
                Page {current + 1} of {pages}
              </span>
              <Button
                variant="outline"
                size="icon-xs"
                onClick={() => setPage(current - 1)}
                disabled={current === 0}
                aria-label="Previous page"
              >
                <ChevronLeft />
              </Button>
              <Button
                variant="outline"
                size="icon-xs"
                onClick={() => setPage(current + 1)}
                disabled={current >= pages - 1}
                aria-label="Next page"
              >
                <ChevronRight />
              </Button>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

export function Mono({ children, className }: { children: ReactNode; className?: string }) {
  return <span className={cn('text-muted-foreground font-mono text-xs', className)}>{children}</span>
}
