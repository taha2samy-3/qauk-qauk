import { ChevronRight, Globe, LayoutGrid, Lock, Pencil, Plus, Redo2, Save, Undo2, X } from 'lucide-react'
import { useCallback, useEffect, useMemo, useReducer, useState } from 'react'
import { Link, useBlocker, useParams } from 'react-router'
import { toast } from 'sonner'
import { useDevices } from '@/api/devices'
import { ApiError } from '@/api/problem'
import { useDashboard, useMe, useMyElements, useUpdateDashboard } from '@/api/queries'
import type { MyElement } from '@/api/types'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { EmptyState, ErrorState } from '@/components/states'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Kbd } from '@/components/ui/kbd'
import { Sheet, SheetBody, SheetContent, SheetTitle } from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import { Spinner } from '@/components/ui/spinner'
import { Hint } from '@/components/ui/tooltip'
import { toastError } from '@/lib/forms'
import { uid } from '@/lib/utils'
import { useConnectionStatus } from '@/realtime/hooks'
import { getWidget } from '@/widgets/registry'
import type { WidgetDefinition } from '@/widgets/types'
import { AddWidgetDialog } from './AddWidgetDialog'
import { DashboardGrid } from './DashboardGrid'
import { editorReducer, initEditor, layoutKey, type GridItem } from './editorState'
import { migrateLayout, type BreakpointKey, type GridPos, type Widget } from './layout'
import { WidgetConfigSheet } from './WidgetConfigSheet'
import { WidgetPalette } from './WidgetPalette'

const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)

