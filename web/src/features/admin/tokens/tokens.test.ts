import { describe, expect, it } from 'vitest'
import { SCOPES, expiresAt, tokenKeys, tokenState } from './api'

describe('API tokens', () => {
  it('offers exactly the scopes the server knows', () => {
    expect(SCOPES.map((s) => s.value)).toEqual(['read', 'control', 'alerts'])
    expect(tokenKeys.machineUsers).toEqual(['admin', 'machine-users'])
  })

  it('computes the expiry from a number of days (0 = never)', () => {
    const now = Date.parse('2026-10-19T00:00:00Z')
    expect(expiresAt(0, now)).toBeUndefined()
    expect(expiresAt(30, now)).toBe('2026-11-18T00:00:00.000Z')
  })

  it('tells expired tokens apart', () => {
    const now = Date.parse('2026-10-19T00:00:00Z')
    expect(tokenState({ expires_at: undefined }, now)).toBe('active')
    expect(tokenState({ expires_at: '2026-10-20T00:00:00Z' }, now)).toBe('active')
    expect(tokenState({ expires_at: '2026-10-18T00:00:00Z' }, now)).toBe('expired')
  })
})
