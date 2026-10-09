'use strict'
const test = require('node:test')
const assert = require('node:assert/strict')
const crypto = require('node:crypto')
const { QuackConnection } = require('../lib/connection')
const { startGateway, newDevice, until } = require('./fake-gateway')

const ELEMENTS = [
  { id: crypto.randomUUID(), name: 'Temperature', description: '', points: 10, details: null },
  { id: crypto.randomUUID(), name: 'Gate relay', description: '', points: 10, details: null },
]

async function setup(t, gwOpts = {}, connOpts = {}) {
  const d = newDevice()
  const gw = await startGateway({ deviceId: d.deviceId, publicKey: d.publicKey, elements: ELEMENTS, ...gwOpts })
  const conn = new QuackConnection({
    server: gw.url,
    deviceId: d.deviceId,
    privateKey: d.privateKeyPem,
    minBackoffMs: 50,
    maxBackoffMs: 200,
    slowRetryMs: 300,
    ...connOpts,
  })
  const states = []
  conn.on('status', (s) => states.push(s))
  conn.on('warn', () => {})
  t.after(async () => {
    await conn.stop()
    await gw.stop()
  })
  return { d, gw, conn, states }
}

const connected = (conn) => until(() => conn.connected && conn.elements, 4000, 'connected with elements')

test('connects, loads elements and sends telemetry by name or ID', async (t) => {
  const { gw, conn } = await setup(t)
  conn.start()
  await connected(conn)
  assert.equal(conn.device.name, 'Test device')

  conn.send(await conn.resolveElement('temperature'), { value: 21.5 }) // names ignore case
  conn.send(await conn.resolveElement(ELEMENTS[1].id.toUpperCase()), { relay: 'ON' })
  await until(() => gw.received.length === 2)
  assert.deepEqual(gw.received, [
    { element_id: ELEMENTS[0].id, message: { value: 21.5 } },
    { element_id: ELEMENTS[1].id, message: { relay: 'ON' } },
  ])
})

test('unknown elements are refused with the list of names, after one reload', async (t) => {
  const { gw, conn } = await setup(t)
  conn.start()
  await connected(conn)
  conn.lastRefresh = 0
  const before = gw.elementRequests
  await assert.rejects(conn.resolveElement('Humidity'), /no element named "Humidity" \(elements: Temperature, Gate relay\)/)
  assert.equal(gw.elementRequests, before + 1)
  await assert.rejects(conn.resolveElement(crypto.randomUUID()), /no element with ID/)

  // an element added while connected becomes usable without reconnecting
  const added = { id: crypto.randomUUID(), name: 'Humidity', description: '', points: 10, details: null }
  gw.elements = [...ELEMENTS, added]
  conn.lastRefresh = 0
  assert.equal(await conn.resolveElement('Humidity'), added.id)

  gw.elements = [...ELEMENTS, { ...added, id: crypto.randomUUID(), name: 'temperature' }]
  await conn.refreshElements()
  await assert.rejects(conn.resolveElement('Temperature'), /more than one element/)
})

test('incoming frames carry who sent them', async (t) => {
  const { d, gw, conn } = await setup(t)
  const frames = []
  conn.on('frame', (f) => frames.push(f))
  conn.start()
  await connected(conn)
  gw.broadcast({ element_id: ELEMENTS[1].id, message: { value: 1 }, auth: { user_id: 5, username: 'alice' }, last_edit_at: '2026-10-07T11:05:10.890Z' })
  gw.broadcast({ element_id: ELEMENTS[0].id, message: { value: 2 }, auth: { user_id: d.deviceId, username: 'Test device' } })
  gw.broadcast({ nope: true })
  await until(() => frames.length === 2)
  assert.deepEqual(frames[0], {
    elementId: ELEMENTS[1].id,
    element: 'Gate relay',
    message: { value: 1 },
    from: { kind: 'user', user_id: 5, username: 'alice' },
    lastEditAt: '2026-10-07T11:05:10.890Z',
  })
  assert.equal(frames[1].from.kind, 'device')
  assert.equal(frames[1].element, 'Temperature')
})

test('reconnects with a freshly signed token after the server drops the socket', async (t) => {
  const { gw, conn, states } = await setup(t)
  conn.start()
  await connected(conn)
  gw.closeAll(1001, 'server shutting down')
  await until(() => gw.handshakes.length === 2 && conn.connected, 4000, 'second handshake')
  const drop = states.find((s) => s.state === 'disconnected')
  assert.equal(drop.detail, 'server shutting down')
  assert.ok(drop.retryIn > 0 && drop.retryIn <= 200)
})

