import { useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { api, unwrap } from './client'
import { ApiError } from './problem'
import { normalizeElement, type Dashboard, type Me, type MyElement } from './types'

export const qk = {
  me: ['auth', 'me'] as const,
  myElements: ['me', 'elements'] as const,
  dashboards: ['dashboards'] as const,
  dashboard: (id: string) => ['dashboards', id] as const,
  history: (elementId: string, range: string, field = '') => ['history', elementId, range, field] as const,
  deviceNames: (ids: string[]) => ['device-names', ...ids] as const,
  admin: (resource: string, params?: unknown) =>
    (params === undefined ? ['admin', resource] : ['admin', resource, params]) as readonly unknown[],
}

// ---------- auth ----------

export async function fetchMe(): Promise<Me | null> {
  const res = await api.GET('/api/v1/auth/me')
  if (res.response.status === 401) return null
  return unwrap(Promise.resolve(res))
}

export function useMe() {
  return useQuery({ queryKey: qk.me, queryFn: fetchMe, staleTime: 5 * 60_000, retry: false })
}

export function useLogin() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { username: string; password: string }) =>
      unwrap(api.POST('/api/v1/auth/login', { body })),
    onSuccess: (me) => {
      qc.clear()
      qc.setQueryData(qk.me, me)
    },
  })
}

export function useLogout() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => unwrap(api.POST('/api/v1/auth/logout')),
    onSettled: () => {
      qc.clear()
      qc.setQueryData(qk.me, null)
    },
  })
}

export function useChangePassword() {
  return useMutation({
    mutationFn: (body: { old_password: string; new_password: string }) =>
      unwrap(api.POST('/api/v1/auth/password', { body })),
  })
}

// ---------- elements ----------

export function useMyElements() {
  return useQuery({
    queryKey: qk.myElements,
    queryFn: async (): Promise<MyElement[]> =>
      ((await unwrap(api.GET('/api/v1/me/elements'))) ?? []).map(normalizeElement),
    staleTime: 60_000,
  })
}

// ---------- dashboards ----------

export interface DashboardInput {
  name: string
  shared: boolean
  layout: unknown
}

export function useDashboards() {
  return useQuery({
    queryKey: qk.dashboards,
    queryFn: async () => (await unwrap(api.GET('/api/v1/dashboards'))) ?? [],
  })
}

export function useDashboard(id: string | undefined) {
  return useQuery({
    queryKey: qk.dashboard(id ?? ''),
    queryFn: () => unwrap(api.GET('/api/v1/dashboards/{id}', { params: { path: { id: id! } } })),
    enabled: !!id,
    retry: (n, e) => !(e instanceof ApiError && (e.status === 404 || e.status === 403)) && n < 2,
  })
}

function syncDashboard(qc: QueryClient, d: Dashboard) {
  qc.setQueryData(qk.dashboard(d.id), d)
  qc.setQueryData<Dashboard[]>(qk.dashboards, (list) =>
    list ? (list.some((x) => x.id === d.id) ? list.map((x) => (x.id === d.id ? d : x)) : [...list, d]) : list,
  )
}

export function useCreateDashboard() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: DashboardInput) => unwrap(api.POST('/api/v1/dashboards', { body })),
    onSuccess: (d) => {
      syncDashboard(qc, d)
      void qc.invalidateQueries({ queryKey: qk.dashboards })
    },
  })
}

export function useUpdateDashboard() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }: DashboardInput & { id: string }) =>
      unwrap(api.PUT('/api/v1/dashboards/{id}', { params: { path: { id } }, body })),
    onSuccess: (d) => syncDashboard(qc, d),
  })
}

export function useDeleteDashboard() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => unwrap(api.DELETE('/api/v1/dashboards/{id}', { params: { path: { id } } })),
    onSuccess: (_d, id) => {
      qc.setQueryData<Dashboard[]>(qk.dashboards, (list) => list?.filter((x) => x.id !== id))
      qc.removeQueries({ queryKey: qk.dashboard(id) })
    },
  })
}
