'use strict'
// The nodes inside a real Node-RED runtime (node-red-node-test-helper),
// against the fake gateway.
const test = require('node:test')
const assert = require('node:assert/strict')
const crypto = require('node:crypto')
const helper = require('node-red-node-test-helper')
const quackNodes = require('../nodes/quack')
const functionNode = require('@node-red/nodes/core/function/10-function.js')
const { startGateway, newDevice, until } = require('./fake-gateway')

helper.init(require.resolve('node-red'))

const TEMP = { id: crypto.randomUUID(), name: 'Temperature', description: '', points: 10, details: null }
const RELAY = { id: crypto.randomUUID(), name: 'Gate relay', description: '', points: 10, details: null }

let gw
let dev

test.before(async () => {
  dev = newDevice()
  gw = await startGateway({ deviceId: dev.deviceId, publicKey: dev.publicKey, elements: [TEMP, RELAY] })
  await new Promise((r) => helper.startServer(r))
})
test.after(async () => {
  await new Promise((r) => helper.stopServer(r))
  await gw.stop()
})
test.afterEach(async () => {
  await helper.unload()
  gw.received.length = 0
})

const device = (extra = {}) => ({ id: 'dev', type: 'quack-device', name: 'Test', server: gw.url, deviceId: dev.deviceId, lifetime: 60, rate: 50, ...extra })
const creds = () => ({ dev: { privateKey: dev.privateKeyPem } })

// flow nodes live on a tab; the device config node is global
async function load(flow, credentials = creds()) {
  const nodes = flow.map((n) => (n.type === 'quack-device' ? n : { z: 'tab', ...n }))
  await helper.load([quackNodes, functionNode], [{ id: 'tab', type: 'tab', label: 'test' }, ...nodes], credentials)
}

const connectedNode = (id) =>
  until(() => helper.getNode('dev')?.conn?.connected && helper.getNode('dev').conn.elements && helper.getNode(id), 4000, 'device connected')

test('quack out: fixed element, msg.topic by name, msg.element_id, raw and object payloads', async () => {
  await load([
    device(),
    { id: 'out', type: 'quack-out', device: 'dev', element: TEMP.id, format: 'auto', wires: [] },
    { id: 'byTopic', type: 'quack-out', device: 'dev', element: '', format: 'auto', wires: [] },
    { id: 'outRaw', type: 'quack-out', device: 'dev', element: TEMP.id, format: 'raw', wires: [] },
  ])
  await connectedNode('out')
  helper.getNode('out').receive({ payload: 21.5 })
  helper.getNode('byTopic').receive({ topic: 'gate RELAY', payload: { relay: 'ON' } })
  helper.getNode('byTopic').receive({ element_id: TEMP.id, payload: { temperature: 20, gps: { lat: 30.1 } } })
  helper.getNode('outRaw').receive({ payload: 7 })
  await until(() => gw.received.length === 4)
  assert.deepEqual(gw.received, [
    { element_id: TEMP.id, message: { value: 21.5 } },
    { element_id: RELAY.id, message: { relay: 'ON' } },
    { element_id: TEMP.id, message: { temperature: 20, gps: { lat: 30.1 } } },
    { element_id: TEMP.id, message: 7 },
  ])
})

test('quack out: errors reach catch nodes instead of vanishing', async () => {
  await load([
    device(),
    { id: 'out', type: 'quack-out', device: 'dev', element: '', wires: [] },
    { id: 'catcher', type: 'catch', scope: null, uncaught: false, wires: [['sink']] },
    { id: 'sink', type: 'helper' },
  ])
  await connectedNode('out')
  const caught = []
  helper.getNode('sink').on('input', (m) => caught.push(m.error.message))
  helper.getNode('out').receive({ payload: 1 })
  helper.getNode('out').receive({ topic: 'Humidity', payload: 1 })
  await until(() => caught.length === 2)
  assert.match(caught[0], /no element/)
  assert.match(caught[1], /no element named "Humidity" \(elements: Temperature, Gate relay\)/)
  assert.equal(gw.received.length, 0)
})

test('quack in: commands from users, unwrapped, with element name and sender', async () => {
  await load([
    device(),
    { id: 'in', type: 'quack-in', device: 'dev', element: '', from: 'users', format: 'auto', wires: [['sink']] },
    { id: 'inRelay', type: 'quack-in', device: 'dev', element: RELAY.id, from: 'any', format: 'raw', wires: [['sinkRelay']] },
    { id: 'sink', type: 'helper' },
    { id: 'sinkRelay', type: 'helper' },
  ])
  await connectedNode('in')
  const all = []
  const relay = []
  helper.getNode('sink').on('input', (m) => all.push(m))
  helper.getNode('sinkRelay').on('input', (m) => relay.push(m))

  gw.broadcast({ element_id: RELAY.id, message: { value: 1 }, auth: { user_id: 5, username: 'alice' }, last_edit_at: '2026-10-07T11:05:10.890Z' })
  gw.broadcast({ element_id: TEMP.id, message: { value: 3 }, auth: { user_id: dev.deviceId, username: 'Test' } }) // another socket of this device
  await until(() => all.length === 1 && relay.length === 1)
  await new Promise((r) => setTimeout(r, 200))
  assert.equal(all.length, 1, 'device frames are filtered out for "users"')
  assert.equal(all[0].payload, 1)
  assert.equal(all[0].topic, 'Gate relay')
  assert.equal(all[0].element_id, RELAY.id)
  assert.deepEqual(all[0].from, { kind: 'user', user_id: 5, username: 'alice' })
  assert.equal(all[0].last_edit_at, '2026-10-07T11:05:10.890Z')
  assert.deepEqual(relay[0].payload, { value: 1 }, 'raw keeps the message')
})

