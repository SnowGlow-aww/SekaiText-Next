import { createHash, createPublicKey, verify } from 'node:crypto'
import { basename } from 'node:path'

const packageSignatureHeader = 'SekaiText-Plugin-Signature-V1\n'
const metadataSignatureHeader = 'SekaiText-Plugin-Metadata-Signature-V2\n'
const snapshotSignatureHeader = 'SekaiText-Plugin-Market-Snapshot-V1\n'
const signatureAlgorithm = 'ed25519'
const officialPublisher = 'sekaitext-official'
const keyIdPattern = /^[A-Za-z0-9._-]{1,64}$/
const pluginIdPattern = /^[A-Za-z0-9_-]{1,64}$/
const sha256Pattern = /^[0-9a-f]{64}$/
const semverPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/
const ed25519SpkiPrefix = Buffer.from('302a300506032b6570032100', 'hex')
const maxSafeSequence = Number.MAX_SAFE_INTEGER
const maxIndexBytes = 4 << 20
const maxPackageBytes = 128 << 20
const indexFields = new Set([
  'version', 'plugins', 'publisher', 'keyId', 'signatureAlgorithm', 'sequence',
  'expiresAt', 'snapshotSignature',
])
const entryFields = new Set([
  'id', 'name', 'version', 'description', 'author', 'icon', 'minHostVersion',
  'download', 'sha256', 'publisher', 'keyId', 'signatureAlgorithm',
  'packageSignature', 'homepage', 'sequence', 'expiresAt', 'metadataSignature',
])

export function parsePluginPublicKeys(value) {
  let encoded = value
  if (typeof value === 'string') {
    try {
      encoded = JSON.parse(value)
    } catch {
      throw new Error('SEKAITEXT_PLUGIN_PUBLIC_KEYS must be valid JSON')
    }
  }
  if (!plainObject(encoded) || Object.keys(encoded).length === 0) {
    throw new Error('SEKAITEXT_PLUGIN_PUBLIC_KEYS must be a non-empty key map')
  }
  const keys = {}
  for (const [keyId, publicValue] of Object.entries(encoded)) {
    if (!keyIdPattern.test(keyId)) throw new Error(`invalid plugin signing keyId: ${keyId}`)
    const rawKey = decodeCanonicalBase64(publicValue, `public key ${keyId}`)
    if (rawKey.length !== 32) throw new Error(`plugin public key ${keyId} must be 32 bytes`)
    keys[keyId] = rawKey
  }
  return keys
}

export function canonicalPluginPackagePayload(entry) {
  let payload = packageSignatureHeader
  payload += canonicalField('publisher', stringValue(entry.publisher))
  payload += canonicalField('keyId', stringValue(entry.keyId))
  payload += canonicalField('algorithm', stringValue(entry.signatureAlgorithm))
  payload += canonicalField('id', stringValue(entry.id))
  payload += canonicalField('version', stringValue(entry.version))
  payload += canonicalField('download', stringValue(entry.download))
  payload += canonicalField('sha256', stringValue(entry.sha256))
  return Buffer.from(payload, 'utf8')
}

export function canonicalPluginMetadataPayload(entry) {
  let payload = metadataSignatureHeader
  payload += canonicalField('publisher', stringValue(entry.publisher))
  payload += canonicalField('keyId', stringValue(entry.keyId))
  payload += canonicalField('algorithm', stringValue(entry.signatureAlgorithm))
  payload += canonicalField('id', stringValue(entry.id))
  payload += canonicalField('name', stringValue(entry.name))
  payload += canonicalField('version', stringValue(entry.version))
  payload += canonicalField('description', stringValue(entry.description))
  payload += canonicalField('author', stringValue(entry.author))
  payload += canonicalField('icon', stringValue(entry.icon))
  payload += canonicalField('minHostVersion', stringValue(entry.minHostVersion))
  payload += canonicalField('download', stringValue(entry.download))
  payload += canonicalField('sha256', stringValue(entry.sha256))
  payload += canonicalField('homepage', stringValue(entry.homepage))
  payload += canonicalField('sequence', String(entry.sequence ?? 0))
  payload += canonicalField('expiresAt', stringValue(entry.expiresAt))
  return Buffer.from(payload, 'utf8')
}

