'use strict'
// One device connection to the Quack Quack gateway, shared by every node that
// uses the same device config. It signs a fresh token for each handshake,
// reconnects with backoff, knows the device's elements (to address them by
// name), and guards the server's limits (frame size, the device's rate and
// each element's rate, as listed by /device/elements) so frames are rejected
// loudly here instead of being dropped silently there.
const { EventEmitter } = require('node:events')
const http = require('node:http')
const https = require('node:https')
const WebSocket = require('ws')
const { UUID_RE, loadPrivateKey, signDeviceToken } = require('./token')
const { endpoints } = require('./endpoints')

const MAX_FRAME_BYTES = 64 * 1024 // the gateway closes the socket (1009) above this

class QuackError extends Error {
  constructor(message, code) {
    super(message)
    this.code = code
  }
}

class QuackConnection extends EventEmitter {
  /**
   * @param {object} o
   * @param {string} o.server        http(s):// or ws(s):// base URL of the platform
   * @param {string} o.deviceId      device UUID
   * @param {string} o.privateKey    PEM private key of the device
   * @param {number} [o.lifetimeSec] token lifetime (server caps it, 24 h by default)
   * @param {number} [o.rate]        max frames per second for the whole device (0 = unlimited)
   * @param {object} [o.tls]         extra TLS options for ws/https (ca, cert, key, rejectUnauthorized)
   */
  constructor(o) {
    super()
    this.ep = endpoints(o.server)
    if (!UUID_RE.test(String(o.deviceId ?? '').trim())) throw new Error('the device ID must be a UUID')
    this.deviceId = o.deviceId.trim().toLowerCase()
    const { key, alg } = loadPrivateKey(o.privateKey)
    this.key = key
    this.alg = alg
    this.lifetimeSec = o.lifetimeSec ?? 3600
    this.tls = o.tls ?? {}
    this.rate = o.rate ?? 500
    this.minBackoffMs = o.minBackoffMs ?? 1000
    this.maxBackoffMs = o.maxBackoffMs ?? 30_000
    this.slowRetryMs = o.slowRetryMs ?? 60_000 // after 403 / revoked: the admin has to act first
    this.livenessMs = o.livenessMs ?? 75_000 // the server pings every 30 s
    this.requestTimeoutMs = o.requestTimeoutMs ?? 10_000

    this.state = 'idle'
    this.detail = ''
    this.device = null
    this.elements = null // null until loaded; [] when the server has none
    this.elementsSupported = true
    this.running = false
    this.gen = 0 // bumped by start/stop so a late close of an old socket can't reconnect
    this.ws = null
    this.attempt = 0
    this.bucket = this.rate
    this.bucketAt = Date.now()
    this.elementBuckets = new Map() // element id -> { tokens, at }
    this.lastRefresh = 0
  }

  token() {
    return signDeviceToken({ deviceId: this.deviceId, key: this.key, alg: this.alg, lifetimeSec: this.lifetimeSec })
  }

  get connected() {
    return this.state === 'connected' && this.ws?.readyState === WebSocket.OPEN
  }

  start() {
    if (this.running) return
    this.running = true
    this.gen++
    this.attempt = 0
    this._connect()
  }

  /** Closes the socket and stops reconnecting. Resolves once the socket is closed. */
  stop() {
    this.running = false
    this.gen++
    clearTimeout(this.retryTimer)
    clearTimeout(this.liveTimer)
    const ws = this.ws
    this._setState('stopped')
    if (!ws || ws.readyState === WebSocket.CLOSED) return Promise.resolve()
    return new Promise((resolve) => {
      const force = setTimeout(() => {
        ws.terminate()
        resolve()
      }, 2000)
      ws.once('close', () => {
        clearTimeout(force)
        resolve()
      })
      if (ws.readyState === WebSocket.CONNECTING) ws.terminate()
      else ws.close(1000, 'client stopping')
    })
  }

  _setState(state, detail = '', extra = {}) {
    this.state = state
    this.detail = detail
    this.emit('status', { state, detail, ...extra })
  }

  _backoff(slow) {
    const base = Math.min(this.maxBackoffMs, this.minBackoffMs * 2 ** this.attempt)
    this.attempt++
    const jittered = Math.round(base * (0.5 + Math.random() * 0.5))
    return slow ? Math.max(jittered, this.slowRetryMs) : jittered
  }

