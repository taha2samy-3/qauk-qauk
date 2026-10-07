import { useEffect } from 'react'
import { Navigate, Outlet, useLocation } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { onUnauthorized } from '@/api/client'
import { qk, useMe } from '@/api/queries'
import { EmptyState, ErrorState } from '@/components/states'
import { LogoMark } from '@/components/layout/Logo'
import { Spinner } from '@/components/ui/spinner'
import { RealtimeProvider } from '@/realtime/context'
import { Button } from '@/components/ui/button'
import { Link } from 'react-router'

export function Splash() {
  return (
    <div className="flex h-dvh flex-col items-center justify-center gap-4">
      <LogoMark className="size-10" />
      <Spinner />
    </div>
  )
}

/** Gate for signed-in routes. Also owns the realtime socket for the session. */
export function RequireAuth() {
  const me = useMe()
  const location = useLocation()
  const qc = useQueryClient()

  useEffect(
    () =>
      onUnauthorized(() => {
        if (qc.getQueryData(qk.me))
          toast.error('Your session has expired', { description: 'Please sign in again.' })
        qc.setQueryData(qk.me, null)
      }),
    [qc],
  )

  if (me.isLoading) return <Splash />
  if (me.isError)
    return (
      <div className="flex h-dvh items-center justify-center p-6">
        <ErrorState
          title="Cannot reach Quack Quack"
          duck="sleeping"
          error={me.error}
          onRetry={() => void me.refetch()}
          className="max-w-md"
        />
      </div>
    )
  if (!me.data) {
    const next = location.pathname + location.search
    return (
      <Navigate to={next && next !== '/' ? `/login?next=${encodeURIComponent(next)}` : '/login'} replace />
    )
  }
  return (
    <RealtimeProvider key={me.data.id}>
      <Outlet />
    </RealtimeProvider>
  )
}

export function RequireAdmin() {
  const { data: me } = useMe()
  if (!me?.is_admin)
    return (
      <div className="p-8">
        <EmptyState
          duck="guardian"
          title="Administrators only"
          description="You need administrator rights to view this page."
          action={
            <Button asChild variant="outline">
              <Link to="/dashboards">Back to dashboards</Link>
            </Button>
          }
        />
      </div>
    )
  return <Outlet />
}
