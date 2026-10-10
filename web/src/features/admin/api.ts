import { useMutation, useQuery, useQueryClient, type QueryKey } from '@tanstack/react-query'
import { api, unwrap } from '@/api/client'
import { qk } from '@/api/queries'
import type { Schemas } from '@/api/types'

const list = <T>(p: Promise<{ data?: T[] | null; error?: unknown; response: Response }>) =>
  unwrap(p).then((d) => d ?? [])

export const adminKeys = {
  users: qk.admin('users'),
  groups: qk.admin('groups'),
  members: (groupId: number) => qk.admin('members', groupId),
  keys: qk.admin('keys'),
  devices: qk.admin('devices'),
  elements: (deviceId?: string) => qk.admin('elements', deviceId ?? null),
  styles: (elementId: string) => qk.admin('styles', elementId),
  pipeline: (elementId: string) => qk.admin('pipeline', elementId),
  permissions: (f?: { element_id?: string; user_id?: number; group_id?: number }) =>
    qk.admin('permissions', f ?? {}),
  connections: (f?: { device_id?: string; limit?: number }) => qk.admin('connections', f ?? {}),
  presence: qk.admin('presence'),
  audit: (limit: number) => qk.admin('audit', limit),
}

export const useUsers = () =>
  useQuery({ queryKey: adminKeys.users, queryFn: () => list(api.GET('/api/v1/admin/users')) })
export const useGroups = () =>
  useQuery({ queryKey: adminKeys.groups, queryFn: () => list(api.GET('/api/v1/admin/groups')) })
export const useGroupMembers = (id: number | undefined) =>
  useQuery({
    queryKey: adminKeys.members(id ?? -1),
    queryFn: () => list(api.GET('/api/v1/admin/groups/{id}/members', { params: { path: { id: id! } } })),
    enabled: id !== undefined,
  })
export const useKeys = () =>
  useQuery({ queryKey: adminKeys.keys, queryFn: () => list(api.GET('/api/v1/admin/keys')) })
export const useAdminDevices = () =>
  useQuery({ queryKey: adminKeys.devices, queryFn: () => list(api.GET('/api/v1/admin/devices')) })
export const useAdminElements = (deviceId?: string) =>
  useQuery({
    queryKey: adminKeys.elements(deviceId),
    queryFn: () =>
      list(api.GET('/api/v1/admin/elements', { params: { query: deviceId ? { device_id: deviceId } : {} } })),
  })
export const useStyles = (elementId: string | undefined) =>
  useQuery({
    queryKey: adminKeys.styles(elementId ?? ''),
    queryFn: () =>
      list(api.GET('/api/v1/admin/elements/{id}/styles', { params: { path: { id: elementId! } } })),
    enabled: !!elementId,
  })
export const useElementPipeline = (elementId: string | undefined) =>
  useQuery({
    queryKey: adminKeys.pipeline(elementId ?? ''),
    queryFn: () =>
      unwrap(api.GET('/api/v1/admin/elements/{id}/pipeline', { params: { path: { id: elementId! } } })),
    enabled: !!elementId,
  })
export const usePermissions = (f: { element_id?: string; user_id?: number; group_id?: number }) =>
  useQuery({
    queryKey: adminKeys.permissions(f),
    queryFn: () => list(api.GET('/api/v1/admin/permissions', { params: { query: f } })),
  })
export const useConnections = (f: { device_id?: string; limit?: number }) =>
  useQuery({
    queryKey: adminKeys.connections(f),
    queryFn: () => list(api.GET('/api/v1/admin/connections', { params: { query: f } })),
  })
export const usePresence = (refetchInterval: number | false) =>
  useQuery({
    queryKey: adminKeys.presence,
    queryFn: () => list(api.GET('/api/v1/admin/presence')),
    refetchInterval,
  })
export const useAudit = (limit: number) =>
  useQuery({
    queryKey: adminKeys.audit(limit),
    queryFn: () => list(api.GET('/api/v1/admin/audit', { params: { query: { limit } } })),
  })

