import { useNow } from '@/lib/useNow'
import { formatDateTime, relativeTime } from '@/lib/time'

/** Self-updating "3s ago" text; the title shows the absolute time. */
export function RelativeTime({
  value,
  className,
}: {
  value: string | number | null | undefined
  className?: string
}) {
  const now = useNow()
  return (
    <time
      className={className}
      dateTime={typeof value === 'string' ? value : undefined}
      title={formatDateTime(value)}
    >
      {relativeTime(value, now)}
    </time>
  )
}
