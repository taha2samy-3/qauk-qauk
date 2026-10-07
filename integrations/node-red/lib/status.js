'use strict'

/** Connection state -> Node-RED node status badge. */
function statusFor(s) {
  switch (s?.state) {
    case 'connected':
      return { fill: 'green', shape: 'dot', text: 'connected' }
    case 'connecting':
      return { fill: 'yellow', shape: 'ring', text: 'connecting' }
    case 'disconnected':
      return { fill: 'red', shape: 'ring', text: retrying('disconnected', s) }
    case 'rejected':
      return { fill: 'red', shape: 'dot', text: retrying('auth failed', s) }
    case 'revoked':
      return { fill: 'red', shape: 'dot', text: retrying(s.detail || 'revoked', s) }
    case 'error':
      return { fill: 'red', shape: 'dot', text: s.detail || 'error' }
    default:
      return {}
  }
}

function retrying(text, s) {
  return s.retryIn ? `${text}, retry in ${Math.ceil(s.retryIn / 1000)}s` : text
}

module.exports = { statusFor }
