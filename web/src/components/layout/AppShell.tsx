import { Menu, PanelLeftClose, PanelLeftOpen } from 'lucide-react'
import { useState } from 'react'
import { NavLink, Outlet } from 'react-router'
import { useMe } from '@/api/queries'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetTitle } from '@/components/ui/sheet'
import { Hint } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'
import { ConnectionIndicator } from './ConnectionIndicator'
import { Logo } from './Logo'
import { ADMIN_NAV, MAIN_NAV, type NavItem } from './nav'
import { ThemeToggle } from './ThemeToggle'
import { UserMenu } from './UserMenu'

const COLLAPSE_KEY = 'quack-sidebar-collapsed'

function readCollapsed(): boolean {
  try {
    const v = localStorage.getItem(COLLAPSE_KEY)
    if (v !== null) return v === '1'
  } catch {
    /* ignore */
  }
  return typeof window !== 'undefined' && window.innerWidth < 1100
}

function NavEntry({
  item,
  collapsed,
  onNavigate,
}: {
  item: NavItem
  collapsed: boolean
  onNavigate?: () => void
}) {
  const link = (
    <NavLink
      to={item.to}
      onClick={onNavigate}
      className={({ isActive }) =>
        cn(
          'group text-sidebar-foreground/80 hover:bg-sidebar-accent hover:text-foreground focus-visible:ring-ring/50 flex h-8 items-center gap-2.5 rounded-md px-2.5 text-sm font-medium transition-colors focus-visible:ring-[3px] focus-visible:outline-none',
          isActive && 'bg-sidebar-accent text-foreground',
          collapsed && 'justify-center px-0',
        )
      }
    >
      <item.icon className="size-4 shrink-0 opacity-80" />
      {!collapsed && <span className="truncate">{item.label}</span>}
    </NavLink>
  )
  return collapsed ? (
    <Hint label={item.label} side="right">
      {link}
    </Hint>
  ) : (
    link
  )
}

function SidebarNav({ collapsed, onNavigate }: { collapsed: boolean; onNavigate?: () => void }) {
  const { data: me } = useMe()
  return (
    <nav className="flex flex-1 flex-col gap-6 overflow-y-auto px-3 py-4" aria-label="Main">
      <div className="grid gap-0.5">
        {MAIN_NAV.map((i) => (
          <NavEntry key={i.to} item={i} collapsed={collapsed} onNavigate={onNavigate} />
        ))}
      </div>
      {me?.is_admin && (
        <div className="grid gap-0.5">
          {collapsed ? (
            <div className="bg-sidebar-border mx-auto mb-1 h-px w-6" />
          ) : (
            <div className="text-muted-foreground mb-1 px-2.5 text-[11px] font-semibold tracking-wider uppercase">
              Admin
            </div>
          )}
          {ADMIN_NAV.map((i) => (
            <NavEntry key={i.to} item={i} collapsed={collapsed} onNavigate={onNavigate} />
          ))}
        </div>
      )}
    </nav>
  )
}

export function AppShell() {
  const [collapsed, setCollapsed] = useState(readCollapsed)
  const [mobileOpen, setMobileOpen] = useState(false)

  const toggle = () => {
    setCollapsed((c) => {
      try {
        localStorage.setItem(COLLAPSE_KEY, c ? '0' : '1')
      } catch {
        /* ignore */
      }
      return !c
    })
  }

  return (
    <div className="bg-background flex h-dvh overflow-hidden">
      <a
        href="#main"
        className="focus:bg-card sr-only focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-50 focus:rounded focus:px-3 focus:py-2 focus:shadow"
      >
        Skip to content
      </a>
      <aside
        className={cn(
          'bg-sidebar hidden shrink-0 flex-col border-r transition-[width] duration-200 md:flex',
          collapsed ? 'w-[60px]' : 'w-60',
        )}
        data-testid="sidebar"
        data-collapsed={collapsed}
      >
        <div
          className={cn(
            'border-sidebar-border flex h-14 items-center border-b px-4',
            collapsed && 'justify-center px-0',
          )}
        >
          <NavLink to="/dashboards" aria-label="Quack Quack home">
            <Logo collapsed={collapsed} />
          </NavLink>
        </div>
        <SidebarNav collapsed={collapsed} />
        <div className={cn('border-sidebar-border border-t p-3', collapsed && 'flex justify-center')}>
          <Button
            variant="ghost"
            size={collapsed ? 'icon-sm' : 'sm'}
            onClick={toggle}
            aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
            className="text-muted-foreground"
          >
            {collapsed ? <PanelLeftOpen /> : <PanelLeftClose />}
            {!collapsed && 'Collapse'}
          </Button>
        </div>
      </aside>

      <Sheet open={mobileOpen} onOpenChange={setMobileOpen}>
        <SheetContent side="left" className="bg-sidebar w-64 sm:max-w-64" aria-describedby={undefined}>
          <SheetTitle className="flex h-14 items-center border-b px-4">
            <Logo />
          </SheetTitle>
          <SidebarNav collapsed={false} onNavigate={() => setMobileOpen(false)} />
        </SheetContent>
      </Sheet>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="bg-background/80 flex h-14 shrink-0 items-center gap-3 border-b px-4 backdrop-blur md:px-6">
          <Button
            variant="ghost"
            size="icon-sm"
            className="md:hidden"
            onClick={() => setMobileOpen(true)}
            aria-label="Open navigation"
          >
            <Menu />
          </Button>
          <div className="md:hidden">
            <Logo collapsed />
          </div>
          <div className="flex-1" />
          <ConnectionIndicator />
          <ThemeToggle />
          <UserMenu />
        </header>
        <main id="main" className="min-h-0 flex-1 overflow-y-auto" tabIndex={-1}>
          <Outlet />
        </main>
      </div>
    </div>
  )
}

/** Standard padded page container. */
export function Page({
  children,
  className,
  wide,
}: {
  children: React.ReactNode
  className?: string
  wide?: boolean
}) {
  return (
    <div
      className={cn(
        'mx-auto w-full px-4 py-6 md:px-8 md:py-8',
        wide ? 'max-w-[1800px]' : 'max-w-6xl',
        className,
      )}
    >
      {children}
    </div>
  )
}
