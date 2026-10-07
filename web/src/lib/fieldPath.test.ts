import { describe, expect, it } from 'vitest'
import { applyTransform, asTime, buildMessage, discoverFields, getPath, isValidPath, parsePath } from './fieldPath'
import { mapValue, normalize, parseLiteral, readMappings } from './valueMap'

describe('field paths', () => {
  const msg = { temperature: 21.5, gps: { lat: 30.04 }, sensors: [{ temp: 7 }, { temp: 8 }], relay: 'ON' }

  it('parses and reads paths', () => {
    expect(parsePath('sensors[1].temp')).toEqual(['sensors', 1, 'temp'])
    expect(getPath(msg, 'temperature')).toBe(21.5)
    expect(getPath(msg, 'gps.lat')).toBe(30.04)
    expect(getPath(msg, 'sensors[1].temp')).toBe(8)
    expect(getPath(msg, 'missing.x')).toBeUndefined()
    expect(getPath(msg, 'relay.x')).toBeUndefined()
  })

  it('validates like the server', () => {
    for (const ok of ['a', 'a.b', 'a[0].b', 'temp-c']) expect(isValidPath(ok)).toBe(true)
    for (const bad of ['', '.a', 'a..b', 'a[x]', 'a b', "a'b"]) expect(isValidPath(bad)).toBe(false)
  })

  it('builds command messages', () => {
    expect(buildMessage('relay', 'OFF')).toEqual({ relay: 'OFF' })
    expect(buildMessage('state.on', true)).toEqual({ state: { on: true } })
    expect(buildMessage('out[1]', 5)).toEqual({ out: [undefined, 5] })
  })

  it('discovers attributes with kinds', () => {
    const fields = discoverFields([{ ...msg, ts: 1_791_000_000_000, at: '2026-10-07T10:00:00Z', ok: true, level: '3.5' }])
    const byPath = Object.fromEntries(fields.map((f) => [f.path, f.kind]))
    expect(byPath).toMatchObject({
      temperature: 'number',
      'gps.lat': 'number',
      'sensors[0].temp': 'number',
      relay: 'string',
      ts: 'time',
      at: 'time',
      ok: 'boolean',
      level: 'number',
    })
  })

  it('parses times and transforms', () => {
    expect(asTime(1_700_000_000)).toBe(1_700_000_000_000)
    expect(asTime(1_700_000_000_000)).toBe(1_700_000_000_000)
    expect(asTime('2026-10-07T10:00:00Z')).toBe(Date.parse('2026-10-07T10:00:00Z'))
    expect(applyTransform(215, { scale: 0.1 })).toBeCloseTo(21.5)
    expect(applyTransform(10, { offset: -2 })).toBe(8)
  })
})

describe('value mappings', () => {
  const maps = readMappings([
    { match: '1', label: 'Running', color: 'green' },
    { match: 'on', label: 'Open' },
    { match: '', label: 'ignored' },
  ])

  it('matches numbers, numeric strings, booleans and case-insensitive text', () => {
    expect(mapValue(1, maps)?.label).toBe('Running')
    expect(mapValue('1.0', maps)?.label).toBe('Running')
    expect(mapValue(true, maps)).toBeUndefined()
    expect(mapValue('ON', maps)?.label).toBe('Open')
    expect(mapValue(0, maps)).toBeUndefined()
    expect(maps).toHaveLength(2)
    expect(normalize(true)).toBe('true')
  })

  it('parses literals for commands', () => {
    expect(parseLiteral('1')).toBe(1)
    expect(parseLiteral('true')).toBe(true)
    expect(parseLiteral('ON')).toBe('ON')
  })
})