export function canonicalPluginSnapshotPayload(index) {
  let payload = snapshotSignatureHeader
  payload += canonicalField('publisher', stringValue(index.publisher))
  payload += canonicalField('keyId', stringValue(index.keyId))
  payload += canonicalField('algorithm', stringValue(index.signatureAlgorithm))
  payload += canonicalField('version', String(index.version))
  payload += canonicalField('sequence', String(index.sequence))
  payload += canonicalField('expiresAt', stringValue(index.expiresAt))
  payload += canonicalField('pluginCount', String(index.plugins?.length ?? 0))
  for (const entry of index.plugins ?? []) {
    payload += canonicalField('pluginId', stringValue(entry.id))
    payload += canonicalField('metadataSignature', stringValue(entry.metadataSignature))
  }
  return Buffer.from(payload, 'utf8')
}

export function verifyPluginMarketIndex(index, publicKeys, {
  now = new Date(),
  allowExpired = false,
  requireV3 = false,
} = {}) {
  if (!plainObject(index)) throw new Error('plugin market index must be an object')
  rejectUnknownFields(index, indexFields, 'plugin market index')
  if (index.version !== 2 && index.version !== 3) {
    throw new Error(`plugin market index must use signed schema v2 or v3, got ${index.version ?? 'missing'}`)
  }
  if (requireV3 && index.version !== 3) throw new Error('app release requires a complete signed plugin market v3 snapshot')
  if (!Array.isArray(index.plugins) || index.plugins.length === 0 || index.plugins.length > 1000) {
    throw new Error('plugin market index plugins must be a non-empty bounded array')
  }
  if (!plainObject(publicKeys) || Object.keys(publicKeys).length === 0) {
    throw new Error('plugin market verification requires a non-empty trust map')
  }

  if (index.version === 2) {
    for (const field of ['publisher', 'keyId', 'signatureAlgorithm', 'sequence', 'expiresAt', 'snapshotSignature']) {
      if (Object.hasOwn(index, field)) throw new Error(`plugin market v2 forbids top-level ${field}`)
    }
  } else {
    if (index.publisher !== officialPublisher || index.signatureAlgorithm !== signatureAlgorithm || !keyIdPattern.test(index.keyId || '')) {
      throw new Error('plugin market v3 has invalid snapshot signing metadata')
    }
    if (!Number.isSafeInteger(index.sequence) || index.sequence <= 0 || index.sequence > maxSafeSequence) {
      throw new Error('plugin market v3 has an invalid snapshot sequence')
    }
    const expiresAt = parseCanonicalRFC3339(index.expiresAt)
    if (!expiresAt) throw new Error('plugin market v3 snapshot has an invalid expiresAt')
    if (!allowExpired && expiresAt <= now) throw new Error('plugin market v3 snapshot is expired')
    decodeCanonicalBase64(index.snapshotSignature, 'snapshotSignature', 64)
  }

  const seen = new Set()
  for (const entry of index.plugins) {
    validateEntry(entry, seen, index, now, allowExpired)
    const key = publicKeyFor(publicKeys, entry.keyId, entry.id)
    const packageSignature = decodeCanonicalBase64(entry.packageSignature, `${entry.id} packageSignature`, 64)
    if (!verify(null, canonicalPluginPackagePayload(entry), key, packageSignature)) {
      throw new Error(`plugin ${entry.id} has an invalid package signature`)
    }
    if (index.version === 2) continue
    const metadataSignature = decodeCanonicalBase64(entry.metadataSignature, `${entry.id} metadataSignature`, 64)
    if (!verify(null, canonicalPluginMetadataPayload(entry), key, metadataSignature)) {
      throw new Error(`plugin ${entry.id} has an invalid metadata signature`)
    }
  }
  if (index.version === 3) {
    const key = publicKeyFor(publicKeys, index.keyId, 'snapshot')
    const snapshotSignature = decodeCanonicalBase64(index.snapshotSignature, 'snapshotSignature', 64)
    if (!verify(null, canonicalPluginSnapshotPayload(index), key, snapshotSignature)) {
      throw new Error('plugin market has an invalid snapshot signature')
    }
  }
  return {
    version: index.version,
    keyId: index.version === 3 ? index.keyId : null,
    sequence: index.version === 3 ? index.sequence : 0,
    snapshotSignature: index.version === 3 ? index.snapshotSignature : null,
  }
}

