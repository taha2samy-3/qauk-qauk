import { useState } from 'react'
import { Link } from 'react-router'
import { Bot, Copy, KeyRound, Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { useConfirm } from '@/components/ConfirmDialog'
import { Page } from '@/components/layout/AppShell'
import { RelativeTime } from '@/components/RelativeTime'
import { EmptyState, ErrorState, PageHeader, TableSkeleton } from '@/components/states'
import { Badge } from '@/components/ui/badge'
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { toastError } from '@/lib/forms'
import { formatDateTime } from '@/lib/time'
import { FormDialog } from '../FormDialog'
import { adminKeys, useAdminMutation } from '../api'
import {
  EXPIRY_DAYS,
  SCOPES,
  expiresAt,
  tokenApi,
  tokenKeys,
  tokenState,
  useMachineUsers,
  type APITokenCreated,
  type MachineUser,
  type TokenScope,
} from './api'

export function ApiTokensPage() {
  const users = useMachineUsers()
  const [newUser, setNewUser] = useState(false)
  const [tokenFor, setTokenFor] = useState<MachineUser | null>(null)
  const [created, setCreated] = useState<APITokenCreated | null>(null)

  return (
    <Page>
      <PageHeader
        title={
          <span className="flex items-center gap-3">
            <img src="/brand/logo-api.svg" alt="" className="size-7 rounded-lg shadow-xs" />
            <span>API tokens</span>
          </span>
        }
        description="Machine users and their tokens, for integrations: your own dashboards, scripts and automations. A token acts as its machine user: it sees and controls only the elements granted to that user in Permissions."
        actions={
          <Button onClick={() => setNewUser(true)}>
            <Plus /> New machine user
          </Button>
        }
      />

      {users.isLoading ? (
        <TableSkeleton rows={3} cols={4} />
      ) : users.error ? (
        <ErrorState error={users.error} onRetry={() => void users.refetch()} />
      ) : !users.data?.length ? (
        <EmptyState
          icon={Bot}
          title="No machine users yet"
          description="Create one for each integration, grant it the elements it needs in Permissions, then give it a token."
          action={
            <Button onClick={() => setNewUser(true)}>
              <Plus /> New machine user
            </Button>
          }
        />
      ) : (
        <div className="grid gap-4">
          {users.data.map((u) => (
            <MachineUserCard key={u.id} user={u} onNewToken={() => setTokenFor(u)} />
          ))}
        </div>
      )}

      <UsageNote />

      {newUser && <MachineUserDialog onOpenChange={setNewUser} />}
      {tokenFor && (
        <TokenDialog
          user={tokenFor}
          onOpenChange={(o) => !o && setTokenFor(null)}
          onCreated={(t) => {
            setTokenFor(null)
            setCreated(t)
          }}
        />
      )}
      <TokenRevealDialog created={created} onClose={() => setCreated(null)} />
    </Page>
  )
}

function MachineUserCard({ user, onNewToken }: { user: MachineUser; onNewToken: () => void }) {
  const confirm = useConfirm()
  const revoke = useAdminMutation(tokenApi.revokeToken, [tokenKeys.machineUsers])
  const del = useAdminMutation(tokenApi.deleteUser, [tokenKeys.machineUsers, adminKeys.users])
  const tokens = user.tokens ?? []

  return (
    <section className="bg-card rounded-xl border" aria-label={`Machine user ${user.username}`}>
      <div className="flex flex-wrap items-center justify-between gap-3 border-b px-4 py-3">
        <div className="flex min-w-0 items-center gap-3">
          <div className="bg-muted flex size-9 items-center justify-center rounded-full">
            <Bot className="size-4.5" />
          </div>
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="font-medium">{user.username}</span>
              {!user.is_active && <Badge variant="muted">Inactive</Badge>}
            </div>
            <div className="text-muted-foreground text-xs">
              {user.email || 'No contact'} · created <RelativeTime value={user.created_at} />
            </div>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" asChild>
            <Link to="/admin/permissions">Permissions</Link>
          </Button>
          <Button size="sm" onClick={onNewToken} disabled={!user.is_active}>
            <Plus /> New token
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`Delete ${user.username}`}
            onClick={() =>
              confirm.ask({
                title: `Delete ${user.username}?`,
                description: 'Its tokens stop working at once, and its permissions are removed.',
                destructive: true,
                onConfirm: async () => {
                  try {
                    await del.mutateAsync(user.id)
                    toast.success(`${user.username} deleted`)
                  } catch (e) {
                    toastError('Could not delete the machine user', e)
                  }
                },
              })
            }
          >
            <Trash2 />
          </Button>
        </div>
      </div>
      {tokens.length === 0 ? (
        <p className="text-muted-foreground px-4 py-5 text-sm">
          No tokens: this account can&apos;t be used yet.
        </p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm" aria-label={`Tokens of ${user.username}`}>
            <thead className="text-muted-foreground text-left text-xs">
              <tr>
                <th className="px-4 py-2 font-medium">Name</th>
                <th className="px-4 py-2 font-medium">Token</th>
                <th className="px-4 py-2 font-medium">Scopes</th>
                <th className="px-4 py-2 font-medium">Expires</th>
                <th className="px-4 py-2 font-medium">Last used</th>
                <th className="px-4 py-2">
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y border-t">
              {tokens.map((t) => {
                const expired = tokenState(t) === 'expired'
                return (
                  <tr key={t.id} className={expired ? 'text-muted-foreground' : ''}>
                    <td className="px-4 py-2.5">
                      <div className="font-medium">{t.name}</div>
                      <div className="text-muted-foreground text-xs">by {t.created_by || '—'}</div>
                    </td>
                    <td className="px-4 py-2.5 font-mono text-xs">{t.prefix}…</td>
                    <td className="px-4 py-2.5">
                      <div className="flex flex-wrap gap-1">
                        {(t.scopes ?? []).map((s) => (
                          <Badge
                            key={s}
                            variant={s === 'control' ? 'warning' : 'secondary'}
                            className="text-[11px]"
                          >
                            {s}
                          </Badge>
                        ))}
                      </div>
                    </td>
                    <td
                      className="px-4 py-2.5 text-xs"
                      title={t.expires_at ? formatDateTime(t.expires_at) : undefined}
                    >
                      {expired ? (
                        <Badge variant="destructive">Expired</Badge>
                      ) : t.expires_at ? (
                        <RelativeTime value={t.expires_at} />
                      ) : (
                        'Never'
                      )}
                    </td>
                    <td className="px-4 py-2.5 text-xs">
                      {t.last_used_at ? <RelativeTime value={t.last_used_at} /> : 'Never'}
                    </td>
                    <td className="px-4 py-2.5 text-right">
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-destructive"
                        aria-label={`Revoke ${t.name}`}
                        onClick={() =>
                          confirm.ask({
                            title: `Revoke ${t.name}?`,
                            description: 'Requests with this token fail from now on. This can’t be undone.',
                            destructive: true,
                            confirmLabel: 'Revoke',
                            onConfirm: async () => {
                              try {
                                await revoke.mutateAsync({ userId: user.id, tokenId: t.id })
                                toast.success(`${t.name} revoked`)
                              } catch (e) {
                                toastError('Could not revoke the token', e)
                              }
                            },
                          })
                        }
                      >
                        Revoke
                      </Button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
      {confirm.dialog}
    </section>
  )
}

function MachineUserDialog({ onOpenChange }: { onOpenChange: (o: boolean) => void }) {
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [error, setError] = useState<string>()
  const create = useAdminMutation(tokenApi.createUser, [tokenKeys.machineUsers, adminKeys.users])

  return (
    <FormDialog
      open
      onOpenChange={onOpenChange}
      title="New machine user"
      description="An account for one integration. It has no password and can't sign in or administer: it uses API tokens, and sees only the elements you grant it."
      submitting={create.isPending}
      formError={error}
      submitLabel="Create"
      onSubmit={async (e) => {
        e.preventDefault()
        if (!name.trim()) return setError('Name is required')
        try {
          const u = await create.mutateAsync({ name: name.trim(), email: email.trim() || undefined })
          toast.success(`${u.username} created: grant it elements in Permissions, then add a token`)
          onOpenChange(false)
        } catch (err) {
          setError(err instanceof Error ? err.message : 'Could not create the machine user')
        }
      }}
    >
      <Field
        label="Name"
        htmlFor="mu-name"
        required
        description="Used as its username, e.g. plant-wallboard."
      >
        <Input id="mu-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
      </Field>
      <Field
        label="Contact email"
        htmlFor="mu-email"
        description="Optional: who to ask about this integration."
      >
        <Input id="mu-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
      </Field>
    </FormDialog>
  )
}

function TokenDialog({
  user,
  onOpenChange,
  onCreated,
}: {
  user: MachineUser
  onOpenChange: (o: boolean) => void
  onCreated: (t: APITokenCreated) => void
}) {
  const [name, setName] = useState('')
  const [scopes, setScopes] = useState<TokenScope[]>(['read'])
  const [days, setDays] = useState('90')
  const [error, setError] = useState<string>()
  const create = useAdminMutation(tokenApi.createToken, [tokenKeys.machineUsers])

  return (
    <FormDialog
      open
      onOpenChange={onOpenChange}
      title={`New token for ${user.username}`}
      description="The token is shown once, right after it is created."
      submitting={create.isPending}
      formError={error}
      submitLabel="Create token"
      onSubmit={async (e) => {
        e.preventDefault()
        if (!name.trim()) return setError('Name is required')
        if (scopes.length === 0) return setError('Pick at least one scope')
        try {
          onCreated(
            await create.mutateAsync({
              userId: user.id,
              name: name.trim(),
              scopes,
              expires_at: expiresAt(Number(days)),
            }),
          )
        } catch (err) {
          setError(err instanceof Error ? err.message : 'Could not create the token')
        }
      }}
    >
      <Field label="Name" htmlFor="tok-name" required description="What uses it, e.g. wallboard or n8n.">
        <Input id="tok-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
      </Field>
      <fieldset className="grid gap-2">
        <legend className="mb-1 text-sm font-medium">Scopes</legend>
        {SCOPES.map((s) => (
          <label key={s.value} className="flex cursor-pointer items-start gap-2.5 text-sm">
            <input
              type="checkbox"
              className="mt-1"
              checked={scopes.includes(s.value)}
              onChange={(e) =>
                setScopes(e.target.checked ? [...scopes, s.value] : scopes.filter((x) => x !== s.value))
              }
            />
            <span>
              <span className="font-medium">{s.label}</span>
              <span className="text-muted-foreground block text-xs">{s.description}</span>
            </span>
          </label>
        ))}
      </fieldset>
      <Field label="Expires" htmlFor="tok-exp">
        <Select value={days} onValueChange={setDays}>
          <SelectTrigger id="tok-exp">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {EXPIRY_DAYS.map((d) => (
              <SelectItem key={d} value={String(d)}>
                {d === 0 ? 'Never' : d === 365 ? 'In 1 year' : `In ${d} days`}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
    </FormDialog>
  )
}

/** The only time a token can be read. */
function TokenRevealDialog({ created, onClose }: { created: APITokenCreated | null; onClose: () => void }) {
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(created?.token ?? '')
      toast.success('Token copied')
    } catch {
      toast.error('Could not copy; select the text and copy it')
    }
  }
  const origin = typeof window === 'undefined' ? '' : window.location.origin
  return (
    <Dialog open={!!created} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <KeyRound className="size-5" /> Token {created?.name}
          </DialogTitle>
          <DialogDescription>
            Copy it now: it isn&apos;t shown again. Keep it like a password; anyone with it acts as this
            machine user.
          </DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-2">
          <code
            className="bg-muted flex-1 rounded-md px-2.5 py-2 font-mono text-xs break-all select-all"
            data-testid="token"
          >
            {created?.token}
          </code>
          <Button
            type="button"
            variant="outline"
            size="icon"
            aria-label="Copy the token"
            onClick={() => void copy()}
          >
            <Copy className="size-4" />
          </Button>
        </div>
        <div className="grid gap-1.5">
          <span className="text-muted-foreground text-xs">Try it:</span>
          <pre className="bg-muted overflow-x-auto rounded-md px-2.5 py-2 font-mono text-[11px]">
            {`curl -H "Authorization: Bearer ${created?.prefix ?? 'qk_'}…" \\\n  ${origin}/api/v1/me/elements`}
          </pre>
        </div>
        <DialogFooter>
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function UsageNote() {
  return (
    <div className="text-muted-foreground mt-6 rounded-lg border border-dashed p-4 text-sm">
      Send the token as <code className="text-foreground">Authorization: Bearer qk_…</code>. Today it reads
      the account&apos;s elements (<code>GET /api/v1/me/elements</code>) and their history (
      <code>GET /api/v1/elements/&#123;id&#125;/history</code>); live values, commands and alerts come with
      the integration API&apos;s next steps. Everything a token does is in the audit log.
    </div>
  )
}
