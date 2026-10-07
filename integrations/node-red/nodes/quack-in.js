'use strict'
const { fromMessage } = require('../lib/format')
const { statusFor } = require('../lib/status')

module.exports = function (RED) {
  function QuackInNode(config) {
    RED.nodes.createNode(this, config)
    const node = this
    node.element = (config.element || '').trim().toLowerCase()
    node.format = config.format || 'auto'
    node.from = config.from || 'users'
    node.confirm = config.confirm === true || config.confirm === 'true'
    node.device = RED.nodes.getNode(config.device)
    if (!node.device) {
      node.status({ fill: 'red', shape: 'dot', text: 'no device configured' })
      return
    }

    node.onConnStatus = (s) => node.status(statusFor(s))
    node.onFrame = (f) => {
      if (node.element && f.elementId !== node.element) return
      // "users": commands from dashboards only, not telemetry from this device's other connections
      if (node.from === 'users' && f.from.kind !== 'user') return
      node.send({
        payload: fromMessage(f.message, node.format),
        topic: f.element ?? f.elementId,
        element_id: f.elementId,
        element: f.element,
        from: f.from,
        last_edit_at: f.lastEditAt,
        _msgid: RED.util.generateId(),
      })
      if (node.confirm && f.from.kind === 'user') {
        // actuator pattern: report the commanded state back; dashboards wait for this echo
        try {
          node.device.conn.send(f.elementId, f.message)
        } catch (err) {
          node.warn(`could not confirm the command: ${err.message}`)
        }
      }
    }
    node.device.register(node)

    node.on('close', (done) => node.device.deregister(node, done))
  }

  RED.nodes.registerType('quack-in', QuackInNode)
}
