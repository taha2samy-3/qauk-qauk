import { describe, expect, it } from 'vitest'
import { bindPoint, readNumber, readRaw, X_RECEIVED } from './messages'
import { switchState } from '@/widgets/switch/SwitchWidget'

describe('attribute reading', () => {
  it('keeps the legacy convention without a field', () => {
    expect(readRaw({ value: 3 })).toBe(3)
    expect(readRaw({ x: 't', y: 4 })).toBe(4)
    expect(readRaw(5)).toBe(5)
    expect(readRaw({ temperature: 1 })).toBeUndefined()
  })

  it('reads a bound attribute with a transform', () => {
    const m = { t: 215, gps: { lat: '30.04' }, ok: true }
    expect(readNumber(m, 't', { scale: 0.1 })).toBeCloseTo(21.5)
    expect(readNumber(m, 'gps.lat')).toBeCloseTo(30.04)
    expect(readNumber(m, 'ok')).toBe(1)
    expect(readNumber(m, 'missing')).toBeUndefined()
  })
})

describe('chart points', () => {
  const received = '2026-10-07T10:00:00.000Z'
  const at = Date.parse(received)

  it('auto X: message.x when present, else receive time', () => {
    expect(bindPoint({ x: '2026-10-07T09:00:00Z', y: 2 }, received, {})).toEqual([Date.parse('2026-10-07T09:00:00Z'), 2])
    expect(bindPoint({ value: 2 }, received, {})).toEqual([at, 2])
  })

  it('binds Y and X attributes, epoch ms and seconds', () => {
    expect(bindPoint({ temp: 21, ts: 1_791_000_000_000 }, received, { field: 'temp', xField: 'ts' })).toEqual([1_791_000_000_000, 21])
    expect(bindPoint({ temp: 21, ts: 1_791_000_000 }, received, { field: 'temp', xField: 'ts' })).toEqual([1_791_000_000_000, 21])
    expect(bindPoint({ temp: 21, x: '2000-01-01T00:00:00Z' }, received, { field: 'temp', xField: X_RECEIVED })).toEqual([at, 21])
    expect(bindPoint({ temp: 'n/a' }, received, { field: 'temp' })).toBeUndefined()
  })
})

describe('switch state', () => {
  it('explicit on/off values, including strings', () => {
    expect(switchState('ON', 'ON', 'OFF', true)).toBe(true)
    expect(switchState('off', 'ON', 'OFF', true)).toBe(false)
    expect(switchState('MAYBE', 'ON', 'OFF', true)).toBeUndefined()
  })
  it('conventions when no explicit values are set', () => {
    expect(switchState(1, 1, 0, false)).toBe(true)
    expect(switchState(0, 1, 0, false)).toBe(false)
    expect(switchState('on', 1, 0, false)).toBe(true)
    expect(switchState(false, 1, 0, false)).toBe(false)
    expect(switchState(undefined, 1, 0, false)).toBeUndefined()
  })
})