test('quack in: "confirm" echoes user commands back as telemetry', async () => {
  await load([
    device(),
    { id: 'in', type: 'quack-in', device: 'dev', element: RELAY.id, from: 'users', format: 'auto', confirm: true, wires: [[]] },
  ])
  await connectedNode('in')
  gw.broadcast({ element_id: RELAY.id, message: { relay: 'OFF' }, auth: { user_id: 5, username: 'alice' } })
  gw.broadcast({ element_id: RELAY.id, message: { relay: 'ON' }, auth: { user_id: dev.deviceId, username: 'Test' } })
  await until(() => gw.received.length === 1)
  await new Promise((r) => setTimeout(r, 200))
  assert.deepEqual(gw.received, [{ element_id: RELAY.id, message: { relay: 'OFF' } }], 'only the user command is confirmed')
})

test('in → out round trip shares one connection', async () => {
  await load([
    device(),
    { id: 'in', type: 'quack-in', device: 'dev', element: '', from: 'users', format: 'auto', wires: [['double']] },
    { id: 'double', type: 'function', func: 'msg.payload = msg.payload * 2; return msg;', wires: [['out']] },
    { id: 'out', type: 'quack-out', device: 'dev', element: '', format: 'auto', wires: [] },
  ])
  await connectedNode('in')
  gw.broadcast({ element_id: TEMP.id, message: { value: 4 }, auth: { user_id: 5, username: 'alice' } })
  await until(() => gw.received.length === 1)
  assert.deepEqual(gw.received[0], { element_id: TEMP.id, message: { value: 8 } })
  assert.equal(gw.sockets.size, 1)
})

test('status badges follow the connection; a bad key is reported, not thrown', async () => {
  const other = newDevice()
  await load([device(), { id: 'out', type: 'quack-out', device: 'dev', element: TEMP.id, wires: [] }], {
    dev: { privateKey: other.privateKeyPem },
  })
  const out = helper.getNode('out')
  const badges = []
  out.on('call:status', (c) => badges.push(c.args[0]))
  await until(() => badges.some((b) => /auth failed/.test(b.text ?? '')), 4000, 'auth failed badge')

  await helper.unload()
  await load([device({ deviceId: 'not-a-uuid' }), { id: 'out', type: 'quack-out', device: 'dev', element: TEMP.id, wires: [] }])
  assert.equal(helper.getNode('dev').connState.state, 'error')
  assert.match(helper.getNode('dev').connState.detail, /UUID/)
})

test('the editor endpoint lists elements with the dialog key or the deployed one', async () => {
  await load([device(), { id: 'out', type: 'quack-out', device: 'dev', element: TEMP.id, wires: [] }])
  const body = { server: gw.url, deviceId: dev.deviceId }

  let res = await helper.request().post('/quackquack/elements').send({ ...body, privateKey: dev.privateKeyPem }).expect(200)
  assert.deepEqual(res.body, {
    device: { id: dev.deviceId, name: 'Test device' },
    alg: 'ES256',
    elements: [TEMP, RELAY].map(({ id, name, description }) => ({ id, name, description })),
  })
  // the editor never gets the deployed key back; '__PWRD__' means "use it"
  res = await helper.request().post('/quackquack/elements').send({ ...body, id: 'dev', privateKey: '__PWRD__' }).expect(200)
  assert.equal(res.body.elements.length, 2)

  res = await helper.request().post('/quackquack/elements').send({ ...body, privateKey: newDevice().privateKeyPem }).expect(400)
  assert.match(res.body.error, /authentication failed/)
  res = await helper.request().post('/quackquack/elements').send({ ...body }).expect(400)
  assert.match(res.body.error, /no private key/)
})

test('the editor can generate a P-256 key pair that signs valid tokens', async () => {
  await load([device()])
  const { body } = await helper.request().post('/quackquack/keypair').expect(200)
  assert.match(body.privateKey, /BEGIN PRIVATE KEY/)
  assert.match(body.publicKey, /BEGIN PUBLIC KEY/)
  const { loadPrivateKey, signDeviceToken } = require('../lib/token')
  const { verifyJwt } = require('./fake-gateway')
  const { key, alg } = loadPrivateKey(body.privateKey)
  assert.equal(alg, 'ES256')
  const tok = signDeviceToken({ deviceId: dev.deviceId, key, alg })
  assert.ok(verifyJwt(tok, crypto.createPublicKey(body.publicKey)))
})
