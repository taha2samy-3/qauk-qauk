'use strict'
const { toMessage } = require('../lib/format')
const { statusFor } = require('../lib/status')

module.exports = function (RED) {
  function QuackOutNode(config) {
    RED.nodes.createNode(this, config)
    const node = this
    node.element = (config.element || '').trim()
    node.format = config.format || 'auto'
    node.device = RED.nodes.getNode(config.device)
    if (!node.device) {
      node.status({ fill: 'red', shape: 'dot', text: 'no device configured' })
      return
    }

    node.onConnStatus = (s) => node.status(statusFor(s))
    node.device.register(node)

    node.on('input', async (msg, send, done) => {
      try {
        const conn = node.device.conn
        if (!conn) throw new Error(`device config error: ${node.device.connState.detail}`)
        const ref = node.element || msg.element_id || msg.topic
        if (!ref) throw new Error('no element: pick one in the node, or set msg.element_id or msg.topic')
        const id = await conn.resolveElement(ref)
        conn.send(id, toMessage(msg.payload, node.format))
        done()
      } catch (err) {
        done(err)
      }
    })

    node.on('close', (done) => node.device.deregister(node, done))
  }

  RED.nodes.registerType('quack-out', QuackOutNode)
}
