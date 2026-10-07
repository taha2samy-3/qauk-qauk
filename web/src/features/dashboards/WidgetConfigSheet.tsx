import { zodResolver } from '@hookform/resolvers/zod'
import { ChevronsUpDown, Plus, Trash2 } from 'lucide-react'
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { Controller, useFieldArray, useForm, useWatch, type Control } from 'react-hook-form'
import { z } from 'zod'
import { useDevices } from '@/api/devices'
import type { MyElement } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { cn } from '@/lib/utils'
import { getWidget, widgetsFor, WIDGETS } from '@/widgets/registry'
import type { OptionField, Options, WidgetDefinition } from '@/widgets/types'
import { ElementList } from './ElementList'
import { ColorSwatches } from './ColorSwatches'
import { AttributeInput, MappingsEditor, SeriesEditor, useDiscoveredFields } from './DataBindingInputs'
import { isValidPath, type DiscoveredField } from '@/lib/fieldPath'
import { X_RECEIVED } from '@/realtime/messages'
import type { Widget } from './layout'

const thresholdSchema = z.object({ value: z.number({ error: 'Enter a number' }), color: z.string().min(1) })

const attributeSchema = z
  .string()
  .max(200)
  .refine(
    (v) => v === '' || v === X_RECEIVED || isValidPath(v),
    'Use a path like temperature, gps.lat or sensors[0].temp',
  )

const mappingSchema = z.object({
  match: z.string().max(100),
  label: z.string().max(60),
  color: z.string().optional(),
})

const seriesSchema = z.object({
  element_id: z.string({ error: 'Choose an element' }).min(1, 'Choose an element'),
  field: attributeSchema.optional(),
  label: z.string().max(60).optional(),
  color: z.string().optional(),
})

function optionSchema(field: OptionField): z.ZodType {
  switch (field.kind) {
    case 'number': {
      let n = z.number({ error: 'Enter a number' })
      if (field.integer) n = n.int('Whole numbers only')
      if (field.min !== undefined) n = n.min(field.min, `Must be ≥ ${field.min}`)
      if (field.max !== undefined) n = n.max(field.max, `Must be ≤ ${field.max}`)
      return n.optional()
    }
    case 'boolean':
      return z.boolean().optional()
    case 'thresholds':
      return z.array(thresholdSchema).optional()
    case 'select':
      return z.enum(field.choices.map((c) => c.value) as [string, ...string[]]).optional()
    case 'attribute':
      return attributeSchema.optional()
    case 'mappings':
      return z.array(mappingSchema).optional()
    case 'series':
      return z.array(seriesSchema).optional()
    default:
      return z.string().max(200).optional()
  }
}

function formSchema(def: WidgetDefinition) {
  const shape: Record<string, z.ZodType> = {}
  for (const f of def.fields) shape[f.key] = optionSchema(f)
  return z
    .object({
      element_id: z.string({ error: 'Choose an element' }).min(1, 'Choose an element'),
      type: z.string(),
      title: z.string().max(100),
      options: z.object(shape).loose(),
    })
    .superRefine((v, ctx) => {
      const o = v.options as Options
      if (typeof o.min === 'number' && typeof o.max === 'number' && o.min >= o.max)
        ctx.addIssue({
          code: 'custom',
          path: ['options', 'max'],
          message: 'Maximum must be greater than minimum',
        })
    })
}

type Values = { element_id: string; type: string; title: string; options: Options }

/** Keep only the keys a widget type understands, falling back to its defaults. */
function optionsFor(def: WidgetDefinition, el: MyElement | undefined, current: Options): Options {
  const base = el ? def.defaultOptions(el) : {}
  const out: Options = { ...base }
  for (const f of def.fields)
    if (current[f.key] !== undefined && current[f.key] !== '') out[f.key] = current[f.key]
  return out
}

export function WidgetConfigSheet({
  widget,
  elements,
  onOpenChange,
  onApply,
}: {
  widget: Widget | null
  elements: Map<string, MyElement>
  onOpenChange: (o: boolean) => void
  onApply: (id: string, patch: Pick<Widget, 'element_id' | 'type' | 'title' | 'options'>) => void
}) {
  return (
    <Sheet open={!!widget} onOpenChange={onOpenChange}>
      <SheetContent className="sm:max-w-md" aria-describedby={undefined}>
        {widget && (
          <ConfigForm
            key={widget.id}
            widget={widget}
            elements={elements}
            onClose={() => onOpenChange(false)}
            onApply={onApply}
          />
        )}
      </SheetContent>
    </Sheet>
  )
}

