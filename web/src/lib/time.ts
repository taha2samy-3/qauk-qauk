const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto', style: 'short' })
const dtf = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'medium' })

export function parseTime(t: string | number | Date | null | undefined): number | undefined {
  if (t === null || t === undefined) return undefined
  if (typeof t === 'number') return t
  if (t instanceof Date) return t.getTime()
  const ms = Date.parse(t)
  return Number.isNaN(ms) ? undefined : ms
}

export function relativeTime(t: string | number | Date | null | undefined, now = Date.now()): string {
  const ms = parseTime(t)
  if (ms === undefined) return 'never'
  const diff = (ms - now) / 1000
  const abs = Math.abs(diff)
  if (abs < 5) return 'just now'
  if (abs < 60) return rtf.format(Math.round(diff), 'second')
  if (abs < 3600) return rtf.format(Math.round(diff / 60), 'minute')
  if (abs < 86400) return rtf.format(Math.round(diff / 3600), 'hour')
  return rtf.format(Math.round(diff / 86400), 'day')
}

export function formatDateTime(t: string | number | Date | null | undefined): string {
  const ms = parseTime(t)
  return ms === undefined ? '—' : dtf.format(ms)
}
