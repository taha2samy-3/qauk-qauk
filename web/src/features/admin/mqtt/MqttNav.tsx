import { NavLink } from 'react-router'
import { cn } from '@/lib/utils'

const LINKS = [
  { to: '/admin/mqtt', label: 'Connections', end: true },
  { to: '/admin/mqtt/decoders', label: 'Decoders', end: false },
  { to: '/admin/mqtt/rejected', label: 'Rejected messages', end: false },
]

/** Sub-navigation of the MQTT admin pages. */
export function MqttNav() {
  return (
    <nav className="bg-muted mb-5 inline-flex rounded-lg p-[3px]" aria-label="MQTT">
      {LINKS.map((l) => (
        <NavLink
          key={l.to}
          to={l.to}
          end={l.end}
          className={({ isActive }) =>
            cn(
              'rounded-md px-3 py-1.5 text-sm font-medium',
              isActive ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground',
            )
          }
        >
          {l.label}
        </NavLink>
      ))}
    </nav>
  )
}

export function StatusDot({ ok, label }: { ok: boolean; label: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 text-sm">
      <span className={cn('size-2 rounded-full', ok ? 'bg-success' : 'bg-destructive')} aria-hidden />
      {label}
    </span>
  )
}
