import { THRESHOLD_COLORS } from '@/lib/chartTheme'
import { useTheme } from '@/lib/theme'
import { cn } from '@/lib/utils'

export function ColorSwatches({
  value,
  onChange,
  label,
}: {
  value: string
  onChange: (c: string) => void
  label: string
}) {
  const { resolved } = useTheme()
  return (
    <div role="radiogroup" aria-label={label} className="flex flex-wrap gap-1.5">
      {THRESHOLD_COLORS.map((c) => (
        <button
          key={c.name}
          type="button"
          role="radio"
          aria-checked={value === c.name}
          aria-label={c.name}
          title={c.name}
          onClick={() => onChange(c.name)}
          className={cn(
            'ring-offset-popover focus-visible:ring-ring size-6 rounded-full ring-offset-2 transition-shadow focus-visible:ring-2 focus-visible:outline-none',
            value === c.name && 'ring-foreground/70 ring-2',
          )}
          style={{ background: resolved === 'dark' ? c.dark : c.light }}
        />
      ))}
    </div>
  )
}
