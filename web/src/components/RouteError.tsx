import { Link, isRouteErrorResponse, useRouteError } from 'react-router'
import { Button } from '@/components/ui/button'
import { EmptyState, ErrorState } from './states'

export function NotFound() {
  return (
    <div className="p-8">
      <EmptyState
        duck="lost"
        title="Page not found"
        description="The page you are looking for does not exist."
        action={
          <Button asChild variant="outline">
            <Link to="/dashboards">Go to dashboards</Link>
          </Button>
        }
      />
    </div>
  )
}

export function RouteError() {
  const err = useRouteError()
  if (isRouteErrorResponse(err) && err.status === 404) return <NotFound />
  return (
    <div className="flex min-h-dvh items-center justify-center p-6">
      <ErrorState
        title="Unexpected error"
        error={err}
        onRetry={() => window.location.reload()}
        className="max-w-lg"
      />
    </div>
  )
}
