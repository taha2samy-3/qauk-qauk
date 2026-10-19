import { useQuery } from '@tanstack/react-query'
import { api, unwrap } from '@/api/client'
import { qk } from '@/api/queries'
import type { Schemas } from '@/api/types'

export type TokenScope = 'read' | 'control' | 'alerts'
export type MachineUser = Schemas['MachineUser']
export type APIToken = Schemas['APIToken']
export type APITokenCreated = Schemas['APITokenCreatedOutBody']

export const SCOPES: { value: TokenScope; label: string; description: string }[] = [
  { value: 'read', label: 'Read', description: 'Elements, live values and history the account is granted' },
  { value: 'control', label: 'Control', description: 'Send commands to elements the account is granted RC' },
  { value: 'alerts', label: 'Alerts', description: 'Alert events of elements the account can read' },
]

/** Expiry choices, in days (0 = never). */
export const EXPIRY_DAYS = [0, 30, 90, 365] as const

export function expiresAt(days: number, now = Date.now()): string | undefined {
  return days > 0 ? new Date(now + days * 86_400_000).toISOString() : undefined
}

/** A token's state from its expiry. */
export function tokenState(t: Pick<APIToken, 'expires_at'>, now = Date.now()): 'active' | 'expired' {
  return t.expires_at && Date.parse(t.expires_at) <= now ? 'expired' : 'active'
}

export const tokenKeys = {
  machineUsers: qk.admin('machine-users'),
}

export const tokenApi = {
  list: () => unwrap(api.GET('/api/v1/admin/machine-users')).then((d) => d ?? []),
  createUser: (body: { name: string; email?: string }) =>
    unwrap(api.POST('/api/v1/admin/machine-users', { body })),
  deleteUser: (id: number) => unwrap(api.DELETE('/api/v1/admin/users/{id}', { params: { path: { id } } })),
  createToken: ({
    userId,
    ...body
  }: {
    userId: number
    name: string
    scopes: TokenScope[]
    expires_at?: string
  }) =>
    unwrap(api.POST('/api/v1/admin/machine-users/{id}/tokens', { params: { path: { id: userId } }, body })),
  revokeToken: ({ userId, tokenId }: { userId: number; tokenId: string }) =>
    unwrap(
      api.DELETE('/api/v1/admin/machine-users/{id}/tokens/{tokenId}', {
        params: { path: { id: userId, tokenId } },
      }),
    ),
}

export const useMachineUsers = () => useQuery({ queryKey: tokenKeys.machineUsers, queryFn: tokenApi.list })