export function DashboardPage() {
  const { id } = useParams<{ id: string }>()
  const me = useMe()
  const dash = useDashboard(id)
  const elementsQ = useMyElements()
  const { byId: devices } = useDevices()
  const update = useUpdateDashboard()
  const { status: socketStatus } = useConnectionStatus()

  const parsed = useMemo(() => migrateLayout(dash.data?.layout), [dash.data?.layout])
  const [baseline, setBaseline] = useState(parsed.layout)
  const [state, dispatch] = useReducer(editorReducer, parsed.layout, initEditor)
  const [editing, setEditing] = useState(false)
  const [configId, setConfigId] = useState<string | null>(null)
  const [addOpen, setAddOpen] = useState(false)
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [discardOpen, setDiscardOpen] = useState(false)

  // Load server state when it changes (and we are not mid-edit).
  const [loadedKey, setLoadedKey] = useState<string>()
  const serverKey = dash.data ? `${dash.data.id}:${dash.data.updated_at}` : undefined
  if (serverKey && serverKey !== loadedKey && !editing) {
    setLoadedKey(serverKey)
    setBaseline(parsed.layout)
    dispatch({ type: 'reset', layout: parsed.layout })
  }
  useEffect(() => {
    if (parsed.dropped > 0) toast.warning(`${parsed.dropped} widget(s) could not be read and were skipped`)
  }, [parsed.dropped])

  const layout = state.present
  const dirty = editing && layoutKey(layout) !== layoutKey(baseline)
  const isOwner = !!dash.data && !!me.data && dash.data.owner_id === me.data.id
  const elements = useMemo(
    () => new Map<string, MyElement>((elementsQ.data ?? []).map((e) => [e.id, e])),
    [elementsQ.data],
  )
  const deviceNames = useMemo(() => new Map([...devices.values()].map((d) => [d.id, d.name])), [devices])

  // ---- unsaved changes guard ----
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) => dirty && currentLocation.pathname !== nextLocation.pathname,
  )
  useEffect(() => {
    if (!dirty) return
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault()
    }
    window.addEventListener('beforeunload', onBeforeUnload)
    return () => window.removeEventListener('beforeunload', onBeforeUnload)
  }, [dirty])

  // ---- actions ----
  const save = useCallback(async () => {
    if (!dash.data) return
    try {
      await update.mutateAsync({ id: dash.data.id, name: dash.data.name, shared: dash.data.shared, layout })
      setBaseline(layout)
      setEditing(false)
      toast.success('Dashboard saved')
    } catch (e) {
      if (e instanceof ApiError && e.status === 403) toast.error('Only the owner can change this dashboard')
      else toastError('Could not save the dashboard', e)
    }
  }, [dash.data, layout, update])

  const discard = () => {
    dispatch({ type: 'reset', layout: baseline })
    setEditing(false)
    setDiscardOpen(false)
  }

  const addForElement = (el: MyElement, def: WidgetDefinition) => {
    const wid = uid()
    dispatch({
      type: 'add',
      widget: { id: wid, type: def.type, element_id: el.id, options: def.defaultOptions(el) },
      size: def.defaultSize,
    })
    toast.success(`${def.label} added`, { description: el.name })
  }

  const addType = (type: string, at?: GridPos, bp?: BreakpointKey) => {
    const def = getWidget(type)
    if (!def) return
    const wid = uid()
    dispatch({
      type: 'add',
      widget: { id: wid, type, element_id: null, options: {} },
      size: def.defaultSize,
      at,
      bp,
    })
    setPaletteOpen(false)
    setConfigId(wid) // choose the element right away
  }

  const onConfigure = useCallback((wid: string) => setConfigId(wid), [])
  const onDuplicate = useCallback((wid: string) => dispatch({ type: 'duplicate', id: wid }), [])
  const onRemove = useCallback((wid: string) => {
    dispatch({ type: 'remove', id: wid })
    toast('Widget removed', { action: { label: 'Undo', onClick: () => dispatch({ type: 'undo' }) } })
  }, [])
  const onMove = useCallback(
    (bp: BreakpointKey, items: GridItem[]) => dispatch({ type: 'move', bp, items }),
    [],
  )
  const onSync = useCallback(
    (bp: BreakpointKey, items: GridItem[]) => dispatch({ type: 'sync', bp, items }),
    [],
  )

  // ---- keyboard shortcuts (edit mode) ----
  useEffect(() => {
    if (!editing) return
    const onKey = (e: KeyboardEvent) => {
      const mod = isMac ? e.metaKey : e.ctrlKey
      if (!mod) return
      const t = e.target as HTMLElement | null
      const typing = t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)
      const k = e.key.toLowerCase()
      if (k === 's') {
        e.preventDefault()
        void save()
      } else if (!typing && k === 'z') {
        e.preventDefault()
        dispatch({ type: e.shiftKey ? 'redo' : 'undo' })
      } else if (!typing && k === 'y') {
        e.preventDefault()
        dispatch({ type: 'redo' })
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [editing, save])

  // ---- render ----
  if (dash.isLoading)
    return (
      <div className="space-y-6 p-4 md:p-8">
        <Skeleton className="h-8 w-64" />
        <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
          {Array.from({ length: 6 }, (_, i) => (
            <Skeleton key={i} className="h-48" />
          ))}
        </div>
      </div>
    )
  if (dash.isError || !dash.data)
    return (
      <div className="p-4 md:p-8">
        {dash.error instanceof ApiError && dash.error.status === 404 ? (
          <EmptyState
            duck="lost"
            title="Dashboard not found"
            description="It may have been deleted, or it is not shared with you."
            action={
              <Button asChild variant="outline">
                <Link to="/dashboards">Back to dashboards</Link>
              </Button>
            }
          />
        ) : (
          <ErrorState error={dash.error} onRetry={() => void dash.refetch()} />
        )}
      </div>
    )

  const d = dash.data
  const configWidget: Widget | null = (configId && layout.widgets.find((w) => w.id === configId)) || null
  const mod = isMac ? '⌘' : 'Ctrl'

  return (
    <div className="flex min-h-full">
      <div className="min-w-0 flex-1 px-4 py-5 md:px-6">
        {/* header */}
        <div className="mb-5 flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
          <div className="min-w-0">
            <nav
              aria-label="Breadcrumb"
              className="text-muted-foreground mb-1 flex items-center gap-1 text-xs"
            >
              <Link to="/dashboards" className="hover:text-foreground hover:underline">
                Dashboards
              </Link>
              <ChevronRight className="size-3" />
            </nav>
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="truncate text-xl font-semibold tracking-tight" data-testid="dashboard-title">
                {d.name}
              </h1>
              {d.shared && (
                <Badge variant="secondary">
                  <Globe /> Shared
                </Badge>
              )}
              {!isOwner && (
                <Badge variant="outline" data-testid="readonly-badge">
                  <Lock /> Read-only · owned by {d.owner_name}
                </Badge>
              )}
              {dirty && <Badge variant="warning">Unsaved changes</Badge>}
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {!editing && isOwner && (
              <Button onClick={() => setEditing(true)} data-testid="edit-dashboard">
                <Pencil /> Edit
              </Button>
            )}
            {editing && (
              <>
                <div className="bg-card flex items-center rounded-md border shadow-xs">
                  <Hint
                    label={
                      <>
                        Undo <Kbd className="text-background/70 ml-1 bg-transparent">{mod}+Z</Kbd>
                      </>
                    }
                  >
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      onClick={() => dispatch({ type: 'undo' })}
                      disabled={!state.past.length}
                      aria-label="Undo"
                      className="rounded-r-none"
                    >
                      <Undo2 />
                    </Button>
                  </Hint>
                  <Hint
                    label={
                      <>
                        Redo <Kbd className="text-background/70 ml-1 bg-transparent">{mod}+Shift+Z</Kbd>
                      </>
                    }
                  >
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      onClick={() => dispatch({ type: 'redo' })}
                      disabled={!state.future.length}
                      aria-label="Redo"
                      className="rounded-l-none"
                    >
                      <Redo2 />
                    </Button>
                  </Hint>
                </div>
                <Button variant="outline" onClick={() => setAddOpen(true)} data-testid="add-widget">
                  <Plus /> Add widget
                </Button>
                <Button variant="outline" className="xl:hidden" onClick={() => setPaletteOpen(true)}>
                  <LayoutGrid /> Palette
                </Button>
                <Button
                  variant="ghost"
                  onClick={() => (dirty ? setDiscardOpen(true) : setEditing(false))}
                  data-testid="discard"
                >
                  <X /> {dirty ? 'Discard' : 'Done'}
                </Button>
                <Button onClick={() => void save()} disabled={update.isPending} data-testid="save-dashboard">
                  {update.isPending ? <Spinner className="text-current" /> : <Save />} Save
                </Button>
              </>
            )}
          </div>
        </div>

        {/* body */}
        {layout.widgets.length === 0 && !editing ? (
          <EmptyState
            duck="builder"
            title="This dashboard is empty"
            description={
              isOwner
                ? 'Add gauges, charts, switches and more. Drag them around to build your layout.'
                : 'The owner has not added any widgets yet.'
            }
            action={
              isOwner && (
                <Button
                  onClick={() => {
                    setEditing(true)
                    setAddOpen(true)
                  }}
                  data-testid="empty-add-widget"
                >
                  <Plus /> Add your first widget
                </Button>
              )
            }
            className="py-24"
          />
        ) : (
          <div className="relative">
            {editing && layout.widgets.length === 0 && (
              <div className="text-muted-foreground pointer-events-none absolute inset-x-0 top-24 z-0 text-center text-sm">
                Drag a widget from the palette, or use{' '}
                <span className="text-foreground font-medium">Add widget</span>.
              </div>
            )}
            <DashboardGrid
              widgets={layout.widgets}
              elements={elements}
              elementsLoaded={!elementsQ.isLoading}
              deviceNames={deviceNames}
              editing={editing}
              socketOpen={socketStatus === 'open'}
              onMove={onMove}
              onSync={onSync}
              onDropType={addType}
              onConfigure={onConfigure}
              onDuplicate={onDuplicate}
              onRemove={onRemove}
            />
          </div>
        )}
      </div>

      {/* palette: docked on wide screens, a sheet otherwise */}
      {editing && (
        <aside
          className="bg-sidebar sticky top-0 hidden h-[calc(100dvh-3.5rem)] w-64 shrink-0 overflow-x-hidden overflow-y-auto border-l p-4 xl:block"
          aria-label="Widget palette"
        >
          <WidgetPalette onPick={(t) => addType(t)} onAddByElement={() => setAddOpen(true)} />
        </aside>
      )}
      <Sheet open={paletteOpen} onOpenChange={setPaletteOpen}>
        <SheetContent className="sm:max-w-xs" aria-describedby={undefined}>
          <SheetTitle className="sr-only">Widget palette</SheetTitle>
          <SheetBody>
            <WidgetPalette
              onPick={(t) => addType(t)}
              onAddByElement={() => {
                setPaletteOpen(false)
                setAddOpen(true)
              }}
            />
          </SheetBody>
        </SheetContent>
      </Sheet>

      <AddWidgetDialog open={addOpen} onOpenChange={setAddOpen} onAdd={addForElement} />
      <WidgetConfigSheet
        widget={configWidget}
        elements={elements}
        onOpenChange={(o) => {
          if (o) return
          // closing the sheet of a brand-new, never-bound widget removes it
          const w = configWidget
          setConfigId(null)
          if (w && !w.element_id) dispatch({ type: 'remove', id: w.id })
        }}
        onApply={(wid, patch) => {
          dispatch({ type: 'update', id: wid, patch })
          setConfigId(null)
        }}
      />
      <ConfirmDialog
        open={discardOpen}
        onOpenChange={setDiscardOpen}
        title="Discard changes?"
        description="Your unsaved changes to this dashboard will be lost."
        confirmLabel="Discard"
        destructive
        onConfirm={discard}
      />
      <ConfirmDialog
        open={blocker.state === 'blocked'}
        onOpenChange={(o) => !o && blocker.state === 'blocked' && blocker.reset()}
        title="Leave without saving?"
        description="You have unsaved changes on this dashboard. If you leave now they will be lost."
        confirmLabel="Leave"
        destructive
        onConfirm={() => blocker.state === 'blocked' && blocker.proceed()}
      />
    </div>
  )
}