function ConfigForm({
  widget,
  elements,
  onClose,
  onApply,
}: {
  widget: Widget
  elements: Map<string, MyElement>
  onClose: () => void
  onApply: (id: string, patch: Pick<Widget, 'element_id' | 'type' | 'title' | 'options'>) => void
}) {
  const [type, setType] = useState(widget.type)
  const def = getWidget(type) ?? WIDGETS[0]!
  const schema = useMemo(() => formSchema(def), [def])
  // the schema depends on the (changeable) widget type: resolve through a ref
  const schemaRef = useRef(schema)
  useLayoutEffect(() => {
    schemaRef.current = schema
  }, [schema])
  const form = useForm<Values>({
    resolver: (values, ctx, opts) =>
      zodResolver(schemaRef.current as never)(values, ctx, opts as never) as never,
    defaultValues: {
      element_id: widget.element_id ?? '',
      type: widget.type,
      title: widget.title ?? '',
      options: widget.options,
    },
  })
  const elementId = useWatch({ control: form.control, name: 'element_id' })
  const element = elements.get(elementId)
  const [pickerOpen, setPickerOpen] = useState(!widget.element_id)
  const { byId: deviceById } = useDevices()
  const errors = form.formState.errors
  const discovered = useDiscoveredFields(elementId || undefined)
  const dataFields = def.fields.filter((f) => f.section === 'data')
  const displayFields = def.fields.filter((f) => f.section !== 'data')

  // re-validate with the new type's schema
  useEffect(() => {
    form.setValue('type', type)
  }, [type, form])

  const chooseElement = (el: MyElement) => {
    const current = getWidget(type)
    let nextType = type
    if (!current || current.fit(el) === 'no') nextType = widgetsFor(el)[0]?.def.type ?? type
    const nextDef = getWidget(nextType)!
    setType(nextType)
    form.setValue('element_id', el.id, { shouldValidate: true, shouldDirty: true })
    form.setValue('options', nextDef.defaultOptions(el), { shouldDirty: true })
    setPickerOpen(false)
  }

  const chooseType = (t: string) => {
    const nextDef = getWidget(t)
    if (!nextDef) return
    setType(t)
    form.setValue('options', optionsFor(nextDef, element, form.getValues('options')), { shouldDirty: true })
  }

  const submit = form.handleSubmit((v) => {
    const clean: Options = {}
    for (const [k, val] of Object.entries(v.options)) {
      if (val === undefined || val === '' || Number.isNaN(val)) continue
      if (k === 'mappings' && Array.isArray(val))
        clean[k] = val.filter(
          (m) => m && String(m.match ?? '').trim() !== '' && String(m.label ?? '').trim() !== '',
        )
      else clean[k] = val
    }
    // the parent closes the sheet once the change is applied
    onApply(widget.id, { element_id: v.element_id, type, title: v.title.trim() || undefined, options: clean })
  })

  const fits = element ? widgetsFor(element) : WIDGETS.map((d) => ({ def: d, fit: 'ok' as const }))

  return (
    <form onSubmit={submit} className="flex h-full flex-col" noValidate>
      <SheetHeader>
        <SheetTitle className="flex items-center gap-2">
          <def.icon className="text-muted-foreground size-4" /> Configure {def.label.toLowerCase()}
        </SheetTitle>
        <SheetDescription>
          Changes apply to the dashboard when you press Apply; save the dashboard to keep them.
        </SheetDescription>
      </SheetHeader>
      <SheetBody className="space-y-5">
        <Field label="Element" error={errors.element_id?.message} required>
          <Popover open={pickerOpen} onOpenChange={setPickerOpen}>
            <PopoverTrigger asChild>
              <Button
                variant="outline"
                className="h-auto min-h-9 w-full justify-between py-1.5 font-normal"
                aria-invalid={!!errors.element_id}
                data-testid="element-picker"
              >
                {element ? (
                  <span className="min-w-0 text-left">
                    <span className="block truncate">{element.name}</span>
                    <span className="text-muted-foreground block truncate text-xs">
                      {deviceById.get(element.device_id)?.name}
                    </span>
                  </span>
                ) : (
                  <span className="text-muted-foreground">Choose an element…</span>
                )}
                <ChevronsUpDown className="opacity-50" />
              </Button>
            </PopoverTrigger>
            <PopoverContent
              align="start"
              className="flex h-80 w-(--radix-popover-trigger-width) min-w-72 flex-col p-2"
            >
              <ElementList
                autoFocus
                value={elementId}
                onSelect={chooseElement}
                fit={def.fit}
                className="flex-1"
              />
            </PopoverContent>
          </Popover>
        </Field>

        <Field label="Widget type">
          <Select value={type} onValueChange={chooseType}>
            <SelectTrigger aria-label="Widget type">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {fits.map(({ def: d, fit }) => (
                <SelectItem key={d.type} value={d.type}>
                  <d.icon className="text-muted-foreground size-4" /> {d.label}
                  {fit === 'suggested' && (
                    <Badge variant="success" className="ml-1">
                      Suggested
                    </Badge>
                  )}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>

        <Field
          label="Title"
          htmlFor="widget-title"
          error={errors.title?.message}
          description="Leave empty to use the element name."
        >
          <Input
            id="widget-title"
            placeholder={element?.name ?? 'Widget title'}
            {...form.register('title')}
          />
        </Field>

        {dataFields.length > 0 && (
          <FieldSection
            title="Data"
            fields={dataFields}
            type={type}
            control={form.control}
            errors={errors}
            discovered={discovered}
            elements={elements}
          />
        )}
        {displayFields.length > 0 && (
          <FieldSection
            title="Display"
            fields={displayFields}
            type={type}
            control={form.control}
            errors={errors}
            discovered={discovered}
            elements={elements}
          />
        )}
      </SheetBody>
      <SheetFooter>
        <Button variant="outline" onClick={onClose}>
          Cancel
        </Button>
        <Button type="submit" data-testid="apply-widget-config">
          Apply
        </Button>
      </SheetFooter>
    </form>
  )
}

function FieldSection({
  title,
  fields,
  type,
  control,
  errors,
  discovered,
  elements,
}: {
  title: string
  fields: OptionField[]
  type: string
  control: Control<Values>
  errors: ReturnType<typeof useForm<Values>>['formState']['errors']
  discovered: DiscoveredField[]
  elements: Map<string, MyElement>
}) {
  return (
    <div className="space-y-4 border-t pt-5" data-testid={`section-${title.toLowerCase()}`}>
      <h3 className="text-sm font-semibold">{title}</h3>
      <div className="grid grid-cols-2 gap-4">
        {fields.map((f) => (
          <OptionInput
            key={`${type}-${f.key}`}
            field={f}
            control={control}
            discovered={discovered}
            elements={elements}
            error={(errors.options as Record<string, { message?: string }> | undefined)?.[f.key]?.message}
          />
        ))}
      </div>
    </div>
  )
}

function OptionInput({
  field,
  control,
  error,
  discovered,
  elements,
}: {
  field: OptionField
  control: Control<Values>
  error?: string
  discovered: DiscoveredField[]
  elements: Map<string, MyElement>
}) {
  const id = `opt-${field.key}`
  const name = `options.${field.key}` as const
  const wide = field.kind === 'thresholds' || field.kind === 'boolean' || field.kind === 'color'
  return (
    <Controller
      control={control}
      name={name}
      render={({ field: f }) => {
        const v = f.value as unknown
        switch (field.kind) {
          case 'boolean':
            return (
              <div className="col-span-2 flex items-center justify-between gap-4">
                <Label htmlFor={id}>{field.label}</Label>
                <Switch id={id} checked={v === true} onCheckedChange={f.onChange} />
              </div>
            )
          case 'number':
            return (
              <Field label={field.label} htmlFor={id} error={error} description={field.help}>
                <Input
                  id={id}
                  type="number"
                  inputMode="decimal"
                  step={field.step ?? 'any'}
                  placeholder={field.placeholder}
                  value={typeof v === 'number' && !Number.isNaN(v) ? v : ''}
                  aria-invalid={!!error}
                  onChange={(e) => f.onChange(e.target.value === '' ? undefined : e.target.valueAsNumber)}
                />
              </Field>
            )
          case 'select':
            return (
              <Field label={field.label} error={error} className="col-span-2 sm:col-span-1">
                <Select value={typeof v === 'string' ? v : undefined} onValueChange={f.onChange}>
                  <SelectTrigger aria-label={field.label}>
                    <SelectValue placeholder="Choose…" />
                  </SelectTrigger>
                  <SelectContent>
                    {field.choices.map((c) => (
                      <SelectItem key={c.value} value={c.value}>
                        {c.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            )
          case 'color':
            return (
              <Field label={field.label} className={cn(wide && 'col-span-2')}>
                <ColorSwatches
                  value={typeof v === 'string' ? v : 'blue'}
                  onChange={f.onChange}
                  label={field.label}
                />
              </Field>
            )
          case 'attribute':
            return (
              <Field
                label={field.label}
                htmlFor={id}
                error={error}
                description={field.help}
                className="col-span-2"
              >
                <AttributeInput
                  id={id}
                  value={typeof v === 'string' ? v : ''}
                  onChange={f.onChange}
                  discovered={discovered}
                  accepts={field.accepts}
                  placeholder={field.placeholder}
                  xAxis={field.xAxis}
                  invalid={!!error}
                />
              </Field>
            )
          case 'mappings':
            return (
              <div className="col-span-2">
                <MappingsEditor
                  control={control as never}
                  name={name}
                  label={field.label}
                  help={field.help}
                />
              </div>
            )
          case 'series':
            return (
              <div className="col-span-2">
                <SeriesEditor
                  control={control as never}
                  name={name}
                  label={field.label}
                  help={field.help}
                  elements={elements}
                />
              </div>
            )
          case 'thresholds':
            return (
              <div className="col-span-2">
                <ThresholdsEditor control={control} label={field.label} help={field.help} />
              </div>
            )
          default:
            return (
              <Field
                label={field.label}
                htmlFor={id}
                error={error}
                description={'help' in field ? field.help : undefined}
              >
                <Input
                  id={id}
                  placeholder={'placeholder' in field ? field.placeholder : undefined}
                  value={typeof v === 'string' ? v : ''}
                  onChange={(e) => f.onChange(e.target.value)}
                />
              </Field>
            )
        }
      }}
    />
  )
}

function ThresholdsEditor({
  control,
  label,
  help,
}: {
  control: Control<Values>
  label: string
  help?: string
}) {
  const { fields, append, remove } = useFieldArray({ control, name: 'options.thresholds' as never })
  const colors = ['yellow', 'red', 'orange', 'purple', 'green', 'blue']
  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <Label>{label}</Label>
        <Button
          variant="ghost"
          size="xs"
          onClick={() =>
            append({ value: fields.length ? 0 : 0, color: colors[fields.length % colors.length] } as never)
          }
        >
          <Plus /> Add
        </Button>
      </div>
      {help && <p className="text-muted-foreground text-xs">{help}</p>}
      {fields.length === 0 && (
        <p className="text-muted-foreground rounded-md border border-dashed px-3 py-3 text-center text-xs">
          No thresholds
        </p>
      )}
      {fields.map((row, i) => (
        <div key={row.id} className="flex items-center gap-2">
          <Controller
            control={control}
            name={`options.thresholds.${i}.value` as never}
            render={({ field: f, fieldState }) => (
              <Input
                type="number"
                step="any"
                aria-label={`Threshold ${i + 1} value`}
                aria-invalid={!!fieldState.error}
                className="w-28"
                value={typeof f.value === 'number' && !Number.isNaN(f.value) ? f.value : ''}
                onChange={(e) => f.onChange(e.target.value === '' ? undefined : e.target.valueAsNumber)}
              />
            )}
          />
          <Controller
            control={control}
            name={`options.thresholds.${i}.color` as never}
            render={({ field: f }) => (
              <ColorSwatches
                value={f.value as string}
                onChange={f.onChange}
                label={`Threshold ${i + 1} color`}
              />
            )}
          />
          <Button
            variant="ghost"
            size="icon-xs"
            className="ml-auto"
            onClick={() => remove(i)}
            aria-label={`Remove threshold ${i + 1}`}
          >
            <Trash2 />
          </Button>
        </div>
      ))}
    </div>
  )
}
