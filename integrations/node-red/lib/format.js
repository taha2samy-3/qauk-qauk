'use strict'
// msg.payload <-> the element `message`. "auto" wraps plain values as
// {"value": x} (the shape dashboards and history aggregates read) and unwraps
// it again on the way in; objects pass through unchanged. "raw" never touches it.

const isPlainObject = (v) => v !== null && typeof v === 'object' && !Array.isArray(v) && !Buffer.isBuffer(v)

function toMessage(payload, format = 'auto') {
  if (payload === undefined) throw new Error('msg.payload is empty')
  if (Buffer.isBuffer(payload)) throw new Error('binary payloads are not supported; send JSON')
  if (typeof payload === 'number' && !Number.isFinite(payload)) throw new Error(`${payload} is not valid JSON`)
  if (format === 'raw' || isPlainObject(payload)) return payload
  return { value: payload }
}

function fromMessage(message, format = 'auto') {
  if (format === 'auto' && isPlainObject(message)) {
    const keys = Object.keys(message)
    if (keys.length === 1 && keys[0] === 'value') return message.value
  }
  return message
}

module.exports = { toMessage, fromMessage, isPlainObject }
