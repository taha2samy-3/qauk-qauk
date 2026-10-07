'use strict'
// Node set entry point: the device config node and the in/out nodes.
module.exports = function (RED) {
  require('./quack-device')(RED)
  require('./quack-out')(RED)
  require('./quack-in')(RED)
}
