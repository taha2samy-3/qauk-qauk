'use strict'
const fs = require('node:fs')
const { QuackConnection } = require('../lib/connection')

module.exports = function (RED) {
  function QuackDeviceNode(config) {
    RED.nodes.createNode(this, config)
    const node = this
    node.name = config.name
    node.users = new Set()
    node.connState = { state: 'idle' }
    node.conn = null

    try {
      node.conn = new QuackConnection(connectionOptions(RED, config, node.credentials))
    } catch (err) {
      node.connState = { state: 'error', detail: err.message }
      node.error(`Quack Quack device "${config.name || config.deviceId}": ${err.message}`)
    }

    if (node.conn) {
      let last = ''
      node.conn.on('status', (s) => {
        node.connState = s
        for (const u of node.users) u.onConnStatus?.(s)
        const line = `${s.state}${s.detail ? `: ${s.detail}` : ''}`
        if (line !== last && s.state !== 'connecting') node.log(`${node.conn.ep.ws} ${line}`)
        last = line
      })
      node.conn.on('frame', (f) => {
        for (const u of node.users) u.onFrame?.(f)
      })
      node.conn.on('warn', (m) => node.warn(m))
    }

    /** Nodes call this to share the connection; the socket opens with the first one. */
    node.register = (user) => {
      node.users.add(user)
      user.onConnStatus?.(node.connState)
      node.conn?.start()
    }
    node.deregister = (user, done) => {
      node.users.delete(user)
      if (node.users.size === 0 && node.conn) node.conn.stop().then(() => done?.())
      else done?.()
    }

    node.on('close', (done) => {
      if (!node.conn) return done()
      node.conn.stop().then(() => done())
    })
  }

  RED.nodes.registerType('quack-device', QuackDeviceNode, {
    credentials: { privateKey: { type: 'password' } },
  })

  // Editor helper: signs with the key from the edit dialog (or the deployed
  // one) and lists the device's elements, for the element pickers and the
  // "Test connection" button.
  RED.httpAdmin.post('/quackquack/elements', RED.auth.needsPermission('quack-device.write'), async (req, res) => {
    const b = req.body ?? {}
    try {
      const creds = { privateKey: b.privateKey }
      if (!creds.privateKey || creds.privateKey === '__PWRD__') {
        creds.privateKey = b.id ? RED.nodes.getCredentials(b.id)?.privateKey : undefined
      }
      const conn = new QuackConnection(connectionOptions(RED, b, creds))
      const { device, elements } = await conn.refreshElements()
      res.json({ device, alg: conn.alg, elements: elements.map(({ id, name, description }) => ({ id, name, description })) })
    } catch (err) {
      res.status(400).json({ error: err.message })
    }
  })
}

function connectionOptions(RED, config, credentials) {
  let privateKey = credentials?.privateKey
  const keyFile = String(config.keyFile ?? '').trim()
  if (keyFile) {
    try {
      privateKey = fs.readFileSync(keyFile, 'utf8')
    } catch (err) {
      throw new Error(`cannot read the key file ${keyFile}: ${err.code ?? err.message}`)
    }
  }
  const tls = {}
  if (config.tls) RED.nodes.getNode(config.tls)?.addTLSOptions(tls)
  const minutes = Number(config.lifetime) || 60
  return {
    server: config.server,
    deviceId: config.deviceId,
    privateKey,
    lifetimeSec: Math.round(minutes * 60),
    rate: config.rate === '' || config.rate === undefined ? 50 : Number(config.rate),
    tls,
  }
}
