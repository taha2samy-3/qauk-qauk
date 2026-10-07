import { useState, type ReactNode } from 'react'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Spinner } from '@/components/ui/spinner'

export interface ConfirmOptions {
  title: ReactNode
  description?: ReactNode
  confirmLabel?: string
  destructive?: boolean
  onConfirm: () => unknown | Promise<unknown>
}

/** Controlled confirm dialog; keeps itself open (with a spinner) while onConfirm runs. */
export function ConfirmDialog({
  open,
  onOpenChange,
  ...o
}: ConfirmOptions & { open: boolean; onOpenChange: (o: boolean) => void }) {
  const [busy, setBusy] = useState(false)
  return (
    <AlertDialog open={open} onOpenChange={(v) => !busy && onOpenChange(v)}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{o.title}</AlertDialogTitle>
          {o.description && <AlertDialogDescription>{o.description}</AlertDialogDescription>}
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant={o.destructive ? 'destructive' : 'default'}
            disabled={busy}
            onClick={async (e) => {
              e.preventDefault()
              setBusy(true)
              try {
                await o.onConfirm()
                onOpenChange(false)
              } catch {
                /* the caller reports errors */
              } finally {
                setBusy(false)
              }
            }}
          >
            {busy && <Spinner className="text-current" />}
            {o.confirmLabel ?? (o.destructive ? 'Delete' : 'Confirm')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/** Imperative-style helper: const confirm = useConfirm(); confirm({...}); render {confirm.dialog}. */
export function useConfirm() {
  const [state, setState] = useState<ConfirmOptions | null>(null)
  const [open, setOpen] = useState(false)
  const ask = (o: ConfirmOptions) => {
    setState(o)
    setOpen(true)
  }
  const dialog = state ? <ConfirmDialog open={open} onOpenChange={setOpen} {...state} /> : null
  return { ask, dialog }
}
