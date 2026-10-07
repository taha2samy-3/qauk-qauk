import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'
import { toast } from 'sonner'
import { z } from 'zod'
import { useState } from 'react'
import { useChangePassword } from '@/api/queries'
import { FormError } from '@/components/FormError'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { applyProblem } from '@/lib/forms'

const schema = z
  .object({
    old_password: z.string().min(1, 'Enter your current password'),
    new_password: z.string().min(8, 'At least 8 characters').max(1024),
    confirm: z.string(),
  })
  .refine((v) => v.new_password === v.confirm, { path: ['confirm'], message: 'Passwords do not match' })
  .refine((v) => v.new_password !== v.old_password, {
    path: ['new_password'],
    message: 'Choose a different password',
  })
type Values = z.infer<typeof schema>

export function ChangePasswordDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const mut = useChangePassword()
  const [formError, setFormError] = useState<string>()
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { old_password: '', new_password: '', confirm: '' },
  })
  const { errors, isSubmitting } = form.formState

  const close = (o: boolean) => {
    if (!o) {
      form.reset()
      setFormError(undefined)
    }
    onOpenChange(o)
  }

  const onSubmit = form.handleSubmit(async ({ old_password, new_password }) => {
    setFormError(undefined)
    try {
      await mut.mutateAsync({ old_password, new_password })
      toast.success('Password changed')
      close(false)
    } catch (e) {
      setFormError(applyProblem(e, form.setError, ['old_password', 'new_password']))
    }
  })

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Change password</DialogTitle>
          <DialogDescription>Use at least 8 characters. Other sessions stay signed in.</DialogDescription>
        </DialogHeader>
        <form onSubmit={onSubmit} className="grid gap-4" noValidate>
          <FormError message={formError} />
          <Field label="Current password" htmlFor="old_password" error={errors.old_password?.message}>
            <Input
              id="old_password"
              type="password"
              autoComplete="current-password"
              aria-invalid={!!errors.old_password}
              {...form.register('old_password')}
            />
          </Field>
          <Field label="New password" htmlFor="new_password" error={errors.new_password?.message}>
            <Input
              id="new_password"
              type="password"
              autoComplete="new-password"
              aria-invalid={!!errors.new_password}
              {...form.register('new_password')}
            />
          </Field>
          <Field label="Confirm new password" htmlFor="confirm" error={errors.confirm?.message}>
            <Input
              id="confirm"
              type="password"
              autoComplete="new-password"
              aria-invalid={!!errors.confirm}
              {...form.register('confirm')}
            />
          </Field>
          <DialogFooter>
            <Button variant="outline" onClick={() => close(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={isSubmitting}>
              {isSubmitting && <Spinner className="text-current" />} Change password
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
