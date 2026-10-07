import { KeyRound, LogOut, ShieldCheck } from 'lucide-react'
import { useState } from 'react'
import { useNavigate } from 'react-router'
import { useLogout, useMe } from '@/api/queries'
import { Badge } from '@/components/ui/badge'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { toastError } from '@/lib/forms'
import { ChangePasswordDialog } from './ChangePasswordDialog'

export function Avatar({ name }: { name: string }) {
  return (
    <span className="from-brand to-brand-2 text-brand-foreground flex size-7 items-center justify-center rounded-full bg-gradient-to-br text-xs font-semibold uppercase">
      {name.slice(0, 2)}
    </span>
  )
}

export function UserMenu() {
  const { data: me } = useMe()
  const logout = useLogout()
  const navigate = useNavigate()
  const [pwOpen, setPwOpen] = useState(false)
  if (!me) return null
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            aria-label="User menu"
            data-testid="user-menu"
            className="hover:bg-accent focus-visible:ring-ring/50 flex items-center gap-2 rounded-full p-0.5 pr-2 text-sm focus-visible:ring-[3px] focus-visible:outline-none"
          >
            <Avatar name={me.username} />
            <span className="hidden max-w-[10rem] truncate font-medium md:inline">{me.username}</span>
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-60">
          <DropdownMenuLabel className="flex flex-col gap-1">
            <span className="flex items-center gap-2">
              {me.username}
              {me.is_admin && (
                <Badge variant="secondary">
                  <ShieldCheck /> Admin
                </Badge>
              )}
            </span>
            <span className="text-muted-foreground text-xs font-normal">
              {me.email || 'No email'}
              {me.groups?.length ? ` · ${me.groups.map((g) => g.name).join(', ')}` : ''}
            </span>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={() => setPwOpen(true)}>
            <KeyRound /> Change password
          </DropdownMenuItem>
          <DropdownMenuItem
            variant="destructive"
            onSelect={() =>
              logout.mutate(undefined, {
                onSuccess: () => navigate('/login', { replace: true }),
                onError: (e) => toastError('Sign out failed', e),
              })
            }
          >
            <LogOut /> Sign out
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <ChangePasswordDialog open={pwOpen} onOpenChange={setPwOpen} />
    </>
  )
}
