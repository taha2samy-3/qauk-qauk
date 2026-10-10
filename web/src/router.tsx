import { createBrowserRouter, Navigate } from 'react-router'
import { LoginPage } from '@/auth/LoginPage'
import { RequireAdmin, RequireAuth } from '@/auth/guards'
import { AppShell } from '@/components/layout/AppShell'
import { NotFound, RouteError } from '@/components/RouteError'
import { DashboardPage } from '@/features/dashboards/DashboardPage'
import { DashboardsListPage } from '@/features/dashboards/DashboardsListPage'
import { DevicesPage } from '@/features/devices/DevicesPage'
import { AuditPage } from '@/features/admin/AuditPage'
import { ConnectionsPage } from '@/features/admin/ConnectionsPage'
import { MqttConnectionPage } from '@/features/admin/mqtt/MqttConnectionPage'
import { MqttConnectionsPage } from '@/features/admin/mqtt/MqttConnectionsPage'
import { MqttDecodersPage } from '@/features/admin/mqtt/MqttDecodersPage'
import { MqttRejectedPage } from '@/features/admin/mqtt/MqttRejectedPage'
import { DevicesAdminPage } from '@/features/admin/DevicesAdminPage'
import { ElementsPage } from '@/features/admin/ElementsPage'
import { GroupsPage } from '@/features/admin/GroupsPage'
import { KeysPage } from '@/features/admin/KeysPage'
import { PermissionsPage } from '@/features/admin/PermissionsPage'
import { PresencePage } from '@/features/admin/PresencePage'
import { UsersPage } from '@/features/admin/UsersPage'

export const router = createBrowserRouter([
  { path: '/login', element: <LoginPage />, errorElement: <RouteError /> },
  {
    element: <RequireAuth />,
    errorElement: <RouteError />,
    children: [
      {
        element: <AppShell />,
        children: [
          { index: true, element: <Navigate to="/dashboards" replace /> },
          { path: 'dashboards', element: <DashboardsListPage /> },
          { path: 'dashboards/:id', element: <DashboardPage /> },
          { path: 'devices', element: <DevicesPage /> },
          {
            path: 'admin',
            element: <RequireAdmin />,
            children: [
              { index: true, element: <Navigate to="/admin/users" replace /> },
              { path: 'users', element: <UsersPage /> },
              { path: 'groups', element: <GroupsPage /> },
              { path: 'keys', element: <KeysPage /> },
              { path: 'devices', element: <DevicesAdminPage /> },
              { path: 'elements', element: <ElementsPage /> },
              { path: 'permissions', element: <PermissionsPage /> },
              { path: 'connections', element: <ConnectionsPage /> },
              { path: 'mqtt', element: <MqttConnectionsPage /> },
              { path: 'mqtt/decoders', element: <MqttDecodersPage /> },
              { path: 'mqtt/rejected', element: <MqttRejectedPage /> },
              { path: 'mqtt/:id', element: <MqttConnectionPage /> },
              { path: 'presence', element: <PresencePage /> },
              { path: 'audit', element: <AuditPage /> },
            ],
          },
          { path: '*', element: <NotFound /> },
        ],
      },
    ],
  },
])
