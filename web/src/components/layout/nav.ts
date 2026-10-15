import {
  RadioTower,
  Activity,
  Cable,
  Cpu,
  FileClock,
  KeyRound,
  LayoutDashboard,
  Lock,
  Radio,
  Users,
  UsersRound,
  Boxes,
  Webhook,
  type LucideIcon,
} from 'lucide-react'

export interface NavItem {
  to: string
  label: string
  icon: LucideIcon
}

export const MAIN_NAV: NavItem[] = [
  { to: '/dashboards', label: 'Dashboards', icon: LayoutDashboard },
  { to: '/devices', label: 'Devices', icon: Cpu },
]

export const ADMIN_NAV: NavItem[] = [
  { to: '/admin/users', label: 'Users', icon: Users },
  { to: '/admin/groups', label: 'Groups', icon: UsersRound },
  { to: '/admin/keys', label: 'Keys', icon: KeyRound },
  { to: '/admin/devices', label: 'Devices', icon: Radio },
  { to: '/admin/elements', label: 'Elements', icon: Boxes },
  { to: '/admin/permissions', label: 'Permissions', icon: Lock },
  { to: '/admin/connections', label: 'Connections', icon: Cable },
  { to: '/admin/mqtt', label: 'MQTT', icon: RadioTower },
  { to: '/admin/presence', label: 'Presence', icon: Activity },
  { to: '/admin/webhooks', label: 'Webhooks', icon: Webhook },
  { to: '/admin/audit', label: 'Audit log', icon: FileClock },
]
