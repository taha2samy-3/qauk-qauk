'use strict'
const test = require('node:test')
const assert = require('node:assert/strict')
const crypto = require('node:crypto')
const { loadPrivateKey, signDeviceToken } = require('../lib/token')
const { endpoints } = require('../lib/endpoints')
const { toMessage, fromMessage } = require('../lib/format')
const { newDevice, verifyJwt } = require('./fake-gateway')

test('tokens: ES256 and RS256 are picked from the key and verify', () => {
  for (const type of ['ec', 'rsa']) {
    const d = newDevice(type)
    const { key, alg } = loadPrivateKey(d.privateKeyPem)
    assert.equal(alg, type === 'ec' ? 'ES256' : 'RS256')
    const tok = signDeviceToken({ deviceId: d.deviceId.toUpperCase(), key, alg, lifetimeSec: 600, now: Date.now() })
    const claims = verifyJwt(tok, d.publicKey)
    assert.ok(claims, `${type} token verifies`)
    assert.equal(claims.id, d.deviceId) // lower-cased, as the server stores it
    assert.equal(claims.exp - claims.iat, 600)
  }
})

test('tokens: SEC1 and PKCS#1 PEMs are accepted too', () => {
  const ec = crypto.generateKeyPairSync('ec', { namedCurve: 'P-256' }).privateKey.export({ type: 'sec1', format: 'pem' })
  assert.equal(loadPrivateKey(ec).alg, 'ES256')
  const rsa = crypto.generateKeyPairSync('rsa', { modulusLength: 2048 }).privateKey.export({ type: 'pkcs1', format: 'pem' })
  assert.equal(loadPrivateKey(rsa).alg, 'RS256')
})

test('tokens: unusable keys are explained', () => {
  const pub = newDevice().publicKey.export({ type: 'spki', format: 'pem' })
  assert.throws(() => loadPrivateKey(pub), /public key/)
  assert.throws(() => loadPrivateKey(''), /no private key/)
  assert.throws(() => loadPrivateKey('-----BEGIN PRIVATE KEY-----\nnope\n-----END PRIVATE KEY-----'), /not a valid PEM/)
  const p384 = crypto.generateKeyPairSync('ec', { namedCurve: 'P-384' }).privateKey.export({ type: 'pkcs8', format: 'pem' })
  assert.throws(() => loadPrivateKey(p384), /P-256/)
  const rsa1024 = crypto.generateKeyPairSync('rsa', { modulusLength: 1024 }).privateKey.export({ type: 'pkcs8', format: 'pem' })
  assert.throws(() => loadPrivateKey(rsa1024), /2048/)
  const ed = crypto.generateKeyPairSync('ed25519').privateKey.export({ type: 'pkcs8', format: 'pem' })
  assert.throws(() => loadPrivateKey(ed), /unsupported key type/)
  const { key, alg } = loadPrivateKey(newDevice().privateKeyPem)
  assert.throws(() => signDeviceToken({ deviceId: 'dev-1', key, alg }), /UUID/)
})

test('endpoints: base URLs, prefixes and pasted device URLs', () => {
  assert.deepEqual(endpoints('http://127.0.0.1:8080'), {
    secure: false,
    ws: 'ws://127.0.0.1:8080/device/node_red/',
    elements: 'http://127.0.0.1:8080/device/elements',
  })
  assert.equal(endpoints('https://iot.example.com/quack/').ws, 'wss://iot.example.com/quack/device/node_red/')
  assert.equal(endpoints('wss://iot.example.com/device/node_red/').elements, 'https://iot.example.com/device/elements')
  assert.equal(endpoints(' ws://h:1/device/node_red ').ws, 'ws://h:1/device/node_red/')
  assert.throws(() => endpoints('127.0.0.1:8080'), /scheme|not valid/)
  assert.throws(() => endpoints('mqtt://broker'), /scheme/)
})

test('format: auto wraps plain values and unwraps {value}', () => {
  assert.deepEqual(toMessage(21.5), { value: 21.5 })
  assert.deepEqual(toMessage(true), { value: true })
  assert.deepEqual(toMessage('ON'), { value: 'ON' })
  assert.deepEqual(toMessage([1, 2]), { value: [1, 2] })
  assert.deepEqual(toMessage(null), { value: null })
  const obj = { temperature: 21, gps: { lat: 1 } }
  assert.equal(toMessage(obj), obj)
  assert.equal(toMessage(21.5, 'raw'), 21.5)
  assert.throws(() => toMessage(undefined), /empty/)
  assert.throws(() => toMessage(Buffer.from('x')), /binary/)
  assert.throws(() => toMessage(NaN), /not valid JSON/)

  assert.equal(fromMessage({ value: 1 }), 1)
  assert.deepEqual(fromMessage({ value: 1 }, 'raw'), { value: 1 })
  assert.deepEqual(fromMessage({ value: 1, unit: 'C' }), { value: 1, unit: 'C' })
  assert.deepEqual(fromMessage({ relay: 'ON' }), { relay: 'ON' })
  assert.equal(fromMessage(7), 7)
})
