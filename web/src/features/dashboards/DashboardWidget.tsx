import { Bug, HelpCircle, RefreshCw } from 'lucide-react'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import { Button } from '@/components/ui/button'
import { memo, useCallback, useState } from 'react'
import type { MyElement } from '@/api/types'
import { useElement } from '@/realtime/hooks'
import { getWidget } from '@/widgets/registry'
import type { Options } from '@/widgets/types'
import type { Widget } from './layout'
import { WidgetFrame, WidgetMessage, WidgetStates } from './WidgetFrame'

interface Props {
  widget: Widget
  element: MyElement | undefined
  elementsLoaded: boolean
  deviceName?: string
  editing: boolean
  socketOpen: boolean
  onConfigure: (id: string) => void
  onDuplicate: (id: string) => void
  onRemove: (id: string) => void
}

/** Binds one layout widget to its element's live state and renders it in a frame. */
export const DashboardWidget = memo(function DashboardWidget({
  widget,
  element,
  elementsLoaded,
  deviceName,
  editing,
  socketOpen,
  onConfigure,
  onDuplicate,
  onRemove,
}: Props) {
  const def = getWidget(widget.type)
  // Only subscribe to elements the user can read (others would just error).
  const rt = useElement(element ? element.id : null)
  const [local, setLocalState] = useState<Options>({})
  const setLocal = useCallback((patch: Options) => setLocalState((s) => ({ ...s, ...patch })), [])

  const title = widget.title || element?.name || def?.label || 'Widget'
  const permission = rt.permission ?? element?.permission
  const configure = () => onConfigure(widget.id)

  let body: React.ReactNode
  let ready = false
  if (!def)
    body = (
      <WidgetMessage
        icon={HelpCircle}
        title="Unknown widget type"
        description={`“${widget.type}” is not available in this version.`}
      />
    )
  else if (!widget.element_id) body = WidgetStates.unbound(editing ? configure : undefined)
  else if (!element) body = elementsLoaded ? WidgetStates.unavailable() : WidgetStates.loading()
  else if (rt.status === 'revoked') body = WidgetStates.revoked(rt.reason, rt.retry)
  else if (rt.status === 'error') body = WidgetStates.error(rt.error?.description, rt.retry)
  else if (rt.status !== 'subscribed' && rt.message === undefined) body = WidgetStates.loading()
  else {
    ready = true
    body = (
      <ErrorBoundary
        resetKey={`${widget.type}:${JSON.stringify(widget.options)}`}
        fallback={(err, reset) => (
          <WidgetMessage
            icon={Bug}
            tone="error"
            title="This widget crashed"
            description={err.message}
            action={
              <Button size="xs" variant="outline" onClick={reset}>
                <RefreshCw /> Reload widget
              </Button>
            }
          />
        )}
      >
        <def.Component
          widget={widget}
          element={element}
          options={widget.options}
          rt={rt}
          editing={editing}
          local={local}
          setLocal={setLocal}
        />
      </ErrorBoundary>
    )
  }

  const Actions = ready && def?.Actions && element ? def.Actions : null

  return (
    <WidgetFrame
      id={widget.id}
      type={widget.type}
      title={title}
      subtitle={element ? (deviceName ? `${deviceName} · ${element.name}` : element.name) : undefined}
      editing={editing}
      readOnly={!!element && permission === 'R'}
      offline={!!element && rt.deviceConnected === false}
      stale={!!element && !socketOpen && rt.message !== undefined}
      lastEditAt={rt.lastEditAt}
      lastEditBy={rt.lastEditBy}
      actions={
        Actions && element ? (
          <Actions
            widget={widget}
            element={element}
            options={widget.options}
            rt={rt}
            editing={editing}
            local={local}
            setLocal={setLocal}
          />
        ) : null
      }
      onConfigure={configure}
      onDuplicate={() => onDuplicate(widget.id)}
      onRemove={() => onRemove(widget.id)}
    >
      {body}
    </WidgetFrame>
  )
})
