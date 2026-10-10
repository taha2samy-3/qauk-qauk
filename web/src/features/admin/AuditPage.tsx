import { ChevronDown, FileClock } from 'lucide-react'
import { useState } from 'react'
import type { AuditEntry } from '@/api/types'
import { DataTable, Mono, type Column } from '@/components/DataTable'
import { Page } from '@/components/layout/AppShell'
import { RelativeTime } from '@/components/RelativeTime'
import { PageHeader } from '@/components/states'
import { Badge } from '@/components/ui/badge'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { formatDateTime } from '@/lib/time'
import { useAudit } from './api'

const ACTION_VARIANT: Record<string, 'success' | 'destructive' | 'secondary'> = {
  create: 'success',
  delete: 'destructive',
  update: 'secondary',
}

export function AuditPage() {
  const [limit, setLimit] = useState('200')
  const audit = useAudit(Number(limit))

  const columns: Column<AuditEntry>[] = [
    {
      id: 'at',
      header: 'When',
      sortValue: (a) => a.at,
      cell: (a) => (
        <span title={formatDateTime(a.at)} className="whitespace-nowrap">
          <RelativeTime value={a.at} />
        </span>
      ),
    },
    {
      id: 'actor',
      header: 'Actor',
      sortValue: (a) => a.actor_name,
      searchValue: (a) => a.actor_name,
      cell: (a) => <span className="font-medium">{a.actor_name}</span>,
    },
    {
      id: 'action',
      header: 'Action',
      sortValue: (a) => a.action,
      searchValue: (a) => a.action,
      cell: (a) => <Badge variant={ACTION_VARIANT[a.action] ?? 'outline'}>{a.action}</Badge>,
    },
    {
      id: 'entity',
      header: 'Entity',
      sortValue: (a) => a.entity,
      searchValue: (a) => `${a.entity} ${a.entity_id}`,
      cell: (a) => (
        <span>
          {a.entity.replace(/_/g, ' ')}{' '}
          <Mono>{a.entity_id.length > 12 ? a.entity_id.slice(0, 8) : a.entity_id}</Mono>
        </span>
      ),
    },
    {
      id: 'data',
      header: 'Data',
      searchValue: (a) => JSON.stringify(a.data ?? ''),
      cell: (a) =>
        a.data === null || a.data === undefined ? (
          <span className="text-muted-foreground">—</span>
        ) : (
          <Popover>
            <PopoverTrigger className="text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:ring-ring/50 inline-flex max-w-[22rem] items-center gap-1 truncate rounded px-1 font-mono text-xs focus-visible:ring-[3px] focus-visible:outline-none">
              <span className="truncate">{JSON.stringify(a.data)}</span>
              <ChevronDown className="size-3 shrink-0" />
            </PopoverTrigger>
            <PopoverContent align="start" className="w-[28rem] max-w-[90vw]">
              <pre className="max-h-80 overflow-auto font-mono text-[11px] leading-relaxed">
                {JSON.stringify(a.data, null, 2)}
              </pre>
            </PopoverContent>
          </Popover>
        ),
    },
  ]

  return (
    <Page wide>
      <PageHeader
        title={
          <div className="flex items-center gap-3">
            <img src="/brand/logo-audit.svg" alt="" className="size-7 rounded-lg shadow-xs" />
            <span>Audit log</span>
          </div>
        }
        description="Administrative changes, newest first."
      />
      <DataTable
        label="Audit log"
        data={audit.data}
        columns={columns}
        getRowId={(a) => a.id}
        isLoading={audit.isLoading}
        error={audit.error}
        onRetry={() => void audit.refetch()}
        searchPlaceholder="Search actor, entity, data…"
        initialSort={{ id: 'at', dir: 'desc' }}
        pageSize={25}
        empty={{ icon: FileClock, title: 'No audit entries' }}
        toolbar={
          <Select value={limit} onValueChange={setLimit}>
            <SelectTrigger className="w-32" aria-label="Number of entries to load">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {['50', '200', '1000'].map((n) => (
                <SelectItem key={n} value={n}>
                  Last {n}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        }
      />
    </Page>
  )
}
