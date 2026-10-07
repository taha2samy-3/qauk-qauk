import { zodResolver } from '@hookform/resolvers/zod'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { Navigate, useNavigate, useSearchParams } from 'react-router'
import { z } from 'zod'
import { ApiError } from '@/api/problem'
import { useLogin, useMe } from '@/api/queries'
import { FormError } from '@/components/FormError'
import { ThemeToggle } from '@/components/layout/ThemeToggle'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { applyProblem } from '@/lib/forms'

const schema = z.object({
  username: z.string().trim().min(1, 'Enter your username').max(150),
  password: z.string().min(1, 'Enter your password').max(1024),
})
type Values = z.infer<typeof schema>

function safeNext(next: string | null): string {
  return next && next.startsWith('/') && !next.startsWith('//') ? next : '/dashboards'
}

export function LoginPage() {
  const me = useMe()
  const login = useLogin()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const [formError, setFormError] = useState<string>()
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { username: '', password: '' },
  })
  const { errors, isSubmitting } = form.formState

  if (me.data) return <Navigate to={safeNext(params.get('next'))} replace />

  const onSubmit = form.handleSubmit(async (v) => {
    setFormError(undefined)
    try {
      await login.mutateAsync(v)
      navigate(safeNext(params.get('next')), { replace: true })
    } catch (e) {
      if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
        setFormError(e.detail || 'Invalid username or password.')
        form.setFocus('password')
      } else setFormError(applyProblem(e, form.setError, ['username', 'password']))
    }
  })

  return (
    <div className="bg-background relative flex min-h-dvh items-center justify-center overflow-hidden px-4">
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 [background:radial-gradient(60rem_40rem_at_50%_-10%,color-mix(in_oklch,var(--brand)_18%,transparent),transparent_70%)]"
      />
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 [background-image:radial-gradient(var(--grid-dots)_1px,transparent_1px)] [mask-image:radial-gradient(ellipse_at_center,black_30%,transparent_75%)] [background-size:20px_20px] opacity-60"
      />
      <div className="absolute top-4 right-4">
        <ThemeToggle />
      </div>
      <main className="relative w-full max-w-sm">
        <div className="mb-8 flex flex-col items-center gap-4 text-center">
          {/* logo.svg switches its text colors with prefers-color-scheme; as an <img> that
              follows the color-scheme we set on <html>, so it tracks the theme toggle too. */}
          <img src="/logo.svg" alt="" width={300} height={64} className="h-14 w-auto" />
          <h1 className="sr-only">Quack Quack</h1>
          <p className="text-muted-foreground text-sm">Sign in to monitor and control your devices</p>
        </div>
        <form
          onSubmit={onSubmit}
          noValidate
          className="bg-card grid gap-4 rounded-xl border p-6 shadow-sm"
          aria-label="Sign in"
        >
          <FormError message={formError} />
          <Field label="Username" htmlFor="username" error={errors.username?.message}>
            <Input
              id="username"
              autoComplete="username"
              autoFocus
              aria-invalid={!!errors.username}
              {...form.register('username')}
            />
          </Field>
          <Field label="Password" htmlFor="password" error={errors.password?.message}>
            <Input
              id="password"
              type="password"
              autoComplete="current-password"
              aria-invalid={!!errors.password}
              {...form.register('password')}
            />
          </Field>
          <Button type="submit" className="mt-1 w-full" disabled={isSubmitting}>
            {isSubmitting && <Spinner className="text-current" />}
            Sign in
          </Button>
        </form>
        <p className="text-muted-foreground mt-6 text-center text-xs">IoT monitoring &amp; control</p>
      </main>
    </div>
  )
}
