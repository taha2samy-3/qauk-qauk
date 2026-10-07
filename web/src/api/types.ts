import type { components } from './schema'
import { isRecord } from '@/lib/utils'

export type Schemas = components['schemas']
export type Me = Schemas['MeBody']
export type Dashboard = Schemas['Dashboard']
export type User = Schemas['User']
export type Group = Schemas['Group']
export type Key = Schemas['Key']
export type Device = Schemas['Device']
export type AdminElement = Schemas['Element']
export type Style = Schemas['Style']
export type PermissionRow = Schemas['Permission']
export type Connection = Schemas['Connection']
export type PresenceRow = Schemas['PresenceRow']
export type AuditEntry = Schemas['AuditEntry']
export type HistoryEvent = Schemas['HistoryEvent']
export type Bucket = Schemas['Bucket']

export type Permission = 'R' | 'RC'
export type WidgetHint = 'sensor' | 'chart' | 'switch' | 'slider'

export interface ElementStyle {
  id: number
  name: string
  details: Record<string, unknown>
}

/** An element from /me/elements, with the loosely typed fields narrowed. */
export interface MyElement {
  id: string
  device_id: string
  name: string
  points: number
  description: string
  created_at: string
  details: Record<string, unknown>
  permission: Permission
  styles: ElementStyle[]
}

export function normalizeElement(raw: Schemas['UserElement']): MyElement {
  const styles = Array.isArray(raw.styles)
    ? (raw.styles as unknown[]).filter(isRecord).map((s) => ({
        id: Number(s.id),
        name: String(s.name ?? ''),
        details: isRecord(s.details) ? s.details : {},
      }))
    : []
  return {
    ...raw,
    details: isRecord(raw.details) ? raw.details : {},
    permission: raw.permission === 'RC' ? 'RC' : 'R',
    styles,
  }
}

/** The `widget` style hint (sensor | chart | switch | slider), if any. */
export function widgetHint(el: Pick<MyElement, 'styles'>): WidgetHint | undefined {
  const s = el.styles.find((st) => st.name === 'widget')
  const w = s?.details.widget
  return w === 'sensor' || w === 'chart' || w === 'switch' || w === 'slider' ? w : undefined
}
