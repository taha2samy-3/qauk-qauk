import { useState } from 'react'
import { Textarea } from '@/components/ui/textarea'
import { cn } from '@/lib/utils'

export function parseJson(text: string): { ok: true; value: unknown } | { ok: false; error: string } {
  if (text.trim() === '') return { ok: true, value: {} }
  try {
    return { ok: true, value: JSON.parse(text) }
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : 'Invalid JSON' }
  }
}

/** JSON textarea with live validation and a "format" action. Value is the raw text. */
export function JsonEditor({
  id,
  value,
  onChange,
  invalid,
  rows = 8,
  'aria-describedby': describedBy,
}: {
  id?: string
  value: string
  onChange: (text: string) => void
  invalid?: boolean
  rows?: number
  'aria-describedby'?: string
}) {
  const [focused, setFocused] = useState(false)
  const parsed = parseJson(value)
  return (
    <div className="space-y-1.5">
      <Textarea
        id={id}
        value={value}
        rows={rows}
        spellCheck={false}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
        onChange={(e) => onChange(e.target.value)}
        aria-invalid={invalid || !parsed.ok}
        aria-describedby={describedBy}
        className={cn('field-sizing-fixed font-mono text-xs leading-relaxed')}
      />
      <div className="flex items-center justify-between text-xs">
        <span className={parsed.ok ? 'text-muted-foreground' : 'text-destructive'}>
          {parsed.ok ? (focused ? 'Valid JSON' : '') : parsed.error}
        </span>
        <button
          type="button"
          className="text-muted-foreground hover:text-foreground underline-offset-2 hover:underline disabled:opacity-50"
          disabled={!parsed.ok}
          onClick={() => parsed.ok && onChange(JSON.stringify(parsed.value, null, 2))}
        >
          Format
        </button>
      </div>
    </div>
  )
}
