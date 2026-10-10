import { useState, useMemo } from 'react'
import {
  Workflow,
  Plus,
  Trash2,
  ChevronUp,
  ChevronDown,
  RotateCcw,
  Play,
  History,
  FlaskConical,
  AlertTriangle,
} from 'lucide-react'
import { toast } from 'sonner'
import type { AdminElement } from '@/api/types'
import { RelativeTime } from '@/components/RelativeTime'
import { useConfirm } from '@/components/ConfirmDialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { Spinner } from '@/components/ui/spinner'
import { EChart } from '@/widgets/EChart'
import { chartPalette } from '@/lib/chartTheme'
import { useTheme } from '@/lib/theme'
import { adminApi, adminKeys, useAdminMutation, useElementPipeline } from './api'

type StepKind =
  | 'pick'
  | 'scale'
  | 'unit'
  | 'round'
  | 'clamp'
  | 'map'
  | 'deadband'
  | 'drop_if'
  | 'script'

interface PipelineStep {
  kind: StepKind
  field?: string
  path?: string
  mul?: number
  add?: number
  from?: string
  to?: string
  decimals?: number
  min?: number
  max?: number
  table?: Record<string, unknown>
  tableText?: string
  default?: unknown
  abs?: number
  pct?: number
  max_silence?: string
  op?: string
  value?: unknown
  source?: string
}

function isStepInvertible(step: PipelineStep): { invertible: boolean; warning?: string } {
  switch (step.kind) {
    case 'scale':
      return { invertible: typeof step.mul === 'number' && step.mul !== 0 }
    case 'unit':
      return { invertible: Boolean(step.from && step.to) }
    case 'map': {
      if (!step.table || typeof step.table !== 'object') return { invertible: false }
      const values = Object.values(step.table)
      const set = new Set(values.map((v) => JSON.stringify(v)))
      if (set.size !== values.length) {
        return {
          invertible: false,
          warning: 'Map table has duplicate target values; cannot be inverted for commands.',
        }
      }
      return { invertible: true }
    }
    case 'script': {
      const hasUntransform = step.source?.includes('untransform') ?? false
      return { invertible: hasUntransform }
    }
    default:
      return { invertible: false }
  }
}

function cleanStepForSave(s: PipelineStep): Record<string, unknown> {
  const out: Record<string, unknown> = { kind: s.kind }
  if (s.field) out.field = s.field
  switch (s.kind) {
    case 'pick':
      out.path = s.path ?? 'value'
      break
    case 'scale':
      out.mul = Number(s.mul ?? 1)
      if (s.add !== undefined && s.add !== 0) out.add = Number(s.add)
      break
    case 'unit':
      out.from = s.from ?? ''
      out.to = s.to ?? ''
      break
    case 'round':
      out.decimals = Number(s.decimals ?? 0)
      break
    case 'clamp':
      if (s.min !== undefined) out.min = Number(s.min)
      if (s.max !== undefined) out.max = Number(s.max)
      break
    case 'map':
      if (s.table) out.table = s.table
      if (s.default !== undefined) out.default = s.default
      break
    case 'deadband':
      if (s.abs !== undefined) out.abs = Number(s.abs)
      if (s.pct !== undefined) out.pct = Number(s.pct)
      if (s.max_silence) out.max_silence = s.max_silence
      break
    case 'drop_if':
      out.op = s.op ?? '=='
      out.value = s.value
      break
    case 'script':
      out.source = s.source ?? ''
      break
  }
  return out
}

function parseStepsFromRaw(raw: unknown): PipelineStep[] {
  if (!Array.isArray(raw)) return []
  return raw.map((item) => {
    const s = { ...item } as PipelineStep
    if (s.kind === 'map' && s.table && typeof s.table === 'object') {
      s.tableText = JSON.stringify(s.table, null, 2)
    }
    return s
  })
}