/** Mutation that invalidates the given admin query prefixes (and /me/elements) on success. */
export function useAdminMutation<TVars, TData = unknown>(
  fn: (v: TVars) => Promise<TData>,
  invalidate: QueryKey[],
) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: fn,
    onSuccess: async () => {
      await Promise.all([
        ...invalidate.map((queryKey) => qc.invalidateQueries({ queryKey })),
        qc.invalidateQueries({ queryKey: qk.myElements }),
      ])
    },
  })
}

// ---------- typed request helpers ----------
type B = Schemas

export const adminApi = {
  createUser: (body: B['UserCreateInBody']) => unwrap(api.POST('/api/v1/admin/users', { body })),
  updateUser: ({ id, ...body }: B['UserPatchInBody'] & { id: number }) =>
    unwrap(api.PATCH('/api/v1/admin/users/{id}', { params: { path: { id } }, body })),
  deleteUser: (id: number) => unwrap(api.DELETE('/api/v1/admin/users/{id}', { params: { path: { id } } })),

  createGroup: (body: B['GroupInBody']) => unwrap(api.POST('/api/v1/admin/groups', { body })),
  renameGroup: ({ id, name }: { id: number; name: string }) =>
    unwrap(api.PATCH('/api/v1/admin/groups/{id}', { params: { path: { id } }, body: { name } })),
  deleteGroup: (id: number) => unwrap(api.DELETE('/api/v1/admin/groups/{id}', { params: { path: { id } } })),
  addMember: ({ id, user_id }: { id: number; user_id: number }) =>
    unwrap(api.PUT('/api/v1/admin/groups/{id}/members/{user_id}', { params: { path: { id, user_id } } })),
  removeMember: ({ id, user_id }: { id: number; user_id: number }) =>
    unwrap(api.DELETE('/api/v1/admin/groups/{id}/members/{user_id}', { params: { path: { id, user_id } } })),

  createKey: (body: B['KeyCreateInBody']) => unwrap(api.POST('/api/v1/admin/keys', { body })),
  updateKey: ({ id, ...body }: B['KeyPatchInBody'] & { id: string }) =>
    unwrap(api.PATCH('/api/v1/admin/keys/{id}', { params: { path: { id } }, body })),
  deleteKey: (id: string) => unwrap(api.DELETE('/api/v1/admin/keys/{id}', { params: { path: { id } } })),

  createDevice: (body: B['DeviceCreateInBody']) => unwrap(api.POST('/api/v1/admin/devices', { body })),
  updateDevice: ({ id, ...body }: B['DevicePatchInBody'] & { id: string }) =>
    unwrap(api.PATCH('/api/v1/admin/devices/{id}', { params: { path: { id } }, body })),
  deleteDevice: (id: string) =>
    unwrap(api.DELETE('/api/v1/admin/devices/{id}', { params: { path: { id } } })),

  createElement: (body: B['ElementCreateInBody']) => unwrap(api.POST('/api/v1/admin/elements', { body })),
  updateElement: ({ id, ...body }: B['ElementPatchInBody'] & { id: string }) =>
    unwrap(api.PATCH('/api/v1/admin/elements/{id}', { params: { path: { id } }, body })),
  deleteElement: (id: string) =>
    unwrap(api.DELETE('/api/v1/admin/elements/{id}', { params: { path: { id } } })),

  createStyle: ({ elementId, ...body }: B['StyleCreateInBody'] & { elementId: string }) =>
    unwrap(api.POST('/api/v1/admin/elements/{id}/styles', { params: { path: { id: elementId } }, body })),
  updateStyle: ({ id, ...body }: B['StylePatchInBody'] & { id: number }) =>
    unwrap(api.PATCH('/api/v1/admin/styles/{id}', { params: { path: { id } }, body })),
  deleteStyle: (id: number) => unwrap(api.DELETE('/api/v1/admin/styles/{id}', { params: { path: { id } } })),

  setPermission: (body: B['PermissionSetInBody']) => unwrap(api.PUT('/api/v1/admin/permissions', { body })),
  deletePermission: (id: number) =>
    unwrap(api.DELETE('/api/v1/admin/permissions/{id}', { params: { path: { id } } })),
}
