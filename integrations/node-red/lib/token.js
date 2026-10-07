'use strict'
// Device JWTs, signed with the device's own private key (Node's crypto, no
// JWT library). The server checks `alg` against the key it stores, so the
// algorithm is derived from the key: ECDSA P-256 -> ES256, RSA >= 2048 -> RS256.
const crypto = require('node:crypto')

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

const b64url = (v) => Buffer.from(v).toString('base64url')

/** Parses a PEM private key (PKCS#8, SEC1 or PKCS#1) and picks the JWT algorithm for it. */
function loadPrivateKey(pem) {
  if (typeof pem !== 'string' || !pem.trim()) throw new Error('no private key configured')
  if (/-----BEGIN (RSA )?PUBLIC KEY-----/.test(pem)) {
    throw new Error('this is the public key; paste the device PRIVATE key (the public one goes on the server)')
  }
  let key
  try {
    key = crypto.createPrivateKey(pem)
  } catch {
    throw new Error('the private key is not a valid PEM private key')
  }
  const d = key.asymmetricKeyDetails ?? {}
  if (key.asymmetricKeyType === 'ec') {
    if (d.namedCurve !== 'prime256v1') throw new Error(`EC keys must use the P-256 curve (got ${d.namedCurve})`)
    return { key, alg: 'ES256' }
  }
  if (key.asymmetricKeyType === 'rsa') {
    if (d.modulusLength < 2048) throw new Error(`RSA keys must be at least 2048 bits (got ${d.modulusLength})`)
    return { key, alg: 'RS256' }
  }
  throw new Error(`unsupported key type "${key.asymmetricKeyType}"; use ECDSA P-256 or RSA`)
}

/** Signs {id, iat, exp}: the claims the gateway requires for a device. */
function signDeviceToken({ deviceId, key, alg, lifetimeSec = 3600, now = Date.now() }) {
  if (!UUID_RE.test(deviceId ?? '')) throw new Error('the device ID must be a UUID')
  const iat = Math.floor(now / 1000)
  const input = `${b64url(JSON.stringify({ alg, typ: 'JWT' }))}.${b64url(
    JSON.stringify({ id: deviceId.toLowerCase(), iat, exp: iat + lifetimeSec }),
  )}`
  // JWS wants raw r||s for ECDSA, not DER
  const sig = crypto.sign('sha256', Buffer.from(input), alg === 'ES256' ? { key, dsaEncoding: 'ieee-p1363' } : key)
  return `${input}.${b64url(sig)}`
}

module.exports = { UUID_RE, loadPrivateKey, signDeviceToken }