export function PipelineSheet({
  element,
  onOpenChange,
}: {
  element: AdminElement | null
  onOpenChange: (open: boolean) => void
}) {
  const theme = useTheme()
  const confirm = useConfirm()
  const pipelineQuery = useElementPipeline(element?.id)
  const qkPrefix = adminKeys.pipeline(element?.id ?? '')
  const elementsQk = adminKeys.elements()

  const saveMut = useAdminMutation(adminApi.savePipeline, [qkPrefix, elementsQk])
  const rollbackMut = useAdminMutation(adminApi.rollbackPipeline, [qkPrefix, elementsQk])
  const deleteMut = useAdminMutation(adminApi.deletePipeline, [qkPrefix, elementsQk])

  const [activeTab, setActiveTab] = useState<'steps' | 'preview' | 'test' | 'versions'>('steps')

  // Working copy of steps
  const [workingSteps, setWorkingSteps] = useState<PipelineStep[]>([])
  const [loadedElementId, setLoadedElementId] = useState<string | null>(null)

  // Initialize or reset working steps when query data loads
  const currentPipeline = pipelineQuery.data?.current
  const currentVersions = pipelineQuery.data?.versions ?? []

  if (element && element.id !== loadedElementId && pipelineQuery.data) {
    setLoadedElementId(element.id)
    if (currentPipeline?.steps) {
      setWorkingSteps(parseStepsFromRaw(currentPipeline.steps))
    } else {
      setWorkingSteps([])
    }
  }

  // Preview state
  const [previewLoading, setPreviewLoading] = useState(false)
  const [previewData, setPreviewData] = useState<{
    summary?: { total: number; passed: number; filtered: number; failed: number }
    rows?: Array<{ time: string; before: unknown; after?: unknown; filtered: boolean; reason?: string; error?: string }> | null
  } | null>(null)

  // Test state
  const [testInput, setTestInput] = useState('{\n  "value": 100\n}')
  const [testLast, setTestLast] = useState('')
  const [testLoading, setTestLoading] = useState(false)
  const [testResult, setTestResult] = useState<{
    after?: unknown
    filtered?: boolean
    reason?: string
    error?: string
    inverse?: unknown
  } | null>(null)

  const handleAddStep = (kind: StepKind) => {
    const newStep: PipelineStep = { kind, field: 'value' }
    switch (kind) {
      case 'pick':
        newStep.path = 'value'
        break
      case 'scale':
        newStep.mul = 1
        newStep.add = 0
        break
      case 'unit':
        newStep.from = 'degC'
        newStep.to = 'degF'
        break
      case 'round':
        newStep.decimals = 1
        break
      case 'clamp':
        newStep.min = 0
        newStep.max = 100
        break
      case 'map':
        newStep.table = { OPEN: 'ON', CLOSED: 'OFF' }
        newStep.tableText = '{\n  "OPEN": "ON",\n  "CLOSED": "OFF"\n}'
        break
      case 'deadband':
        newStep.abs = 0.5
        newStep.max_silence = '10m'
        break
      case 'drop_if':
        newStep.op = '<'
        newStep.value = 0
        break
      case 'script':
        newStep.source = `function transform(msg, ctx) {
  // msg.value = msg.value * 2;
  return msg;
}

function untransform(msg, ctx) {
  // Inverse for commands
  return msg;
}`
        break
    }
    setWorkingSteps([...workingSteps, newStep])
  }

  const handleRemoveStep = (index: number) => {
    setWorkingSteps(workingSteps.filter((_, i) => i !== index))
  }

  const handleMoveStep = (index: number, dir: -1 | 1) => {
    const target = index + dir
    if (target < 0 || target >= workingSteps.length) return
    const next = [...workingSteps]
    const temp = next[index]!
    next[index] = next[target]!
    next[target] = temp
    setWorkingSteps(next)
  }

  const handleUpdateStep = (index: number, updates: Partial<PipelineStep>) => {
    const next = [...workingSteps]
    next[index] = { ...next[index]!, ...updates }
    setWorkingSteps(next)
  }

  const handleSave = async () => {
    if (!element) return
    const cleaned = workingSteps.map(cleanStepForSave)
    try {
      await saveMut.mutateAsync({
        elementId: element.id,
        steps: cleaned,
      })
      toast.success('Pipeline saved successfully')
      void pipelineQuery.refetch()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      toast.error(`Failed to save pipeline: ${msg}`)
    }
  }

  const handleRunPreview = async () => {
    if (!element) return
    setPreviewLoading(true)
    const cleaned = workingSteps.map(cleanStepForSave)
    try {
      const res = await adminApi.previewPipeline({
        elementId: element.id,
        steps: cleaned,
      })
      setPreviewData(res)
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      toast.error(`Preview failed: ${msg}`)
    } finally {
      setPreviewLoading(false)
    }
  }

  const handleRunTest = async () => {
    if (!element) return
    let parsedInput: unknown
    try {
      parsedInput = JSON.parse(testInput)
    } catch {
      toast.error('Invalid JSON for test message')
      return
    }

    let parsedLast: unknown = undefined
    if (testLast.trim()) {
      try {
        parsedLast = JSON.parse(testLast)
      } catch {
        toast.error('Invalid JSON for last message')
        return
      }
    }

    setTestLoading(true)
    const cleaned = workingSteps.map(cleanStepForSave)
    try {
      const res = await adminApi.testPipeline({
        elementId: element.id,
        steps: cleaned,
        message: parsedInput,
        last: parsedLast,
      })
      setTestResult(res)
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      toast.error(`Test failed: ${msg}`)
    } finally {
      setTestLoading(false)
    }
  }

  const handleRollback = (ver: number) => {
    if (!element) return
    confirm.ask({
      title: `Rollback pipeline to version ${ver}?`,
      description: 'This will save a new pipeline version with the exact steps from version ' + ver + '.',
      onConfirm: async () => {
        try {
          await rollbackMut.mutateAsync({
            elementId: element.id,
            version: ver,
          })
          toast.success(`Pipeline rolled back to version ${ver}`)
          void pipelineQuery.refetch()
        } catch (err: unknown) {
          const msg = err instanceof Error ? err.message : String(err)
          toast.error(`Rollback failed: ${msg}`)
        }
      },
    })
  }

  const handleDeletePipeline = () => {
    if (!element) return
    confirm.ask({
      title: 'Remove pipeline?',
      description: 'The element will pass raw device values directly to the bus with no transformations.',
      destructive: true,
      onConfirm: async () => {
        try {
          await deleteMut.mutateAsync(element.id)
          toast.success('Pipeline removed')
          setWorkingSteps([])
          void pipelineQuery.refetch()
        } catch (err: unknown) {
          const msg = err instanceof Error ? err.message : String(err)
          toast.error(`Failed to remove pipeline: ${msg}`)
        }
      },
    })
  }

  // Chart data for preview
  const chartOption = useMemo(() => {
    if (!previewData?.rows || previewData.rows.length === 0) return null
    const pal = chartPalette(theme.resolved)

    const beforeData: [number, number][] = []
    const afterData: [number, number][] = []

    for (const r of previewData.rows) {
      const t = new Date(r.time).getTime()
      if (isNaN(t)) continue

      // extract numeric before
      let bVal: number | null = null
      if (typeof r.before === 'number') bVal = r.before
      else if (r.before && typeof r.before === 'object' && 'value' in r.before && typeof (r.before as Record<string, unknown>).value === 'number') {
        bVal = (r.before as Record<string, unknown>).value as number
      }

      if (bVal !== null) beforeData.push([t, bVal])

      // extract numeric after if not filtered
      if (!r.filtered && !r.error && r.after !== undefined) {
        let aVal: number | null = null
        if (typeof r.after === 'number') aVal = r.after
        else if (r.after && typeof r.after === 'object' && 'value' in r.after && typeof (r.after as Record<string, unknown>).value === 'number') {
          aVal = (r.after as Record<string, unknown>).value as number
        }
        if (aVal !== null) afterData.push([t, aVal])
      }
    }

    if (beforeData.length === 0 && afterData.length === 0) return null

    return {
      backgroundColor: 'transparent',
      animation: false,
      grid: { top: 20, right: 16, bottom: 24, left: 40 },
      tooltip: {
        trigger: 'axis' as const,
        backgroundColor: pal.tooltipBg,
        borderColor: pal.tooltipBorder,
        textStyle: { color: pal.text, fontSize: 12 },
      },
      legend: {
        data: ['Before', 'After'],
        textStyle: { color: pal.text },
        top: 0,
      },
      xAxis: {
        type: 'time' as const,
        axisLine: { lineStyle: { color: pal.axis } },
        axisLabel: { color: pal.muted, fontSize: 10 },
        splitLine: { show: false },
      },
      yAxis: {
        type: 'value' as const,
        scale: true,
        axisLabel: { color: pal.muted, fontSize: 10 },
        splitLine: { lineStyle: { color: pal.grid } },
      },
      series: [
        {
          name: 'Before',
          type: 'line' as const,
          data: beforeData,
          showSymbol: false,
          lineStyle: { width: 1.5, type: 'dashed' as const, color: '#94a3b8' },
          itemStyle: { color: '#94a3b8' },
        },
        {
          name: 'After',
          type: 'line' as const,
          data: afterData,
          showSymbol: false,
          lineStyle: { width: 2, color: '#10b981' },
          itemStyle: { color: '#10b981' },
        },
      ],
    }
  }, [previewData, theme.resolved])

  return (
    <>
      <Sheet open={!!element} onOpenChange={onOpenChange}>
        <SheetContent className="sm:max-w-2xl overflow-y-auto">
          <SheetHeader>
            <div className="flex items-center justify-between">
              <SheetTitle className="flex items-center gap-2">
                <Workflow className="size-5 text-primary" />
                Pipeline · {element?.name}
              </SheetTitle>
              {currentPipeline && (
                <Badge variant="outline" className="font-mono text-xs">
                  v{currentPipeline.version} ({workingSteps.length} steps)
                </Badge>
              )}
            </div>
            <SheetDescription>
              Transforms raw device values into engineering units in memory before Redpanda. Invertible steps run in reverse for commands.
            </SheetDescription>
          </SheetHeader>

          <SheetBody className="mt-4 space-y-6">
            <Tabs value={activeTab} onValueChange={(v) => setActiveTab(v as typeof activeTab)}>
              <TabsList className="grid grid-cols-4 w-full">
                <TabsTrigger value="steps" className="gap-1.5">
                  <Workflow className="size-3.5" /> Steps ({workingSteps.length})
                </TabsTrigger>
                <TabsTrigger value="preview" className="gap-1.5">
                  <Play className="size-3.5" /> Preview (500)
                </TabsTrigger>
                <TabsTrigger value="test" className="gap-1.5">
                  <FlaskConical className="size-3.5" /> Test
                </TabsTrigger>
                <TabsTrigger value="versions" className="gap-1.5">
                  <History className="size-3.5" /> Versions
                </TabsTrigger>
              </TabsList>

              {/* 1. STEPS TAB */}
              <TabsContent value="steps" className="space-y-4 pt-4">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <Select onValueChange={(k) => handleAddStep(k as StepKind)}>
                      <SelectTrigger className="w-40 h-8 text-xs">
                        <Plus className="size-3.5 mr-1" /> Add Step…
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="pick">pick (select path)</SelectItem>
                        <SelectItem value="scale">scale (mul × v + add)</SelectItem>
                        <SelectItem value="unit">unit (conversion)</SelectItem>
                        <SelectItem value="round">round (decimals)</SelectItem>
                        <SelectItem value="clamp">clamp (min..max)</SelectItem>
                        <SelectItem value="map">map (dictionary)</SelectItem>
                        <SelectItem value="deadband">deadband (drop quiet)</SelectItem>
                        <SelectItem value="drop_if">drop_if (condition)</SelectItem>
                        <SelectItem value="script">script (JavaScript)</SelectItem>
                      </SelectContent>
                    </Select>
                    {workingSteps.length > 0 && (
                      <Button
                        variant="ghost"
                        size="xs"
                        className="text-muted-foreground hover:text-destructive"
                        onClick={() => setWorkingSteps([])}
                      >
                        Clear All
                      </Button>
                    )}
                  </div>
                  <div className="flex items-center gap-2">
                    {currentPipeline && (
                      <Button
                        variant="ghost"
                        size="xs"
                        className="text-destructive hover:bg-destructive/10"
                        onClick={handleDeletePipeline}
                        disabled={deleteMut.isPending}
                      >
                        Remove Pipeline
                      </Button>
                    )}
                    <Button
                      size="xs"
                      onClick={handleSave}
                      disabled={saveMut.isPending}
                      className="gap-1.5 font-medium"
                    >
                      {saveMut.isPending && <Spinner className="size-3" />}
                      Save as v{(currentPipeline?.version ?? 0) + 1}
                    </Button>
                  </div>
                </div>

                {workingSteps.length === 0 ? (
                  <div className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">
                    <Workflow className="mx-auto size-8 mb-2 opacity-50" />
                    No steps configured. This element passes raw messages directly through without alteration.
                    <div className="mt-3">
                      <Button size="xs" variant="outline" onClick={() => handleAddStep('scale')}>
                        <Plus className="size-3.5 mr-1" /> Add a Scale Step
                      </Button>
                    </div>
                  </div>
                ) : (
                  <div className="space-y-3">
                    {workingSteps.map((step, idx) => {
                      const inv = isStepInvertible(step)
                      return (
                        <div
                          key={idx}
                          className="rounded-lg border bg-card/50 p-3.5 text-card-foreground shadow-xs transition-colors hover:border-foreground/20"
                        >
                          <div className="flex items-center justify-between pb-2 mb-2 border-b">
                            <div className="flex items-center gap-2">
                              <span className="flex size-5 items-center justify-center rounded-full bg-muted font-mono text-[11px] font-semibold text-muted-foreground">
                                {idx + 1}
                              </span>
                              <Badge variant="outline" className="font-mono text-xs uppercase font-bold tracking-wider">
                                {step.kind}
                              </Badge>
                              {inv.invertible ? (
                                <Badge variant="outline" className="border-emerald-500/40 text-emerald-600 dark:text-emerald-400 text-[10px]">
                                  also applied to commands
                                </Badge>
                              ) : (
                                <span className="text-[11px] text-muted-foreground">uplink only</span>
                              )}
                            </div>
                            <div className="flex items-center gap-1">
                              <Button
                                variant="ghost"
                                size="icon-xs"
                                disabled={idx === 0}
                                onClick={() => handleMoveStep(idx, -1)}
                                aria-label="Move step up"
                              >
                                <ChevronUp className="size-3.5" />
                              </Button>
                              <Button
                                variant="ghost"
                                size="icon-xs"
                                disabled={idx === workingSteps.length - 1}
                                onClick={() => handleMoveStep(idx, 1)}
                                aria-label="Move step down"
                              >
                                <ChevronDown className="size-3.5" />
                              </Button>
                              <Button
                                variant="ghost"
                                size="icon-xs"
                                className="text-muted-foreground hover:text-destructive"
                                onClick={() => handleRemoveStep(idx)}
                                aria-label="Remove step"
                              >
                                <Trash2 className="size-3.5" />
                              </Button>
                            </div>
                          </div>

                          {inv.warning && (
                            <div className="mb-2 flex items-center gap-1.5 rounded-md bg-amber-500/10 px-2.5 py-1 text-xs text-amber-600 dark:text-amber-400">
                              <AlertTriangle className="size-3.5 shrink-0" />
                              {inv.warning}
                            </div>
                          )}

                          {/* Step Editors */}
                          <div className="grid gap-3 pt-1 text-xs">
                            {step.kind !== 'pick' && step.kind !== 'script' && (
                              <div className="grid grid-cols-4 items-center gap-2">
                                <span className="text-muted-foreground">Target Field</span>
                                <Input
                                  value={step.field ?? ''}
                                  placeholder="value"
                                  className="col-span-3 h-7 text-xs font-mono"
                                  onChange={(e) => handleUpdateStep(idx, { field: e.target.value })}
                                />
                              </div>
                            )}

                            {step.kind === 'pick' && (
                              <div className="grid grid-cols-4 items-center gap-2">
                                <span className="text-muted-foreground">JSON Path</span>
                                <Input
                                  value={step.path ?? ''}
                                  placeholder="e.g. data.temperature"
                                  className="col-span-3 h-7 text-xs font-mono"
                                  onChange={(e) => handleUpdateStep(idx, { path: e.target.value })}
                                />
                              </div>
                            )}

                            {step.kind === 'scale' && (
                              <div className="grid grid-cols-2 gap-3">
                                <div className="grid grid-cols-2 items-center gap-2">
                                  <span className="text-muted-foreground">Multiplier</span>
                                  <Input
                                    type="number"
                                    step="any"
                                    value={step.mul ?? 1}
                                    className="h-7 text-xs font-mono"
                                    onChange={(e) => handleUpdateStep(idx, { mul: Number(e.target.value) })}
                                  />
                                </div>
                                <div className="grid grid-cols-2 items-center gap-2">
                                  <span className="text-muted-foreground">Offset (Add)</span>
                                  <Input
                                    type="number"
                                    step="any"
                                    value={step.add ?? 0}
                                    className="h-7 text-xs font-mono"
                                    onChange={(e) => handleUpdateStep(idx, { add: Number(e.target.value) })}
                                  />
                                </div>
                              </div>
                            )}

                            {step.kind === 'unit' && (
                              <div className="grid grid-cols-2 gap-3">
                                <div className="grid grid-cols-2 items-center gap-2">
                                  <span className="text-muted-foreground">From Unit</span>
                                  <Input
                                    value={step.from ?? ''}
                                    placeholder="degC"
                                    className="h-7 text-xs font-mono"
                                    onChange={(e) => handleUpdateStep(idx, { from: e.target.value })}
                                  />
                                </div>
                                <div className="grid grid-cols-2 items-center gap-2">
                                  <span className="text-muted-foreground">To Unit</span>
                                  <Input
                                    value={step.to ?? ''}
                                    placeholder="degF"
                                    className="h-7 text-xs font-mono"
                                    onChange={(e) => handleUpdateStep(idx, { to: e.target.value })}
                                  />
                                </div>
                              </div>
                            )}

                            {step.kind === 'round' && (
                              <div className="grid grid-cols-4 items-center gap-2">
                                <span className="text-muted-foreground">Decimals (0–6)</span>
                                <Input
                                  type="number"
                                  min={0}
                                  max={6}
                                  value={step.decimals ?? 0}
                                  className="col-span-3 h-7 text-xs font-mono"
                                  onChange={(e) => handleUpdateStep(idx, { decimals: Number(e.target.value) })}
                                />
                              </div>
                            )}

                            {step.kind === 'clamp' && (
                              <div className="grid grid-cols-2 gap-3">
                                <div className="grid grid-cols-2 items-center gap-2">
                                  <span className="text-muted-foreground">Minimum</span>
                                  <Input
                                    type="number"
                                    step="any"
                                    value={step.min ?? 0}
                                    className="h-7 text-xs font-mono"
                                    onChange={(e) => handleUpdateStep(idx, { min: Number(e.target.value) })}
                                  />
                                </div>
                                <div className="grid grid-cols-2 items-center gap-2">
                                  <span className="text-muted-foreground">Maximum</span>
                                  <Input
                                    type="number"
                                    step="any"
                                    value={step.max ?? 100}
                                    className="h-7 text-xs font-mono"
                                    onChange={(e) => handleUpdateStep(idx, { max: Number(e.target.value) })}
                                  />
                                </div>
                              </div>
                            )}

                            {step.kind === 'map' && (
                              <div className="space-y-2">
                                <span className="text-muted-foreground">Mapping Table (JSON)</span>
                                <Textarea
                                  rows={3}
                                  className="font-mono text-xs"
                                  value={step.tableText ?? JSON.stringify(step.table ?? {}, null, 2)}
                                  onChange={(e) => {
                                    const text = e.target.value
                                    let parsed: Record<string, unknown> | undefined
                                    try {
                                      parsed = JSON.parse(text)
                                    } catch {
                                      // wait for valid json
                                    }
                                    handleUpdateStep(idx, { tableText: text, table: parsed ?? step.table })
                                  }}
                                />
                              </div>
                            )}

                            {step.kind === 'deadband' && (
                              <div className="grid grid-cols-3 gap-2">
                                <div>
                                  <span className="text-muted-foreground block text-[11px] mb-1">Absolute Delta</span>
                                  <Input
                                    type="number"
                                    step="any"
                                    placeholder="e.g. 0.5"
                                    value={step.abs ?? ''}
                                    className="h-7 text-xs font-mono"
                                    onChange={(e) => handleUpdateStep(idx, { abs: e.target.value ? Number(e.target.value) : undefined })}
                                  />
                                </div>
                                <div>
                                  <span className="text-muted-foreground block text-[11px] mb-1">Percent (%)</span>
                                  <Input
                                    type="number"
                                    step="any"
                                    placeholder="e.g. 5"
                                    value={step.pct ?? ''}
                                    className="h-7 text-xs font-mono"
                                    onChange={(e) => handleUpdateStep(idx, { pct: e.target.value ? Number(e.target.value) : undefined })}
                                  />
                                </div>
                                <div>
                                  <span className="text-muted-foreground block text-[11px] mb-1">Max Silence</span>
                                  <Input
                                    placeholder="10m"
                                    value={step.max_silence ?? ''}
                                    className="h-7 text-xs font-mono"
                                    onChange={(e) => handleUpdateStep(idx, { max_silence: e.target.value })}
                                  />
                                </div>
                              </div>
                            )}

                            {step.kind === 'drop_if' && (
                              <div className="grid grid-cols-3 gap-2">
                                <div>
                                  <span className="text-muted-foreground block text-[11px] mb-1">Operator</span>
                                  <Select
                                    value={step.op ?? '=='}
                                    onValueChange={(op) => handleUpdateStep(idx, { op })}
                                  >
                                    <SelectTrigger className="h-7 text-xs font-mono">
                                      <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                      <SelectItem value="<">&lt;</SelectItem>
                                      <SelectItem value="<=">&lt;=</SelectItem>
                                      <SelectItem value=">">&gt;</SelectItem>
                                      <SelectItem value=">=">&gt;=</SelectItem>
                                      <SelectItem value="==">==</SelectItem>
                                      <SelectItem value="!=">!=</SelectItem>
                                    </SelectContent>
                                  </Select>
                                </div>
                                <div className="col-span-2">
                                  <span className="text-muted-foreground block text-[11px] mb-1">Comparison Value</span>
                                  <Input
                                    value={String(step.value ?? '')}
                                    placeholder="e.g. 0 or 'offline'"
                                    className="h-7 text-xs font-mono"
                                    onChange={(e) => {
                                      const raw = e.target.value
                                      const numVal = Number(raw)
                                      handleUpdateStep(idx, { value: !isNaN(numVal) && raw.trim() !== '' ? numVal : raw })
                                    }}
                                  />
                                </div>
                              </div>
                            )}

                            {step.kind === 'script' && (
                              <div className="space-y-1.5">
                                <div className="flex items-center justify-between">
                                  <span className="text-muted-foreground">JavaScript (Goja Sandboxed)</span>
                                  <span className="text-[11px] text-muted-foreground">20ms timeout · return null drops</span>
                                </div>
                                <Textarea
                                  rows={6}
                                  className="font-mono text-xs leading-relaxed"
                                  value={step.source ?? ''}
                                  onChange={(e) => handleUpdateStep(idx, { source: e.target.value })}
                                />
                              </div>
                            )}
                          </div>
                        </div>
                      )
                    })}
                  </div>
                )}
              </TabsContent>

              {/* 2. PREVIEW TAB */}
              <TabsContent value="preview" className="space-y-4 pt-4">
                <div className="flex items-center justify-between">
                  <div className="text-xs text-muted-foreground">
                    Evaluate current steps over the last 500 stored device messages in time order.
                  </div>
                  <Button size="xs" onClick={handleRunPreview} disabled={previewLoading} className="gap-1.5">
                    {previewLoading ? <Spinner className="size-3" /> : <Play className="size-3" />}
                    Run Preview
                  </Button>
                </div>

                {previewData && (
                  <div className="space-y-4">
                    {/* Summary badges */}
                    <div className="grid grid-cols-4 gap-2 rounded-lg border bg-muted/30 p-2.5 text-center text-xs">
                      <div>
                        <div className="text-muted-foreground">Total</div>
                        <div className="text-base font-bold font-mono">{previewData.summary?.total ?? 0}</div>
                      </div>
                      <div>
                        <div className="text-emerald-600 dark:text-emerald-400">Accepted</div>
                        <div className="text-base font-bold font-mono text-emerald-600 dark:text-emerald-400">
                          {previewData.summary?.passed ?? 0}
                        </div>
                      </div>
                      <div>
                        <div className="text-amber-600 dark:text-amber-400">Filtered</div>
                        <div className="text-base font-bold font-mono text-amber-600 dark:text-amber-400">
                          {previewData.summary?.filtered ?? 0}
                        </div>
                      </div>
                      <div>
                        <div className="text-destructive">Failed</div>
                        <div className="text-base font-bold font-mono text-destructive">
                          {previewData.summary?.failed ?? 0}
                        </div>
                      </div>
                    </div>

                    {/* Chart if numeric */}
                    {chartOption && (
                      <div className="h-44 w-full rounded-lg border bg-card p-2">
                        <EChart option={chartOption} />
                      </div>
                    )}

                    {/* Rows table */}
                    <div className="rounded-lg border overflow-hidden">
                      <div className="max-h-80 overflow-y-auto">
                        <table className="w-full text-left text-xs">
                          <thead className="bg-muted/50 sticky top-0 border-b">
                            <tr>
                              <th className="p-2 font-medium">Time</th>
                              <th className="p-2 font-medium">Before</th>
                              <th className="p-2 font-medium">After / Status</th>
                            </tr>
                          </thead>
                          <tbody className="divide-y font-mono">
                            {previewData.rows?.map((row, i) => (
                              <tr
                                key={i}
                                className={
                                  row.error
                                    ? 'bg-destructive/10'
                                    : row.filtered
                                    ? 'bg-amber-500/5 text-muted-foreground'
                                    : ''
                                }
                              >
                                <td className="p-2 text-[11px] text-muted-foreground whitespace-nowrap">
                                  {new Date(row.time).toLocaleTimeString()}
                                </td>
                                <td className="p-2 max-w-[180px] truncate" title={JSON.stringify(row.before)}>
                                  {JSON.stringify(row.before)}
                                </td>
                                <td className="p-2">
                                  {row.error ? (
                                    <Badge variant="destructive" className="text-[10px]">
                                      Error: {row.error}
                                    </Badge>
                                  ) : row.filtered ? (
                                    <Badge variant="outline" className="text-[10px] border-amber-500/40 text-amber-600 dark:text-amber-400">
                                      Filtered ({row.reason ?? 'deadband'})
                                    </Badge>
                                  ) : (
                                    <span className="text-emerald-600 dark:text-emerald-400 font-semibold max-w-[200px] truncate block">
                                      {JSON.stringify(row.after)}
                                    </span>
                                  )}
                                </td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    </div>
                  </div>
                )}
              </TabsContent>

              {/* 3. TEST TAB */}
              <TabsContent value="test" className="space-y-4 pt-4">
                <div className="grid grid-cols-2 gap-4">
                  <div className="space-y-2">
                    <Field label="Test Message (JSON)" htmlFor="test-msg">
                      <Textarea
                        id="test-msg"
                        rows={4}
                        className="font-mono text-xs"
                        value={testInput}
                        onChange={(e) => setTestInput(e.target.value)}
                      />
                    </Field>
                  </div>
                  <div className="space-y-2">
                    <Field label="Previous Message (Optional, for deadband)" htmlFor="test-last">
                      <Textarea
                        id="test-last"
                        rows={4}
                        placeholder='{"value": 99.5}'
                        className="font-mono text-xs"
                        value={testLast}
                        onChange={(e) => setTestLast(e.target.value)}
                      />
                    </Field>
                  </div>
                </div>

                <Button size="xs" onClick={handleRunTest} disabled={testLoading} className="gap-1.5">
                  {testLoading ? <Spinner className="size-3" /> : <FlaskConical className="size-3" />}
                  Run Test
                </Button>

                {testResult && (
                  <div className="grid grid-cols-2 gap-4 pt-2">
                    <div className="rounded-lg border p-3 bg-muted/20 space-y-2">
                      <div className="flex items-center justify-between text-xs font-semibold">
                        <span>Forward Transform</span>
                        {testResult.error ? (
                          <Badge variant="destructive">Error</Badge>
                        ) : testResult.filtered ? (
                          <Badge variant="outline" className="border-amber-500/40 text-amber-600">
                            Filtered
                          </Badge>
                        ) : (
                          <Badge variant="outline" className="border-emerald-500/40 text-emerald-600">
                            Accepted
                          </Badge>
                        )}
                      </div>
                      {testResult.error ? (
                        <div className="text-destructive font-mono text-xs">{testResult.error}</div>
                      ) : testResult.filtered ? (
                        <div className="text-amber-600 font-mono text-xs">
                          Dropped: {testResult.reason ?? 'filtered'}
                        </div>
                      ) : (
                        <pre className="font-mono text-xs text-emerald-600 dark:text-emerald-400 bg-background/50 p-2 rounded overflow-x-auto">
                          {JSON.stringify(testResult.after, null, 2)}
                        </pre>
                      )}
                    </div>

                    <div className="rounded-lg border p-3 bg-muted/20 space-y-2">
                      <div className="flex items-center justify-between text-xs font-semibold">
                        <span>Command Inverse</span>
                        {testResult.inverse !== undefined ? (
                          <Badge variant="outline" className="border-emerald-500/40 text-emerald-600">
                            Invertible
                          </Badge>
                        ) : (
                          <Badge variant="muted">Uplink Only</Badge>
                        )}
                      </div>
                      {testResult.inverse !== undefined ? (
                        <pre className="font-mono text-xs text-primary bg-background/50 p-2 rounded overflow-x-auto">
                          {JSON.stringify(testResult.inverse, null, 2)}
                        </pre>
                      ) : (
                        <div className="text-muted-foreground text-xs italic">
                          This pipeline contains non-invertible steps (e.g. clamp, round, or script without untransform).
                          Commands to this element are sent un-transformed.
                        </div>
                      )}
                    </div>
                  </div>
                )}
              </TabsContent>

              {/* 4. VERSIONS TAB */}
              <TabsContent value="versions" className="space-y-4 pt-4">
                <div className="text-xs text-muted-foreground">
                  Every pipeline save creates an immutable version. You can rollback to any prior version anytime.
                </div>

                {currentVersions.length === 0 ? (
                  <div className="rounded-lg border border-dashed p-6 text-center text-xs text-muted-foreground">
                    No versions recorded yet.
                  </div>
                ) : (
                  <div className="space-y-2">
                    {currentVersions.map((v) => {
                      const isCurrent = v.version === currentPipeline?.version
                      const stepsCount = Array.isArray(v.steps) ? v.steps.length : 0
                      return (
                        <div
                          key={v.version}
                          className="flex items-center justify-between rounded-lg border p-3 text-xs bg-card"
                        >
                          <div className="space-y-1">
                            <div className="flex items-center gap-2">
                              <span className="font-bold font-mono text-sm">v{v.version}</span>
                              {isCurrent && (
                                <Badge variant="secondary" className="text-[10px] font-semibold">
                                  Active
                                </Badge>
                              )}
                              <span className="text-muted-foreground">({stepsCount} steps)</span>
                            </div>
                            <div className="text-[11px] text-muted-foreground">
                              Updated by <span className="font-medium text-foreground">{v.updated_by}</span> ·{' '}
                              <RelativeTime value={v.updated_at} />
                            </div>
                          </div>

                          <div className="flex items-center gap-2">
                            <Button
                              variant="outline"
                              size="xs"
                              onClick={() => {
                                setWorkingSteps(parseStepsFromRaw(v.steps))
                                setActiveTab('steps')
                                toast.info(`Loaded steps from v${v.version} into editor`)
                              }}
                            >
                              Inspect
                            </Button>
                            {!isCurrent && (
                              <Button
                                size="xs"
                                variant="secondary"
                                onClick={() => handleRollback(v.version)}
                                disabled={rollbackMut.isPending}
                                className="gap-1"
                              >
                                <RotateCcw className="size-3" /> Rollback
                              </Button>
                            )}
                          </div>
                        </div>
                      )
                    })}
                  </div>
                )}
              </TabsContent>
            </Tabs>
          </SheetBody>
        </SheetContent>
      </Sheet>
      {confirm.dialog}
    </>
  )
}
