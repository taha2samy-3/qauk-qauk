import { useEffect, useMemo, useState } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
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
import type { AdminElement, Schemas } from '@/api/types'
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
import { adminApi, adminKeys, useAdminMutation, useElementPipeline, usePipelineUnits } from './api'
import { STEP_KINDS, newStep, toConfig, toDrafts, type NumberKey, type StepDraft, type StepKind } from './pipelineSteps'

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms)
    return () => clearTimeout(t)
  }, [value, ms])
  return v
}

/** A number in a message: the value itself or its "value" field. */
function numberOf(v: unknown): number | null {
  if (typeof v === 'number') return v
  const inner = v && typeof v === 'object' ? (v as Record<string, unknown>).value : undefined
  return typeof inner === 'number' ? inner : null
}

const json = (v: unknown) => (v === undefined ? '' : JSON.stringify(v))

const BASIS_LABEL: Record<string, string> = {
  raw: 'raw device values',
  reconstructed: 'reconstructed',
  stored: 'stored output',
  mixed: 'mixed',
}

export function PipelineSheet({
  element,
  onOpenChange,
}: {
  element: AdminElement | null
  onOpenChange: (open: boolean) => void
}) {
  // keep the content while the sheet animates closed
  const [shown, setShown] = useState(element)
  if (element && element !== shown) setShown(element)
  const el = element ?? shown

  return (
    <Sheet open={!!element} onOpenChange={onOpenChange}>
      <SheetContent className="sm:max-w-2xl overflow-y-auto">
        {el && <PipelineEditor key={el.id} element={el} />}
      </SheetContent>
    </Sheet>
  )
}

type PreviewResult = Schemas['PipelinePreviewOutBody']
type TestResult = Schemas['PipelineTestOutBody']

