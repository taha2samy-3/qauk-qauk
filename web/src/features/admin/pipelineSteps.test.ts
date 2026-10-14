import { describe, expect, it } from 'vitest'
import { newStep, toConfig, toDrafts } from './pipelineSteps'

describe('pipeline steps', () => {
  it('round-trips stored configs through the editor', () => {
    const stored = [
      { kind: 'pick', path: 'data.temp' },
      { kind: 'scale', field: 'value', mul: 0.1, add: -40 },
      { kind: 'unit', from: 'degC', to: 'degF' },
      { kind: 'round', decimals: 2 },
      { kind: 'clamp', min: 0 },
      { kind: 'map', table: { '0': 'OFF', '1': 'ON' }, default: 'UNKNOWN' },
      { kind: 'deadband', abs: 0.5, max_silence: '10m' },
      { kind: 'drop_if', op: '<', value: -100 },
      { kind: 'script', source: 'function transform(m) { return m }' },
    ]
    expect(toDrafts(stored).map((d) => toConfig(d).config)).toEqual(stored)
  })

  it('an empty number is left out, not sent as 0', () => {
    const s = { ...newStep('clamp'), min: '', max: '100' }
    expect(toConfig(s).config).toEqual({ kind: 'clamp', max: 100 })
    const d = { ...newStep('deadband'), abs: '', pct: '5' }
    expect(toConfig(d).config).toEqual({ kind: 'deadband', pct: 5, max_silence: '10m' })
  })

  it('a value that is not a number is an error, not NaN or text', () => {
    expect(toConfig({ ...newStep('scale'), mul: '1,5' }).error).toMatch(/Multiplier/)
    expect(toConfig({ ...newStep('drop_if'), value: 'offline' }).error).toMatch(/number/)
    expect(toConfig({ ...newStep('round'), decimals: '1.5' }).error).toMatch(/whole number/)
  })

  it('a mapping table must be a JSON object', () => {
    expect(toConfig({ ...newStep('map'), table: '["ON"]' }).error).toMatch(/object/)
    expect(toConfig({ ...newStep('map'), table: 'null' }).error).toMatch(/object/)
    expect(toConfig({ ...newStep('map'), table: '{"a": ' }).error).toMatch(/valid JSON/)
    expect(toConfig({ ...newStep('map'), default: 'OFF' }).error).toMatch(/default/)
  })

  it('every new step gets its own id (stable keys while reordering)', () => {
    expect(newStep('scale').id).not.toBe(newStep('scale').id)
  })
})