export async function fetchPluginMarketIndex(url, { fetchImpl = fetch, timeoutMs = 30_000 } = {}) {
  const response = await fetchImpl(cacheBusted(url), {
    cache: 'no-store',
    signal: AbortSignal.timeout(timeoutMs),
  })
  if (!response.ok) throw new Error(`plugin market returned HTTP ${response.status}`)
  const bytes = await readLimitedBody(response, maxIndexBytes, 'plugin market index')
  let index
  try {
    index = JSON.parse(bytes.toString('utf8'))
  } catch {
    throw new Error('plugin market index is not valid JSON')
  }
  return { bytes, index }
}

export async function verifyPluginPackages(index, { fetchImpl = fetch, timeoutMs = 120_000 } = {}) {
  for (const entry of index.plugins) {
    const response = await fetchImpl(entry.download, {
      cache: 'no-store',
      signal: AbortSignal.timeout(timeoutMs),
    })
    if (!response.ok) throw new Error(`plugin ${entry.id} package returned HTTP ${response.status}`)
    const declaredSize = Number(response.headers.get('content-length'))
    if (Number.isFinite(declaredSize) && declaredSize > maxPackageBytes) {
      throw new Error(`plugin ${entry.id} package exceeds the size limit`)
    }
    const hash = createHash('sha256')
    let size = 0
    if (!response.body) throw new Error(`plugin ${entry.id} package has no response body`)
    for await (const chunk of response.body) {
      size += chunk.byteLength
      if (size > maxPackageBytes) throw new Error(`plugin ${entry.id} package exceeds the size limit`)
      hash.update(chunk)
    }
    const actual = hash.digest('hex')
    if (actual !== entry.sha256) {
      throw new Error(`plugin ${entry.id} package digest does not match its signed index entry`)
    }
  }
}

function validateEntry(entry, seen, index, now, allowExpired) {
  if (!plainObject(entry)) throw new Error('plugin market entry must be an object')
  rejectUnknownFields(entry, entryFields, `plugin ${entry.id ?? '<unknown>'}`)
  if (!pluginIdPattern.test(entry.id || '') || seen.has(entry.id)) {
    throw new Error('plugin market contains an invalid or duplicate plugin id')
  }
  seen.add(entry.id)
  requireBoundedString(entry.name, 200, `${entry.id} name`, { nonBlank: true })
  requireBoundedString(entry.description, 4000, `${entry.id} description`, { optional: true })
  requireBoundedString(entry.author, 200, `${entry.id} author`, { optional: true })
  requireBoundedString(entry.icon, 100, `${entry.id} icon`, { optional: true })
  if (!validSemver(entry.version)) throw new Error(`plugin ${entry.id} version is not strict semver`)
  if (entry.minHostVersion !== undefined && entry.minHostVersion !== '' && !validSemver(entry.minHostVersion)) {
    throw new Error(`plugin ${entry.id} minHostVersion is not strict semver`)
  }
  requireHttpsURL(entry.download, `${entry.id} download`)
  if (basename(new URL(entry.download).pathname) !== `${entry.id}-${entry.version}.sekplugin`) {
    throw new Error(`plugin ${entry.id} package filename must match its id and version`)
  }
  if (entry.homepage !== undefined && entry.homepage !== '') requireHttpsURL(entry.homepage, `${entry.id} homepage`)
  if (!sha256Pattern.test(entry.sha256 || '')) throw new Error(`plugin ${entry.id} has an invalid sha256`)
  if (entry.publisher !== officialPublisher || entry.signatureAlgorithm !== signatureAlgorithm || !keyIdPattern.test(entry.keyId || '')) {
    throw new Error(`plugin ${entry.id} has invalid signing metadata`)
  }
  if (typeof entry.packageSignature !== 'string' || !entry.packageSignature) {
    throw new Error(`plugin ${entry.id} is missing its package signature`)
  }
  if (index.version === 2) {
    for (const field of ['sequence', 'expiresAt', 'metadataSignature']) {
      if (Object.hasOwn(entry, field)) throw new Error(`plugin ${entry.id} v2 entry forbids ${field}`)
    }
    return
  }
  if (!Number.isSafeInteger(entry.sequence) || entry.sequence <= 0 || entry.sequence > maxSafeSequence) {
    throw new Error(`plugin ${entry.id} has an invalid sequence`)
  }
  const expiresAt = parseCanonicalRFC3339(entry.expiresAt)
  if (!expiresAt) throw new Error(`plugin ${entry.id} signed metadata has an invalid expiresAt`)
  if (!allowExpired && expiresAt <= now) throw new Error(`plugin ${entry.id} signed metadata is expired`)
  if (typeof entry.metadataSignature !== 'string' || !entry.metadataSignature) {
    throw new Error(`plugin ${entry.id} is missing signed v3 metadata`)
  }
  if (entry.publisher !== index.publisher || entry.keyId !== index.keyId ||
      entry.signatureAlgorithm !== index.signatureAlgorithm || entry.sequence !== index.sequence ||
      entry.expiresAt !== index.expiresAt) {
    throw new Error(`plugin ${entry.id} signing metadata does not match the snapshot`)
  }
}

