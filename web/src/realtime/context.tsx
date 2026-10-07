import * as React from 'react'
import { RealtimeClient, defaultSocketUrl } from './client'

const RealtimeContext = React.createContext<RealtimeClient | null>(null)

/**
 * Provides the tab-wide realtime client. The socket is opened while the
 * provider is mounted (i.e. while the user is signed in) and closed on unmount.
 */
export function RealtimeProvider({
  children,
  client,
}: {
  children: React.ReactNode
  client?: RealtimeClient
}) {
  const [rt] = React.useState(() => client ?? new RealtimeClient({ url: defaultSocketUrl }))

  React.useEffect(() => {
    rt.connect()
    const online = () => rt.retryNow()
    const offline = () => rt.markOffline()
    const visible = () => {
      if (document.visibilityState === 'visible') rt.retryNow()
    }
    window.addEventListener('online', online)
    window.addEventListener('offline', offline)
    document.addEventListener('visibilitychange', visible)
    return () => {
      window.removeEventListener('online', online)
      window.removeEventListener('offline', offline)
      document.removeEventListener('visibilitychange', visible)
      rt.close()
    }
  }, [rt])

  return <RealtimeContext.Provider value={rt}>{children}</RealtimeContext.Provider>
}

export function useRealtimeClient(): RealtimeClient {
  const rt = React.useContext(RealtimeContext)
  if (!rt) throw new Error('useRealtimeClient must be used inside <RealtimeProvider>')
  return rt
}
