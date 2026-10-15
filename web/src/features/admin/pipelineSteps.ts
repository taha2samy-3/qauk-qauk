// Pipeline steps as the editor holds them (text as typed) and as the API takes them.

export type StepKind = 'pick' | 'scale' | 'unit' | 'round' | 'clamp' | 'map' | 'deadband' | 'drop_if' | 'script'

export const STEP_KINDS: { kind: StepKind; label: string }[] = [
  { kind: 'pick', label: 'pick (select path)' },
  { kind: 'scale', label: 'scale (mul × v + add)' },
  { kind: 'unit', label: 'unit (conversion)' },
  { kind: 'round', label: 'round (decimals)' },
  { kind: 'clamp', label: 'clamp (min..max)' },
  { kind: 'map', label: 'map (dictionary)' },
  { kind: 'deadband', label: 'deadband (drop quiet)' },
  { kind: 'drop_if', label: 'drop_if (condition)' },
  { kind: 'script', label: 'script (JavaScript)' },
]

/** One step in the editor. Every value is the text as typed; toConfig converts it. */
export interface StepDraft {
  id: number
  kind: StepKind
  field: string
  path: string
  mul: string
  add: string
  from: string
  to: string
  decimals: string
  min: string
  max: string
  /** JSON object text */
  table: string
  /** JSON text; empty for none */
  default: string
  abs: string
  pct: string
  max_silence: string
  op: string
  value: string
  source: string
}

type TextKey = Exclude<keyof StepDraft, 'id' | 'kind' | 'table' | 'default'>
export type NumberKey = 'mul' | 'add' | 'decimals' | 'min' | 'max' | 'abs' | 'pct' | 'value'

const TEXT_KEYS: TextKey[] = [
  'field', 'path', 'mul', 'add', 'from', 'to', 'decimals', 'min', 'max',
  'abs', 'pct', 'max_silence', 'op', 'value', 'source',
]

let nextStepId = 0

function blankStep(kind: StepKind): StepDraft {
  return {
    id: ++nextStepId, kind, field: '', path: '', mul: '', add: '', from: '', to: '', decimals: '',
    min: '', max: '', table: '', default: '', abs: '', pct: '', max_silence: '', op: '<', value: '', source: '',
  }
}

const SCRIPT_TEMPLATE = `function transform(msg, ctx) {
  // msg.value = msg.value * 2;
  return msg; // null drops the message
}

// Optional: turns a command back into device units.
// Without it, the script doesn't apply to commands.
function untransform(msg, ctx) {
  return msg;
}`

export function newStep(kind: StepKind): StepDraft {
  const s = blankStep(kind)
  switch (kind) {
    case 'pick': s.path = 'value'; break
    case 'scale': s.mul = '1'; break
    case 'unit': s.from = 'degC'; s.to = 'degF'; break
    case 'round': s.decimals = '1'; break
    case 'clamp': s.min = '0'; s.max = '100'; break
    case 'map': s.table = '{\n  "OPEN": "ON",\n  "CLOSED": "OFF"\n}'; break
    case 'deadband': s.abs = '0.5'; s.max_silence = '10m'; break
    case 'drop_if': s.op = '<'; s.value = '0'; break
    case 'script': s.source = SCRIPT_TEMPLATE; break
  }
  return s
}

/** Stored step configs → editor steps. */
export function toDrafts(raw: unknown): StepDraft[] {
  if (!Array.isArray(raw)) return []
  return raw.map((item) => {
    const c = (item ?? {}) as Record<string, unknown>
    const d = blankStep(c.kind as StepKind)
    d.op = ''
    for (const k of TEXT_KEYS) {
      const v = c[k]
      if (v !== undefined && v !== null) d[k] = String(v)
    }
    if (c.table !== undefined) d.table = JSON.stringify(c.table, null, 2)
    if (c.default !== undefined) d.default = JSON.stringify(c.default)
    return d
  })
}

/** An editor step → its JSON config, with the first value that doesn't parse. */
export function toConfig(d: StepDraft): { config: Record<string, unknown>; error?: string } {
  const out: Record<string, unknown> = { kind: d.kind }
  let error: string | undefined
  const num = (key: NumberKey, label: string, integer = false) => {
    const t = d[key].trim()
    if (t === '') return
    const n = Number(t)
    if (!Number.isFinite(n) || (integer && !Number.isInteger(n))) {
      error ??= `${label} must be ${integer ? 'a whole number' : 'a number'}`
      return
    }
    out[key] = n
  }
  if (d.kind !== 'pick' && d.kind !== 'script' && d.field.trim()) out.field = d.field.trim()
  switch (d.kind) {
    case 'pick':
      out.path = d.path.trim()
      break
    case 'scale':
      num('mul', 'Multiplier')
      num('add', 'Offset')
      if (out.add === 0) delete out.add
      break
    case 'unit':
      out.from = d.from.trim()
      out.to = d.to.trim()
      break
    case 'round':
      num('decimals', 'Decimals', true)
      break
    case 'clamp':
      num('min', 'Minimum')
      num('max', 'Maximum')
      break
    case 'map': {
      let table: unknown
      try {
        table = JSON.parse(d.table.trim() || '{}')
      } catch {
        error = 'The mapping table isn’t valid JSON'
        break
      }
      if (!table || typeof table !== 'object' || Array.isArray(table)) {
        error = 'The mapping table must be a JSON object, like {"OPEN": "ON"}'
        break
      }
      out.table = table
      if (d.default.trim()) {
        try {
          out.default = JSON.parse(d.default)
        } catch {
          error = 'The default isn’t valid JSON (quote text: "OFF")'
        }
      }
      break
    }
    case 'deadband':
      num('abs', 'Absolute delta')
      num('pct', 'Percent')
      if (d.max_silence.trim()) out.max_silence = d.max_silence.trim()
      break
    case 'drop_if':
      out.op = d.op
      num('value', 'Comparison value')
      break
    case 'script':
      out.source = d.source
      break
  }
  return { config: out, error }
}