function validSemver(value) {
  if (typeof value !== 'string') return false
  const match = semverPattern.exec(value)
  if (!match) return false
  const maxUint64 = 0xffffffffffffffffn
  return !match.slice(1, 4).some((part) => BigInt(part) > maxUint64)
}

function parseCanonicalRFC3339(value) {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/.test(value)) return null
  const parsed = new Date(value)
  return !Number.isNaN(parsed.valueOf()) && parsed.toISOString().replace('.000Z', 'Z') === value ? parsed : null
}

function requireBoundedString(value, maxBytes, name, { optional = false, nonBlank = false } = {}) {
  if (optional && value === undefined) return
  if (typeof value !== 'string' || Buffer.byteLength(value, 'utf8') > maxBytes || (nonBlank && !value.trim())) {
    throw new Error(`plugin ${name} is invalid`)
  }
}

function requireHttpsURL(value, name) {
  try {
    const parsed = new URL(value)
    if (parsed.protocol !== 'https:' || parsed.username || parsed.password || parsed.hash) throw new Error()
  } catch {
    throw new Error(`plugin ${name} must use HTTPS without credentials or fragments`)
  }
}

function decodeCanonicalBase64(value, name, expectedBytes) {
  if (typeof value !== 'string' || !value) throw new Error(`plugin ${name} is missing`)
  const decoded = Buffer.from(value, 'base64')
  if (decoded.toString('base64') !== value) throw new Error(`plugin ${name} is not canonical Base64`)
  if (expectedBytes != null && decoded.length !== expectedBytes) {
    throw new Error(`plugin ${name} must decode to ${expectedBytes} bytes`)
  }
  return decoded
}

function publicKeyFor(publicKeys, keyId, label) {
  const rawKey = publicKeys[keyId]
  if (!Buffer.isBuffer(rawKey) || rawKey.length !== 32) {
    throw new Error(`plugin ${label} uses untrusted keyId ${keyId}`)
  }
  return createPublicKey({
    key: Buffer.concat([ed25519SpkiPrefix, rawKey]),
    format: 'der',
    type: 'spki',
  })
}

function canonicalField(name, value) {
  return `${name}:${Buffer.byteLength(value, 'utf8')}:${value}\n`
}

function stringValue(value) {
  return typeof value === 'string' ? value : ''
}

function rejectUnknownFields(value, allowed, name) {
  const unknown = Object.keys(value).filter((field) => !allowed.has(field))
  if (unknown.length) throw new Error(`${name} contains unknown fields: ${unknown.join(', ')}`)
}

function plainObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

async function readLimitedBody(response, limit, name) {
  if (!response.body) throw new Error(`${name} has no response body`)
  const chunks = []
  let size = 0
  for await (const chunk of response.body) {
    size += chunk.byteLength
    if (size > limit) throw new Error(`${name} exceeds the size limit`)
    chunks.push(Buffer.from(chunk))
  }
  return Buffer.concat(chunks, size)
}

function cacheBusted(value) {
  const url = new URL(value)
  url.searchParams.set('release-preflight', String(Date.now()))
  return url.toString()
}