test('403 and revocation retry slowly and say why', async (t) => {
  const { gw, conn, states } = await setup(t)
  gw.reject = true
  conn.start()
  const rejected = await until(() => states.find((s) => s.state === 'rejected'))
  assert.match(rejected.detail, /authentication failed/)
  assert.ok(rejected.retryIn >= 300, 'waits slowRetryMs')
  assert.equal(conn.connected, false)

  gw.reject = false // admin fixed the key
  await connected(conn)
  gw.closeAll(4000, 'device deleted')
  const revoked = await until(() => states.find((s) => s.state === 'revoked'))
  assert.equal(revoked.detail, 'device deleted')
  assert.ok(revoked.retryIn >= 300)
})

test('sending is refused loudly: offline, too large, over the rate', async (t) => {
  const { gw, conn } = await setup(t, {}, { rate: 5 })
  assert.throws(() => conn.send(ELEMENTS[0].id, { value: 1 }), (e) => e.code === 'offline')
  conn.start()
  await connected(conn)

  assert.throws(() => conn.send(ELEMENTS[0].id, { blob: 'x'.repeat(70 * 1024) }), (e) => e.code === 'size')
  const circular = {}
  circular.self = circular
  assert.throws(() => conn.send(ELEMENTS[0].id, circular), (e) => e.code === 'json')

  let sent = 0
  let limited = 0
  for (let i = 0; i < 8; i++) {
    try {
      conn.send(ELEMENTS[0].id, { value: i })
      sent++
    } catch (e) {
      assert.equal(e.code, 'rate')
      limited++
    }
  }
  assert.equal(sent, 5)
  assert.equal(limited, 3)
  await until(() => gw.received.length === 5)
})

test("each element's server limit is enforced loudly (drop), not for keep-latest elements", async (t) => {
  const elements = [
    { ...ELEMENTS[0], rate: 2, burst: 2, over_limit: 'drop' },
    { ...ELEMENTS[1], rate: 1, burst: 1, over_limit: 'latest' },
  ]
  const { gw, conn } = await setup(t, { elements }, { rate: 0 })
  conn.start()
  await connected(conn)
  const results = []
  for (let i = 0; i < 4; i++) {
    try {
      conn.send(elements[0].id, { value: i })
      results.push('sent')
    } catch (e) {
      results.push(e.code)
    }
  }
  assert.deepEqual(results, ['sent', 'sent', 'element-rate', 'element-rate'])
  for (let i = 0; i < 4; i++) conn.send(elements[1].id, { value: i }) // latest: the server keeps the newest
  await until(() => gw.received.length === 6)
})

test('a silent server is detected and the socket replaced', async (t) => {
  const { gw, conn, states } = await setup(t, {}, { livenessMs: 250 })
  conn.start()
  await connected(conn)
  // the fake gateway never pings
  await until(() => gw.handshakes.length >= 2, 4000, 'reconnect after liveness timeout')
  assert.ok(states.some((s) => s.state === 'disconnected' && s.detail === 'no ping from the server'))
})

test('stop() closes for good; stop+start never leaves two sockets', async (t) => {
  const { gw, conn } = await setup(t)
  conn.start()
  await connected(conn)
  await conn.stop()
  await until(() => gw.sockets.size === 0)
  await new Promise((r) => setTimeout(r, 400))
  assert.equal(gw.handshakes.length, 1, 'no reconnect after stop')

  // a partial redeploy: stop and start without waiting
  conn.stop()
  conn.start()
  conn.stop()
  conn.start()
  await until(() => conn.connected)
  await new Promise((r) => setTimeout(r, 500))
  assert.equal(gw.sockets.size, 1)
})

test('an older server without /device/elements still works with IDs', async (t) => {
  const { gw, conn } = await setup(t, { legacy: true })
  const warnings = []
  conn.removeAllListeners('warn')
  conn.on('warn', (w) => warnings.push(w))
  conn.start()
  await until(() => conn.connected && warnings.length)
  assert.match(warnings[0], /does not list elements/)
  assert.equal(await conn.resolveElement(ELEMENTS[0].id), ELEMENTS[0].id)
  await assert.rejects(conn.resolveElement('Temperature'), /only accepts IDs/)
  conn.send(ELEMENTS[0].id, { value: 3 })
  await until(() => gw.received.length === 1)
})
