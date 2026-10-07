'use strict'
// A stand-in for the Quack Quack gateway: verifies device JWTs like the real
// one (signature, id, exp), serves GET /device/elements and the device socket.
const crypto = require('node:crypto')
const http = require('node:http')
const { WebSocketServer } = require('ws')

function verifyJwt(token, publicKey) {
  const [h, p, s] = String(token).split('.')
  if (!s) return null
  const header = JSON.parse(Buffer.from(h, 'base64url'))
  const claims = JSON.parse(Buffer.from(p, 'base64url'))
  const want = publicKey.asymmetricKeyType === 'ec' ? 'ES256' : 'RS256'
  if (header.alg !== want) return null
  const ok = crypto.verify(
    'sha256',
    Buffer.from(`${h}.${p}`),
    want === 'ES256' ? { key: publicKey, dsaEncoding: 'ieee-p1363' } : publicKey,
    Buffer.from(s, 'base64url'),
  )
  if (!ok || typeof claims.exp !== 'number' || claims.exp * 1000 < Date.now()) return null
  return claims
}

async function startGateway({ deviceId, publicKey, elements = [], legacy = false }) {
  const g = {
    deviceId,
    elements,
    received: [], // frames from the device
    handshakes: [], // claims of each accepted socket
    sockets: new Set(),
    reject: false, // answer 403 to the next handshakes
    elementRequests: 0,
  }
  const auth = (req) => {
    const m = /^Bearer (.+)$/.exec(req.headers.authorization ?? '')
    const claims = m && verifyJwt(m[1], publicKey)
    return claims && claims.id === deviceId && !g.reject ? claims : null
  }
  const wss = new WebSocketServer({ noServer: true })
  const server = http.createServer((req, res) => {
    if (req.url === '/device/elements' && !legacy) {
      g.elementRequests++
      if (!auth(req)) {
        res.writeHead(403).end('Device authentication failed')
        return
      }
      res.writeHead(200, { 'Content-Type': 'application/json' })
      res.end(JSON.stringify({ device: { id: deviceId, name: 'Test device' }, elements: g.elements }))
      return
    }
    // like the real server's web app fallback
    res.writeHead(200, { 'Content-Type': 'text/html' }).end('<!doctype html><title>Quack</title>')
  })
  server.on('upgrade', (req, socket, head) => {
    const claims = req.url === '/device/node_red/' && auth(req)
    if (!claims) {
      socket.end('HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n')
      return
    }
    wss.handleUpgrade(req, socket, head, (ws) => {
      g.handshakes.push(claims)
      g.sockets.add(ws)
      ws.on('close', () => g.sockets.delete(ws))
      ws.on('message', (d) => g.received.push(JSON.parse(d.toString())))
    })
  })
  await new Promise((r) => server.listen(0, '127.0.0.1', r))
  g.url = `http://127.0.0.1:${server.address().port}`
  /** Sends a frame to every connected device socket. */
  g.broadcast = (frame) => {
    for (const ws of g.sockets) ws.send(JSON.stringify(frame))
  }
  g.closeAll = (code, reason) => {
    for (const ws of g.sockets) ws.close(code, reason)
  }
  g.stop = () =>
    new Promise((r) => {
      for (const ws of g.sockets) ws.terminate()
      wss.close()
      server.closeAllConnections?.()
      server.close(() => r())
    })
  return g
}

function newDevice(type = 'ec') {
  const { privateKey, publicKey } =
    type === 'ec'
      ? crypto.generateKeyPairSync('ec', { namedCurve: 'P-256' })
      : crypto.generateKeyPairSync('rsa', { modulusLength: 2048 })
  return {
    deviceId: crypto.randomUUID(),
    privateKeyPem: privateKey.export({ type: 'pkcs8', format: 'pem' }),
    publicKey,
  }
}

/** Polls until fn() is truthy. */
async function until(fn, timeoutMs = 4000, what = 'condition') {
  const end = Date.now() + timeoutMs
  for (;;) {
    const v = await fn()
    if (v) return v
    if (Date.now() > end) throw new Error(`timed out waiting for ${what}`)
    await new Promise((r) => setTimeout(r, 20))
  }
}

module.exports = { startGateway, newDevice, verifyJwt, until }
