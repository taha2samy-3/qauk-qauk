'use strict'

/**
 * Accepts the server as http(s)://host[:port][/prefix] or the full device URL
 * (ws(s)://host/device/node_red/) and returns the socket and HTTP endpoints.
 */
function endpoints(server) {
  let u
  try {
    u = new URL(String(server ?? '').trim())
  } catch {
    throw new Error('the server URL is not valid (example: http://127.0.0.1:8080)')
  }
  if (!['http:', 'https:', 'ws:', 'wss:'].includes(u.protocol)) {
    throw new Error(`unsupported server URL scheme "${u.protocol}" (use http, https, ws or wss)`)
  }
  const secure = u.protocol === 'https:' || u.protocol === 'wss:'
  const prefix = u.pathname.replace(/\/+$/, '').replace(/\/device\/node_red$/, '')
  const host = u.host
  return {
    secure,
    ws: `${secure ? 'wss' : 'ws'}://${host}${prefix}/device/node_red/`,
    elements: `${secure ? 'https' : 'http'}://${host}${prefix}/device/elements`,
  }
}

module.exports = { endpoints }
