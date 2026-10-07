/**
 * Data-binding inputs of the widget configuration sheet: pick an attribute
 * of the element's messages, map values to names/colors, and add chart series
 * from other elements. Attributes are discovered from recent live messages.
 */
import { Plus, Trash2 } from 'lucide-react'
import { Controller, useFieldArray, type Control } from 'react-hook-form'
import type { MyElement } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import type { DiscoveredField, FieldKind } from '@/lib/fieldPath'
import { cn } from '@/lib/utils'
import { X_RECEIVED } from '@/realtime/messages'
import { ColorSwatches } from './ColorSwatches'
import { useDiscoveredFields } from './useDiscoveredFields'

function sampleText(v: unknown): string {
  const s = typeof v === 'string' ? `"${v}"` : String(v)
  return s.length > 18 ? `${s.slice(0, 17)}…` : s
}

const ACCEPTS: Record<FieldKind, FieldKind[]> = {
  number: ['number', 'boolean'],
  boolean: ['boolean', 'number', 'string'],
  string: ['string', 'number', 'boolean'],
  time: ['time'],
}

export function AttributeInput({
  id,
  value,
  onChange,
  discovered,
  accepts,
  placeholder,
  xAxis,
  invalid,
}: {
  id: string
  value: string
  onChange: (v: string) => void
  discovered: DiscoveredField[]
  accepts?: FieldKind[]
  placeholder?: string
  xAxis?: boolean
  invalid?: boolean
}) {
  const allowed = new Set((accepts ?? []).flatMap((k) => ACCEPTS[k]))
  const options = discovered.filter((f) => allowed.size === 0 || allowed.has(f.kind))
  return (
    <div className="space-y-2">
      <Input
        id={id}
        value={value === X_RECEIVED ? '' : value}
        placeholder={value === X_RECEIVED ? 'server receive time' : placeholder}
        onChange={(e) => onChange(e.target.value.trim())}
        aria-invalid={invalid}
        className="font-mono text-xs"
        autoComplete="off"
        spellCheck={false}
      />
      <div className="flex flex-wrap gap-1.5" role="list" aria-label="Discovered attributes">
        {xAxis && (
          <Chip
            active={value === X_RECEIVED}
            onClick={() => onChange(X_RECEIVED)}
            label="Server receive time"
          />
        )}
        {options.map((f) => (
          <Chip
            key={f.path}
            active={value === f.path}
            onClick={() => onChange(f.path)}
            label={f.path}
            sample={sampleText(f.sample)}
          />
        ))}
        {!xAxis && options.length === 0 && (
          <span className="text-muted-foreground text-xs">
            No attributes seen yet. They appear here once the device sends data; you can also type a path.
          </span>
        )}
      </div>
    </div>
  )
}

function Chip({
  label,
  sample,
  active,
  onClick,
}: {
  label: string
  sample?: string
  active: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      role="listitem"
      onClick={onClick}
      data-testid={`attr-chip-${label}`}
      aria-pressed={active}
      className={cn(
        'hover:bg-accent focus-visible:ring-ring inline-flex max-w-full items-center gap-1.5 rounded-md border px-2 py-1 font-mono text-[11px] transition-colors focus-visible:ring-2 focus-visible:outline-none',
        active && 'border-primary bg-primary/10 text-primary',
      )}
    >
      <span className="truncate">{label}</span>
      {sample && <span className="text-muted-foreground truncate">{sample}</span>}
    </button>
  )
}

/** value → label/color rows */
export function MappingsEditor({
  control,
  name,
  label,
  help,
}: {
  control: Control<never>
  name: string
  label: string
  help?: string
}) {
  const { fields, append, remove } = useFieldArray({ control, name: name as never })
  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <Label>{label}</Label>
        <Button
          variant="ghost"
          size="xs"
          onClick={() => append({ match: '', label: '', color: 'green' } as never)}
        >
          <Plus /> Add
        </Button>
      </div>
      {help && <p className="text-muted-foreground text-xs">{help}</p>}
      {fields.length === 0 && (
        <p className="text-muted-foreground rounded-md border border-dashed px-3 py-3 text-center text-xs">
          No mappings
        </p>
      )}
      {fields.map((row, i) => (
        <div key={row.id} className="space-y-2 rounded-md border p-2">
          <div className="flex items-center gap-2">
            <Controller
              control={control}
              name={`${name}.${i}.match` as never}
              render={({ field: f }) => (
                <Input
                  aria-label={`Mapping ${i + 1} value`}
                  placeholder="value, e.g. 1 or ON"
                  className="w-32 font-mono text-xs"
                  value={(f.value as string) ?? ''}
                  onChange={(e) => f.onChange(e.target.value)}
                />
              )}
            />
            <span className="text-muted-foreground">→</span>
            <Controller
              control={control}
              name={`${name}.${i}.label` as never}
              render={({ field: f }) => (
                <Input
                  aria-label={`Mapping ${i + 1} label`}
                  placeholder="show as, e.g. Running"
                  className="flex-1"
                  value={(f.value as string) ?? ''}
                  onChange={(e) => f.onChange(e.target.value)}
                />
              )}
            />
            <Button
              variant="ghost"
              size="icon-xs"
              onClick={() => remove(i)}
              aria-label={`Remove mapping ${i + 1}`}
            >
              <Trash2 />
            </Button>
          </div>
          <Controller
            control={control}
            name={`${name}.${i}.color` as never}
            render={({ field: f }) => (
              <ColorSwatches
                value={(f.value as string) ?? ''}
                onChange={f.onChange}
                label={`Mapping ${i + 1} color`}
              />
            )}
          />
        </div>
      ))}
    </div>
  )
}