  _connect() {
    if (!this.running) return
    const gen = this.gen
    this._setState('connecting')
    let token
    try {
      token = this.token()
    } catch (err) {
      this._setState('error', err.message)
      return
    }
    let lastError = null
    const ws = new WebSocket(this.ep.ws, {
      headers: { Authorization: `Bearer ${token}` },
      handshakeTimeout: this.requestTimeoutMs,
      perMessageDeflate: false,
      maxPayload: 4 * 1024 * 1024,
      ...this.tls,
    })
    this.ws = ws

    ws.on('open', () => {
      if (gen !== this.gen) return ws.terminate()
      this.attempt = 0
      this._arm(ws)
      this._setState('connected')
      this.refreshElements(token).catch((err) => this.emit('warn', `could not load the device's elements: ${err.message}`))
    })
    ws.on('ping', () => gen === this.gen && this._arm(ws))
    ws.on('message', (data, isBinary) => {
      if (gen !== this.gen) return
      this._arm(ws)
      if (isBinary) return
      let f
      try {
        f = JSON.parse(data.toString())
      } catch {
        return
      }
      if (!f || typeof f !== 'object' || typeof f.element_id !== 'string') return
      this.emit('frame', this._decorate(f))
    })
    ws.on('error', (err) => {
      lastError = err
    })
    ws.on('close', (code, reasonBuf) => {
      if (this.ws === ws) this.ws = null
      if (gen !== this.gen) return // stopped (and maybe restarted) meanwhile
      clearTimeout(this.liveTimer)
      const reason = reasonBuf?.toString() || ''
      const httpErr = /Unexpected server response: (\d+)/.exec(lastError?.message ?? '')
      let state = 'disconnected'
      let detail = reason || lastError?.message || `closed (${code})`
      let slow = false
      if (httpErr?.[1] === '403') {
        state = 'rejected'
        detail = 'authentication failed: check the device ID, the private key, and that the key is active'
        slow = true
      } else if (httpErr) {
        detail = `server answered HTTP ${httpErr[1]}`
      } else if (code === 4000) {
        state = 'revoked'
        detail = reason || 'access revoked'
        slow = true
      } else if (code === 1000 && reason === 'key changed') {
        this.attempt = 0 // key rotation: reconnect promptly with a fresh token
      } else if (this.liveTimedOut) {
        detail = 'no ping from the server'
      }
      this.liveTimedOut = false
      const retryIn = this._backoff(slow)
      this._setState(state, detail, { code, retryIn })
      this.retryTimer = setTimeout(() => this._connect(), retryIn)
    })
  }

  /** Half-open TCP detection: the server pings every 30 s; silence means the link is gone. */
  _arm(ws) {
    clearTimeout(this.liveTimer)
    this.liveTimer = setTimeout(() => {
      this.liveTimedOut = true
      ws.terminate()
    }, this.livenessMs)
  }

  _decorate(f) {
    const auth = f.auth && typeof f.auth === 'object' ? f.auth : {}
    const kind =
      typeof auth.user_id === 'number' ? 'user' : String(auth.user_id ?? '').toLowerCase() === this.deviceId ? 'device' : 'unknown'
    return {
      elementId: f.element_id,
      element: this.elements?.find((e) => e.id === f.element_id)?.name ?? null,
      message: f.message,
      from: { kind, user_id: auth.user_id ?? null, username: auth.username ?? null },
      lastEditAt: f.last_edit_at ?? null,
    }
  }

  // --- elements ---

  _get(url, token) {
    const lib = url.startsWith('https:') ? https : http
    return new Promise((resolve, reject) => {
      const req = lib.get(
        url,
        { headers: { Authorization: `Bearer ${token}`, Accept: 'application/json' }, timeout: this.requestTimeoutMs, ...this.tls },
        (res) => {
          let body = ''
          res.setEncoding('utf8')
          res.on('data', (c) => (body += c))
          res.on('end', () => resolve({ status: res.statusCode, body }))
        },
      )
      req.on('timeout', () => req.destroy(new Error('request timed out')))
      req.on('error', reject)
    })
  }

  /** Loads the device's own elements (GET /device/elements, same JWT as the socket). */
  async refreshElements(token = this.token()) {
    this.lastRefresh = Date.now()
    const res = await this._get(this.ep.elements, token)
    if (res.status === 404) {
      // a server older than this endpoint: names can't be resolved, UUIDs still work
      this.elementsSupported = false
      throw new Error('the server does not list elements (use element IDs)')
    }
    if (res.status === 403) throw new Error('authentication failed')
    if (res.status !== 200) throw new Error(`HTTP ${res.status}`)
    let data
    try {
      data = JSON.parse(res.body)
    } catch {
      // an older server answers with its web app instead of JSON
      this.elementsSupported = false
      throw new Error('the server does not list elements (use element IDs)')
    }
    this.device = data.device ?? null
    this.elements = Array.isArray(data.elements) ? data.elements : []
    this.emit('elements', this.elements)
    return { device: this.device, elements: this.elements }
  }