function PipelineEditor({ element }: { element: AdminElement }) {
  const theme = useTheme()
  const confirm = useConfirm()
  const pipelineQuery = useElementPipeline(element.id)
  const units = usePipelineUnits()
  const invalidate = [adminKeys.pipeline(element.id), adminKeys.allElements]
  const saveMut = useAdminMutation(adminApi.savePipeline, invalidate)
  const rollbackMut = useAdminMutation(adminApi.rollbackPipeline, invalidate)
  const deleteMut = useAdminMutation(adminApi.deletePipeline, invalidate)

  const [activeTab, setActiveTab] = useState<'steps' | 'preview' | 'test' | 'versions'>('steps')

  // The editor shows the saved steps until edited; null follows the server.
  const current = pipelineQuery.data?.current ?? null
  const versions = pipelineQuery.data?.versions ?? []
  const saved = useMemo(() => toDrafts(current?.steps), [current])
  const [draft, setDraft] = useState<StepDraft[] | null>(null)
  const steps = draft ?? saved
  const built = useMemo(() => steps.map(toConfig), [steps])
  const configs = built.map((b) => b.config)
  const stepsJSON = JSON.stringify(configs)
  const savedJSON = useMemo(() => JSON.stringify(saved.map((s) => toConfig(s).config)), [saved])
  const dirty = stepsJSON !== savedJSON
  const localError = built.find((b) => b.error)?.error

  // the server says whether the steps compile and which apply to commands
  const checkJSON = useDebounced(stepsJSON, 300)
  const validation = useQuery({
    queryKey: adminKeys.pipelineValidate(checkJSON),
    queryFn: () => adminApi.validatePipeline({ steps: JSON.parse(checkJSON) }),
    enabled: !localError,
    placeholderData: keepPreviousData,
    staleTime: Infinity,
  })
  const check = validation.data
  const checkIsCurrent = check !== undefined && checkJSON === stepsJSON && !validation.isPlaceholderData
  const serverError = checkIsCurrent && !check.valid ? check.error : undefined
  const blocked = !!localError || !!serverError

  const [previewLoading, setPreviewLoading] = useState(false)
  const [preview, setPreview] = useState<PreviewResult | null>(null)
  const [testInput, setTestInput] = useState('{\n  "value": 100\n}')
  const [testLast, setTestLast] = useState('')
  const [testLoading, setTestLoading] = useState(false)
  const [testResult, setTestResult] = useState<TestResult | null>(null)

  const edit = (next: StepDraft[]) => setDraft(next)
  const updateStep = (index: number, updates: Partial<StepDraft>) =>
    edit(steps.map((s, i) => (i === index ? { ...s, ...updates } : s)))
  const moveStep = (index: number, dir: -1 | 1) => {
    const target = index + dir
    if (target < 0 || target >= steps.length) return
    const next = [...steps]
    ;[next[index], next[target]] = [next[target]!, next[index]!]
    edit(next)
  }

  const errorText = (err: unknown) => (err instanceof Error ? err.message : String(err))

  const handleSave = async () => {
    if (blocked) return
    try {
      await saveMut.mutateAsync({ elementId: element.id, steps: configs })
      setDraft(null)
      toast.success('Pipeline saved')
    } catch (err) {
      toast.error(`Failed to save the pipeline: ${errorText(err)}`)
    }
  }

  const handleRunPreview = async () => {
    if (blocked) return
    setPreviewLoading(true)
    try {
      setPreview(await adminApi.previewPipeline({ elementId: element.id, steps: configs }))
    } catch (err) {
      toast.error(`Preview failed: ${errorText(err)}`)
    } finally {
      setPreviewLoading(false)
    }
  }

  const handleRunTest = async () => {
    if (blocked) return
    let message: unknown
    try {
      message = JSON.parse(testInput)
    } catch {
      toast.error('The test message isn’t valid JSON')
      return
    }
    let last: unknown = undefined
    if (testLast.trim()) {
      try {
        last = JSON.parse(testLast)
      } catch {
        toast.error('The previous message isn’t valid JSON')
        return
      }
    }
    setTestLoading(true)
    try {
      setTestResult(await adminApi.testPipeline({ elementId: element.id, steps: configs, message, last }))
    } catch (err) {
      toast.error(`Test failed: ${errorText(err)}`)
    } finally {
      setTestLoading(false)
    }
  }

  const handleRollback = (ver: number) =>
    confirm.ask({
      title: `Roll back to version ${ver}?`,
      description: `This saves a new version with the steps of version ${ver}.${dirty ? ' Your unsaved edits are discarded.' : ''}`,
      onConfirm: async () => {
        try {
          await rollbackMut.mutateAsync({ elementId: element.id, version: ver })
          setDraft(null)
          toast.success(`Pipeline rolled back to version ${ver}`)
        } catch (err) {
          toast.error(`Rollback failed: ${errorText(err)}`)
        }
      },
    })

  const handleDeletePipeline = () =>
    confirm.ask({
      title: 'Remove the pipeline?',
      description:
        'This saves an empty version: device values reach the bus as sent, and commands reach the device as written. Older versions stay available for rollback.',
      destructive: true,
      onConfirm: async () => {
        try {
          await deleteMut.mutateAsync(element.id)
          setDraft(null)
          toast.success('Pipeline removed')
        } catch (err) {
          toast.error(`Failed to remove the pipeline: ${errorText(err)}`)
        }
      },
    })

  const showStored = preview?.rows?.some((r) => r.basis !== 'raw') ?? false

  const chartOption = useMemo(() => {
    if (!preview?.rows || preview.rows.length === 0) return null
    const pal = chartPalette(theme.resolved)
    const input: [number, number][] = []
    const output: [number, number][] = []
    for (const r of preview.rows) {
      const t = new Date(r.time).getTime()
      if (isNaN(t)) continue
      const b = numberOf(r.before)
      if (b !== null) input.push([t, b])
      if (!r.filtered && !r.error) {
        const a = numberOf(r.after)
        if (a !== null) output.push([t, a])
      }
    }
    if (input.length === 0 && output.length === 0) return null
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
      legend: { data: ['Input', 'Output'], textStyle: { color: pal.text }, top: 0 },
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
          name: 'Input',
          type: 'line' as const,
          data: input,
          showSymbol: false,
          lineStyle: { width: 1.5, type: 'dashed' as const, color: '#94a3b8' },
          itemStyle: { color: '#94a3b8' },
        },
        {
          name: 'Output',
          type: 'line' as const,
          data: output,
          showSymbol: false,
          lineStyle: { width: 2, color: '#10b981' },
          itemStyle: { color: '#10b981' },
        },
      ],
    }
  }, [preview, theme.resolved])

  const savedSteps = saved.length

  return (
    <>
      <SheetHeader>
        <div className="flex items-center justify-between gap-2">
          <SheetTitle className="flex items-center gap-2.5">
            <img src="/brand/logo-pipeline.svg" alt="" className="size-5 rounded-md shadow-xs" />
            Pipeline · {element.name}
          </SheetTitle>
          <div className="flex items-center gap-1.5">
            {dirty && (
              <Badge variant="warning" className="text-[10px]">
                Unsaved changes
              </Badge>
            )}
            {current && (
              <Badge variant="outline" className="font-mono text-xs">
                v{current.version} · {savedSteps} {savedSteps === 1 ? 'step' : 'steps'}
              </Badge>
            )}
          </div>
        </div>
        <SheetDescription>
          Transforms device values into engineering units before they reach the bus. Steps marked “also applied to
          commands” run in reverse on commands to this element.
        </SheetDescription>
      </SheetHeader>

      <SheetBody className="mt-4 space-y-6">
        {pipelineQuery.isPending ? (
          <div className="flex justify-center p-8">
            <Spinner />
          </div>
        ) : (
          <Tabs value={activeTab} onValueChange={(v) => setActiveTab(v as typeof activeTab)}>
            <TabsList className="grid grid-cols-4 w-full">
              <TabsTrigger value="steps" className="gap-1.5">
                <Workflow className="size-3.5" /> Steps ({steps.length})
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

            {/* 1. STEPS */}
            <TabsContent value="steps" className="space-y-4 pt-4">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex items-center gap-2">
                  <Select value="" onValueChange={(k) => edit([...steps, newStep(k as StepKind)])}>
                    <SelectTrigger className="w-40 h-8 text-xs" aria-label="Add a step">
                      <Plus className="size-3.5" />
                      <SelectValue placeholder="Add step…" />
                    </SelectTrigger>
                    <SelectContent>
                      {STEP_KINDS.map((k) => (
                        <SelectItem key={k.kind} value={k.kind}>
                          {k.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  {dirty && (
                    <Button variant="ghost" size="xs" className="text-muted-foreground" onClick={() => setDraft(null)}>
                      Discard changes
                    </Button>
                  )}
                </div>
                <div className="flex items-center gap-2">
                  {savedSteps > 0 && (
                    <Button
                      variant="ghost"
                      size="xs"
                      className="text-destructive hover:bg-destructive/10"
                      onClick={handleDeletePipeline}
                      disabled={deleteMut.isPending}
                    >
                      Remove pipeline
                    </Button>
                  )}
                  <Button
                    size="xs"
                    onClick={handleSave}
                    disabled={saveMut.isPending || !dirty || blocked}
                    className="gap-1.5 font-medium"
                  >
                    {saveMut.isPending && <Spinner className="size-3" />}
                    Save as v{(current?.version ?? 0) + 1}
                  </Button>
                </div>
              </div>

              {(localError || serverError) && (
                <div role="alert" className="flex items-start gap-1.5 rounded-md bg-destructive/10 px-2.5 py-1.5 text-xs text-destructive">
                  <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
                  <span>{localError ?? serverError}</span>
                </div>
              )}
              {!blocked && checkIsCurrent && check.valid && !check.round_trips && (
                <div className="flex items-start gap-1.5 rounded-md bg-amber-500/10 px-2.5 py-1.5 text-xs text-amber-700 dark:text-amber-400">
                  <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
                  <span>
                    A step changes values but doesn’t apply to commands (a pick, a map with repeated targets, or a
                    script without untransform). Commands skip it, so they may not reach the device in its units.
                  </span>
                </div>
              )}

              {steps.length === 0 ? (
                <div className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">
                  <Workflow className="mx-auto size-8 mb-2 opacity-50" />
                  No steps: device values reach the bus as sent.
                  <div className="mt-3">
                    <Button size="xs" variant="outline" onClick={() => edit([newStep('scale')])}>
                      <Plus className="size-3.5 mr-1" /> Add a scale step
                    </Button>
                  </div>
                </div>
              ) : (
                <div className="space-y-3">
                  {steps.map((step, idx) => (
                    <StepCard
                      key={step.id}
                      step={step}
                      index={idx}
                      count={steps.length}
                      error={built[idx]?.error}
                      invertible={check?.steps_invertible?.[idx]}
                      units={units.data ?? []}
                      onChange={(u) => updateStep(idx, u)}
                      onMove={(dir) => moveStep(idx, dir)}
                      onRemove={() => edit(steps.filter((_, i) => i !== idx))}
                    />
                  ))}
                </div>
              )}
              <datalist id="pipeline-units">
                {(units.data ?? []).flatMap((u) =>
                  [u.name, ...(u.aliases ?? [])].map((n) => (
                    <option key={n} value={n}>
                      {u.dimension}
                    </option>
                  )),
                )}
              </datalist>
            </TabsContent>

            {/* 2. PREVIEW */}
            <TabsContent value="preview" className="space-y-4 pt-4">
              <div className="flex items-center justify-between gap-2">
                <div className="text-xs text-muted-foreground">
                  Runs the steps in the editor over the last 500 stored messages, in time order.
                </div>
                <Button size="xs" onClick={handleRunPreview} disabled={previewLoading || blocked} className="gap-1.5">
                  {previewLoading ? <Spinner className="size-3" /> : <Play className="size-3" />}
                  Run Preview
                </Button>
              </div>

              {preview && (
                <div className="space-y-4">
                  <div className="flex items-start gap-2 rounded-md border bg-muted/30 px-2.5 py-1.5 text-xs">
                    <Badge variant="outline" className="shrink-0 text-[10px]">
                      Input: {BASIS_LABEL[preview.basis] ?? preview.basis}
                    </Badge>
                    <span className="text-muted-foreground">{preview.note}</span>
                  </div>

                  <div className="grid grid-cols-4 gap-2 rounded-lg border bg-muted/30 p-2.5 text-center text-xs">
                    <div>
                      <div className="text-muted-foreground">Total</div>
                      <div className="text-base font-bold font-mono">{preview.summary.total}</div>
                    </div>
                    <div>
                      <div className="text-emerald-600 dark:text-emerald-400">Accepted</div>
                      <div className="text-base font-bold font-mono text-emerald-600 dark:text-emerald-400">
                        {preview.summary.passed}
                      </div>
                    </div>
                    <div>
                      <div className="text-amber-600 dark:text-amber-400">Filtered</div>
                      <div className="text-base font-bold font-mono text-amber-600 dark:text-amber-400">
                        {preview.summary.filtered}
                      </div>
                    </div>
                    <div>
                      <div className="text-destructive">Failed</div>
                      <div className="text-base font-bold font-mono text-destructive">{preview.summary.failed}</div>
                    </div>
                  </div>

                  {chartOption && (
                    <div className="h-44 w-full rounded-lg border bg-card p-2">
                      <EChart option={chartOption} />
                    </div>
                  )}

                  <div className="rounded-lg border overflow-hidden">
                    <div className="max-h-80 overflow-y-auto">
                      <table className="w-full text-left text-xs">
                        <thead className="bg-muted/50 sticky top-0 border-b">
                          <tr>
                            <th className="p-2 font-medium">Time</th>
                            {showStored && <th className="p-2 font-medium">Stored</th>}
                            <th className="p-2 font-medium">Input</th>
                            <th className="p-2 font-medium">Output / Status</th>
                          </tr>
                        </thead>
                        <tbody className="divide-y font-mono">
                          {preview.rows?.map((row, i) => (
                            <tr
                              key={i}
                              className={
                                row.error ? 'bg-destructive/10' : row.filtered ? 'bg-amber-500/5 text-muted-foreground' : ''
                              }
                            >
                              <td className="p-2 text-[11px] text-muted-foreground whitespace-nowrap">
                                {new Date(row.time).toLocaleTimeString()}
                              </td>
                              {showStored && (
                                <td className="p-2 max-w-[140px] truncate text-muted-foreground" title={json(row.stored)}>
                                  {json(row.stored)}
                                </td>
                              )}
                              <td className="p-2 max-w-[160px]" title={json(row.before)}>
                                <span className="block truncate">{row.before === undefined ? '—' : json(row.before)}</span>
                                {preview.basis === 'mixed' && (
                                  <span className="text-[10px] text-muted-foreground">{BASIS_LABEL[row.basis]}</span>
                                )}
                              </td>
                              <td className="p-2">
                                {row.error ? (
                                  <Badge variant="destructive" className="text-[10px] whitespace-normal">
                                    Error: {row.error}
                                  </Badge>
                                ) : row.filtered ? (
                                  <Badge
                                    variant="outline"
                                    className="text-[10px] border-amber-500/40 text-amber-600 dark:text-amber-400"
                                  >
                                    Filtered ({row.reason ?? 'filtered'})
                                  </Badge>
                                ) : (
                                  <span className="text-emerald-600 dark:text-emerald-400 font-semibold max-w-[200px] truncate block">
                                    {json(row.after)}
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

            {/* 3. TEST */}
            <TabsContent value="test" className="space-y-4 pt-4">
              <div className="grid grid-cols-2 gap-4">
                <Field label="Test message (JSON)" htmlFor="test-msg">
                  <Textarea
                    id="test-msg"
                    rows={4}
                    className="font-mono text-xs"
                    value={testInput}
                    onChange={(e) => setTestInput(e.target.value)}
                  />
                </Field>
                <Field label="Previous message (optional, for deadband and scripts)" htmlFor="test-last">
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

              <Button size="xs" onClick={handleRunTest} disabled={testLoading || blocked} className="gap-1.5">
                {testLoading ? <Spinner className="size-3" /> : <FlaskConical className="size-3" />}
                Run Test
              </Button>

              {testResult && (
                <div className="grid grid-cols-2 gap-4 pt-2">
                  <div className="rounded-lg border p-3 bg-muted/20 space-y-2">
                    <div className="flex items-center justify-between text-xs font-semibold">
                      <span>Device → bus</span>
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
                      <div className="text-amber-600 font-mono text-xs">Dropped: {testResult.reason ?? 'filtered'}</div>
                    ) : (
                      <pre className="font-mono text-xs text-emerald-600 dark:text-emerald-400 bg-background/50 p-2 rounded overflow-x-auto">
                        {JSON.stringify(testResult.after, null, 2)}
                      </pre>
                    )}
                  </div>

                  <div className="rounded-lg border p-3 bg-muted/20 space-y-2">
                    <div className="flex items-center justify-between text-xs font-semibold">
                      <span>That output as a command</span>
                      {!testResult.invertible ? (
                        <Badge variant="muted">Uplink only</Badge>
                      ) : testResult.inverse_error ? (
                        <Badge variant="destructive">Refused</Badge>
                      ) : testResult.inverse !== undefined ? (
                        <Badge variant="outline" className="border-emerald-500/40 text-emerald-600">
                          Converted
                        </Badge>
                      ) : null}
                    </div>
                    {testResult.inverse_error ? (
                      <div className="text-destructive font-mono text-xs">
                        The command would not be delivered: {testResult.inverse_error}
                      </div>
                    ) : testResult.after === undefined ? (
                      <div className="text-muted-foreground text-xs italic">The message was dropped: nothing to send back.</div>
                    ) : (
                      <>
                        <pre className="font-mono text-xs text-primary bg-background/50 p-2 rounded overflow-x-auto">
                          {JSON.stringify(testResult.inverse, null, 2)}
                        </pre>
                        {!testResult.invertible && (
                          <div className="text-muted-foreground text-xs italic">
                            No step applies to commands, so commands reach the device as written.
                          </div>
                        )}
                      </>
                    )}
                  </div>
                </div>
              )}
            </TabsContent>

            {/* 4. VERSIONS */}
            <TabsContent value="versions" className="space-y-4 pt-4">
              <div className="text-xs text-muted-foreground">
                Every save, rollback or removal is a new version; older versions stay available.
              </div>

              {versions.length === 0 ? (
                <div className="rounded-lg border border-dashed p-6 text-center text-xs text-muted-foreground">
                  No versions yet.
                </div>
              ) : (
                <div className="space-y-2">
                  {versions.map((v) => {
                    const isCurrent = v.version === current?.version
                    const stepsCount = Array.isArray(v.steps) ? v.steps.length : 0
                    return (
                      <div key={v.version} className="flex items-center justify-between rounded-lg border p-3 text-xs bg-card">
                        <div className="space-y-1">
                          <div className="flex items-center gap-2">
                            <span className="font-bold font-mono text-sm">v{v.version}</span>
                            {isCurrent && (
                              <Badge variant="secondary" className="text-[10px] font-semibold">
                                Active
                              </Badge>
                            )}
                            <span className="text-muted-foreground">
                              ({stepsCount} {stepsCount === 1 ? 'step' : 'steps'})
                            </span>
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
                              edit(toDrafts(v.steps))
                              setActiveTab('steps')
                              toast.info(`Loaded the steps of v${v.version} into the editor (not saved)`)
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
        )}
      </SheetBody>
      {confirm.dialog}
    </>
  )
}

function StepCard({
  step,
  index,
  count,
  error,
  invertible,
  units,
  onChange,
  onMove,
  onRemove,
}: {
  step: StepDraft
  index: number
  count: number
  error?: string
  /** From the server's validation; undefined while unknown */
  invertible?: boolean
  units: Schemas['UnitInfo'][]
  onChange: (u: Partial<StepDraft>) => void
  onMove: (dir: -1 | 1) => void
  onRemove: () => void
}) {
  const id = `step-${step.id}`
  const numberInput = (key: NumberKey, label: string, placeholder?: string) => (
    <div>
      <label htmlFor={`${id}-${key}`} className="text-muted-foreground block text-[11px] mb-1">
        {label}
      </label>
      <Input
        id={`${id}-${key}`}
        inputMode="decimal"
        placeholder={placeholder}
        value={step[key]}
        className="h-7 text-xs font-mono"
        onChange={(e) => onChange({ [key]: e.target.value })}
      />
    </div>
  )
  const unitDimension = (name: string) =>
    units.find((u) => u.name === name || (u.aliases ?? []).includes(name))?.dimension

  return (
    <div
      className={`rounded-lg border bg-card/50 p-3.5 text-card-foreground shadow-xs transition-colors hover:border-foreground/20 ${
        error ? 'border-destructive/50' : ''
      }`}
    >
      <div className="flex items-center justify-between pb-2 mb-2 border-b">
        <div className="flex items-center gap-2">
          <span className="flex size-5 items-center justify-center rounded-full bg-muted font-mono text-[11px] font-semibold text-muted-foreground">
            {index + 1}
          </span>
          <Badge variant="outline" className="font-mono text-xs uppercase font-bold tracking-wider">
            {step.kind}
          </Badge>
          {invertible === true && (
            <Badge variant="outline" className="border-emerald-500/40 text-emerald-600 dark:text-emerald-400 text-[10px]">
              also applied to commands
            </Badge>
          )}
          {invertible === false && <span className="text-[11px] text-muted-foreground">uplink only</span>}
        </div>
        <div className="flex items-center gap-1">
          <Button variant="ghost" size="icon-xs" disabled={index === 0} onClick={() => onMove(-1)} aria-label="Move step up">
            <ChevronUp className="size-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            disabled={index === count - 1}
            onClick={() => onMove(1)}
            aria-label="Move step down"
          >
            <ChevronDown className="size-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            className="text-muted-foreground hover:text-destructive"
            onClick={onRemove}
            aria-label="Remove step"
          >
            <Trash2 className="size-3.5" />
          </Button>
        </div>
      </div>

      <div className="grid gap-3 pt-1 text-xs">
        {step.kind !== 'pick' && step.kind !== 'script' && (
          <div className="grid grid-cols-4 items-center gap-2">
            <label htmlFor={`${id}-field`} className="text-muted-foreground">
              Field
            </label>
            <Input
              id={`${id}-field`}
              value={step.field}
              placeholder="value"
              className="col-span-3 h-7 text-xs font-mono"
              onChange={(e) => onChange({ field: e.target.value })}
            />
          </div>
        )}

        {step.kind === 'pick' && (
          <div className="grid grid-cols-4 items-center gap-2">
            <label htmlFor={`${id}-path`} className="text-muted-foreground">
              Path
            </label>
            <Input
              id={`${id}-path`}
              value={step.path}
              placeholder="e.g. data.temperature"
              className="col-span-3 h-7 text-xs font-mono"
              onChange={(e) => onChange({ path: e.target.value })}
            />
          </div>
        )}

        {step.kind === 'scale' && (
          <div className="grid grid-cols-2 gap-3">
            {numberInput('mul', 'Multiplier', '1')}
            {numberInput('add', 'Offset (add)', '0')}
          </div>
        )}

        {step.kind === 'unit' && (
          <div className="grid grid-cols-2 gap-3">
            {(['from', 'to'] as const).map((k) => (
              <div key={k}>
                <label htmlFor={`${id}-${k}`} className="text-muted-foreground block text-[11px] mb-1">
                  {k === 'from' ? 'From unit' : 'To unit'}
                  {unitDimension(step[k]) && <span className="ml-1 opacity-70">({unitDimension(step[k])})</span>}
                </label>
                <Input
                  id={`${id}-${k}`}
                  list="pipeline-units"
                  value={step[k]}
                  placeholder={k === 'from' ? 'degC' : 'degF'}
                  className="h-7 text-xs font-mono"
                  onChange={(e) => onChange({ [k]: e.target.value })}
                />
              </div>
            ))}
          </div>
        )}

        {step.kind === 'round' && <div className="grid grid-cols-2 gap-3">{numberInput('decimals', 'Decimals (0–6)', '0')}</div>}

        {step.kind === 'clamp' && (
          <div className="grid grid-cols-2 gap-3">
            {numberInput('min', 'Minimum', 'e.g. 0')}
            {numberInput('max', 'Maximum', 'e.g. 100')}
          </div>
        )}

        {step.kind === 'map' && (
          <div className="grid gap-2">
            <div>
              <label htmlFor={`${id}-table`} className="text-muted-foreground block text-[11px] mb-1">
                Mapping table (JSON object: device value → value)
              </label>
              <Textarea
                id={`${id}-table`}
                rows={3}
                className="font-mono text-xs"
                value={step.table}
                onChange={(e) => onChange({ table: e.target.value })}
              />
            </div>
            <div>
              <label htmlFor={`${id}-default`} className="text-muted-foreground block text-[11px] mb-1">
                Default (JSON; without one, a value not in the table fails the message)
              </label>
              <Input
                id={`${id}-default`}
                value={step.default}
                placeholder='e.g. "UNKNOWN"'
                className="h-7 text-xs font-mono"
                onChange={(e) => onChange({ default: e.target.value })}
              />
            </div>
          </div>
        )}

        {step.kind === 'deadband' && (
          <div className="grid grid-cols-3 gap-2">
            {numberInput('abs', 'Absolute delta', 'e.g. 0.5')}
            {numberInput('pct', 'Percent (%)', 'e.g. 5')}
            <div>
              <label htmlFor={`${id}-silence`} className="text-muted-foreground block text-[11px] mb-1">
                Max silence
              </label>
              <Input
                id={`${id}-silence`}
                placeholder="10m"
                value={step.max_silence}
                className="h-7 text-xs font-mono"
                onChange={(e) => onChange({ max_silence: e.target.value })}
              />
            </div>
          </div>
        )}

        {step.kind === 'drop_if' && (
          <div className="grid grid-cols-3 gap-2">
            <div>
              <span className="text-muted-foreground block text-[11px] mb-1">Operator</span>
              <Select value={step.op} onValueChange={(op) => onChange({ op })}>
                <SelectTrigger className="h-7 text-xs font-mono" aria-label="Operator">
                  <SelectValue placeholder="op" />
                </SelectTrigger>
                <SelectContent>
                  {['<', '<=', '>', '>=', '==', '!='].map((op) => (
                    <SelectItem key={op} value={op}>
                      {op}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="col-span-2">{numberInput('value', 'Number (non-numbers always pass)', 'e.g. 0')}</div>
          </div>
        )}

        {step.kind === 'script' && (
          <div className="space-y-1.5">
            <div className="flex items-center justify-between">
              <label htmlFor={`${id}-source`} className="text-muted-foreground">
                JavaScript (sandboxed)
              </label>
              <span className="text-[11px] text-muted-foreground">20 ms limit · return null drops</span>
            </div>
            <Textarea
              id={`${id}-source`}
              rows={6}
              className="font-mono text-xs leading-relaxed"
              value={step.source}
              onChange={(e) => onChange({ source: e.target.value })}
            />
          </div>
        )}

        {error && <div className="text-destructive text-[11px]">{error}</div>}
      </div>
    </div>
  )
}