/** Extra chart series, each from any readable element. */
export function SeriesEditor({
  control,
  name,
  label,
  help,
  elements,
}: {
  control: Control<never>
  name: string
  label: string
  help?: string
  elements: Map<string, MyElement>
}) {
  const { fields, append, remove } = useFieldArray({ control, name: name as never })
  const palette = ['orange', 'green', 'purple', 'red', 'yellow', 'blue']
  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <Label>{label}</Label>
        <Button
          variant="ghost"
          size="xs"
          data-testid="add-series"
          onClick={() =>
            append({
              element_id: '',
              field: '',
              label: '',
              color: palette[fields.length % palette.length],
            } as never)
          }
        >
          <Plus /> Add series
        </Button>
      </div>
      {help && <p className="text-muted-foreground text-xs">{help}</p>}
      {fields.map((row, i) => (
        <SeriesRow
          key={row.id}
          index={i}
          control={control}
          name={name}
          elements={elements}
          onRemove={() => remove(i)}
        />
      ))}
    </div>
  )
}

function SeriesRow({
  index,
  control,
  name,
  elements,
  onRemove,
}: {
  index: number
  control: Control<never>
  name: string
  elements: Map<string, MyElement>
  onRemove: () => void
}) {
  const base = `${name}.${index}`
  const sorted = [...elements.values()].sort((a, b) => a.name.localeCompare(b.name))
  return (
    <div className="space-y-3 rounded-md border p-3" data-testid={`series-row-${index}`}>
      <div className="flex items-center justify-between">
        <span className="text-xs font-semibold">Series {index + 2}</span>
        <Button variant="ghost" size="icon-xs" onClick={onRemove} aria-label={`Remove series ${index + 2}`}>
          <Trash2 />
        </Button>
      </div>
      <Controller
        control={control}
        name={`${base}.element_id` as never}
        render={({ field: el, fieldState }) => (
          <>
            <Field label="Element" error={fieldState.error?.message}>
              <Select value={(el.value as string) || undefined} onValueChange={el.onChange}>
                <SelectTrigger aria-label={`Series ${index + 2} element`}>
                  <SelectValue placeholder="Choose an element…" />
                </SelectTrigger>
                <SelectContent>
                  {sorted.map((e) => (
                    <SelectItem key={e.id} value={e.id}>
                      {e.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <SeriesAttribute
              control={control}
              base={base}
              elementId={(el.value as string) || undefined}
              index={index}
            />
          </>
        )}
      />
      <div className="grid grid-cols-2 gap-3">
        <Controller
          control={control}
          name={`${base}.label` as never}
          render={({ field: f }) => (
            <Field label="Name in legend">
              <Input
                aria-label={`Series ${index + 2} name`}
                placeholder="element name"
                value={(f.value as string) ?? ''}
                onChange={(e) => f.onChange(e.target.value)}
              />
            </Field>
          )}
        />
        <Controller
          control={control}
          name={`${base}.color` as never}
          render={({ field: f }) => (
            <Field label="Color">
              <ColorSwatches
                value={(f.value as string) ?? 'orange'}
                onChange={f.onChange}
                label={`Series ${index + 2} color`}
              />
            </Field>
          )}
        />
      </div>
    </div>
  )
}

function SeriesAttribute({
  control,
  base,
  elementId,
  index,
}: {
  control: Control<never>
  base: string
  elementId?: string
  index: number
}) {
  const discovered = useDiscoveredFields(elementId)
  return (
    <Controller
      control={control}
      name={`${base}.field` as never}
      render={({ field: f, fieldState }) => (
        <Field label="Attribute (Y)" error={fieldState.error?.message}>
          <AttributeInput
            id={`series-${index}-field`}
            value={(f.value as string) ?? ''}
            onChange={f.onChange}
            discovered={discovered}
            accepts={['number']}
            placeholder="auto (value)"
            invalid={!!fieldState.error}
          />
        </Field>
      )}
    />
  )
}