  _findByName(name) {
    const want = name.trim().toLowerCase()
    const hits = (this.elements ?? []).filter((e) => e.name.trim().toLowerCase() === want)
    if (hits.length > 1) throw new QuackError(`more than one element is named "${name}"; use its ID`, 'ambiguous')
    return hits[0]?.id
  }

  /**
   * Turns an element ID or name into an ID of this device. Unknown references
   * trigger one (throttled) reload, since admins can add elements while the
   * device is connected.
   */
  async resolveElement(ref) {
    const r = String(ref ?? '').trim()
    if (!r) throw new QuackError('no element given', 'element')
    const isId = UUID_RE.test(r)
    const lookup = () => (isId ? this.elements?.find((e) => e.id === r.toLowerCase())?.id : this._findByName(r))
    if (isId && (!this.elementsSupported || this.elements === null)) return r.toLowerCase()
    let id = lookup()
    if (!id && this.elementsSupported && Date.now() - this.lastRefresh > 5000) {
      await this.refreshElements().catch(() => {})
      id = lookup()
    }
    if (id) return id
    if (!this.elementsSupported) throw new QuackError(`"${r}" is not an element ID; this server only accepts IDs`, 'element')
    if (this.elements === null) throw new QuackError(`can't resolve "${r}" yet: the element list is not loaded`, 'element')
    const names = this.elements.map((e) => e.name).join(', ') || 'none'
    throw new QuackError(`this device has no element ${isId ? 'with ID' : 'named'} "${r}" (elements: ${names})`, 'element')
  }

  // --- sending ---

  _take() {
    if (!this.rate) return true
    const now = Date.now()
    this.bucket = Math.min(this.rate, this.bucket + ((now - this.bucketAt) / 1000) * this.rate)
    this.bucketAt = now
    if (this.bucket < 1) return false
    this.bucket -= 1
    return true
  }

  /**
   * The server's limit for one element (servers since 2026-10 list it). Only
   * elements set to "drop" are guarded here: for "latest" the server keeps the
   * newest value and sends it when allowed, so nothing is lost.
   */
  _elementLimit(elementId) {
    const e = this.elements?.find((x) => x.id === elementId)
    if (!e || !(e.rate > 0) || e.over_limit !== 'drop') return null
    return { name: e.name, rate: e.rate, burst: e.burst >= 1 ? e.burst : Math.max(1, e.rate) }
  }

  _takeElement(elementId, lim) {
    const now = Date.now()
    const b = this.elementBuckets.get(elementId) ?? { tokens: lim.burst, at: now }
    b.tokens = Math.min(lim.burst, b.tokens + ((now - b.at) / 1000) * lim.rate)
    b.at = now
    this.elementBuckets.set(elementId, b)
    if (b.tokens < 1) return false
    b.tokens -= 1
    return true
  }

  /** Sends one telemetry frame. Throws a QuackError (code offline | rate | element-rate | size | json) instead of dropping silently. */
  send(elementId, message) {
    if (!this.connected) throw new QuackError(`not connected (${this.state}${this.detail ? `: ${this.detail}` : ''})`, 'offline')
    let frame
    try {
      frame = JSON.stringify({ element_id: elementId, message })
    } catch (err) {
      throw new QuackError(`the message is not JSON-serializable: ${err.message}`, 'json')
    }
    const size = Buffer.byteLength(frame)
    if (size > MAX_FRAME_BYTES) throw new QuackError(`frame is ${size} bytes; the server accepts at most ${MAX_FRAME_BYTES}`, 'size')
    const lim = this._elementLimit(elementId)
    if (lim && !this._takeElement(elementId, lim)) {
      throw new QuackError(`element "${lim.name}" allows ${lim.rate} messages/s (set by the server admin); this one was dropped`, 'element-rate')
    }
    if (!this._take()) throw new QuackError(`more than ${this.rate} messages/s for this device; this one was dropped`, 'rate')
    this.ws.send(frame)
  }
}

module.exports = { QuackConnection, QuackError, MAX_FRAME_BYTES }
