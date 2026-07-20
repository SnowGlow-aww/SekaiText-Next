import { createPrivateKey, createPublicKey, sign, verify } from 'node:crypto'

const sha256Digest = /^sha256:[0-9a-f]{64}$/i
const keyIdPattern = /^[A-Za-z0-9._-]{1,64}$/
const signatureHeader = 'SekaiText-App-Release-Signature-V1\n'
const signatureAlgorithm = 'ed25519'
const ed25519SpkiPrefix = Buffer.from('302a300506032b6570032100', 'hex')

export function buildReleaseManifest(source, { version, assets, urlForAsset }) {
  const downloads = {}
  const artifacts = {}
  for (const asset of assets) {
    const platform = platformForAsset(asset.name)
    if (downloads[platform]) throw new Error(`multiple release assets map to ${platform}`)
    if (!sha256Digest.test(asset.digest || '')) {
      throw new Error(`${asset.name} has no valid sha256 digest`)
    }
    if (!Number.isSafeInteger(asset.size) || asset.size <= 0) {
      throw new Error(`${asset.name} has no valid size`)
    }
    const url = urlForAsset(asset)
    if (typeof url !== 'string' || !url) throw new Error(`${asset.name} has no download URL`)
    downloads[platform] = url
    artifacts[platform] = {
      digest: asset.digest.toLowerCase(),
      size: asset.size,
    }
  }
  return {
    ...source,
    version,
    downloads,
    artifacts,
  }
}

export function verifyReleaseManifest(actual, expected) {
  if (actual.version !== expected.version) {
    throw new Error(`manifest version mismatch: ${actual.version} != ${expected.version}`)
  }
  const platforms = Object.keys(expected.downloads)
  if (Object.keys(actual.downloads || {}).length !== platforms.length) {
    throw new Error('manifest download platforms do not match the release')
  }
  if (Object.keys(actual.artifacts || {}).length !== platforms.length) {
    throw new Error('manifest artifact platforms do not match the release')
  }
  for (const platform of platforms) {
    if (actual.downloads?.[platform] !== expected.downloads[platform]) {
      throw new Error(`${platform} manifest download URL does not match the release`)
    }
    const actualArtifact = actual.artifacts?.[platform]
    const expectedArtifact = expected.artifacts[platform]
    if (actualArtifact?.digest !== expectedArtifact.digest || actualArtifact?.size !== expectedArtifact.size) {
      throw new Error(`${platform} manifest digest/size does not match the release`)
    }
  }
  if (!Buffer.from(canonicalReleaseManifestPayload(actual)).equals(Buffer.from(canonicalReleaseManifestPayload(expected)))) {
    throw new Error('manifest signed payload does not match the release')
  }
  if (actual.signature?.algorithm !== expected.signature?.algorithm ||
      actual.signature?.keyId !== expected.signature?.keyId ||
      actual.signature?.value !== expected.signature?.value) {
    throw new Error('manifest signature does not match the release')
  }
}

export function signReleaseManifest(manifest, { keyId, privateKey }) {
  if (!keyIdPattern.test(keyId || '')) throw new Error('invalid app update signing keyId')
  const privateDer = decodeCanonicalBase64(privateKey, 'private key')
  const key = createPrivateKey({ key: privateDer, format: 'der', type: 'pkcs8' })
  if (key.asymmetricKeyType !== 'ed25519') throw new Error('app update signing key is not Ed25519')
  const signed = structuredClone(manifest)
  signed.signature = { algorithm: signatureAlgorithm, keyId, value: '' }
  signed.signature.value = sign(null, canonicalReleaseManifestPayload(signed), key).toString('base64')
  return signed
}

export function verifyReleaseManifestSignature(manifest, publicKey) {
  const rawKey = decodeCanonicalBase64(publicKey, 'public key')
  if (rawKey.length !== 32) throw new Error('app update public key must be 32 bytes')
  const signature = decodeCanonicalBase64(manifest.signature?.value, 'signature')
  if (signature.length !== 64) return false
  const key = createPublicKey({
    key: Buffer.concat([ed25519SpkiPrefix, rawKey]),
    format: 'der',
    type: 'spki',
  })
  return verify(null, canonicalReleaseManifestPayload(manifest), key, signature)
}

export function assertReleasePublicationMutable(targetVersion, currentManifest, publicKeys) {
  const comparison = compareReleaseVersions(targetVersion, currentManifest?.version)
  const trusted = hasTrustedReleaseSignature(currentManifest, publicKeys)
  const legacyUnsigned = currentManifest?.version === '5.9.0' && currentManifest.signature === undefined
  if (!trusted && !legacyUnsigned) throw new Error('published app manifest does not have a trusted valid signature')
  if (comparison <= 0) {
    const relation = comparison === 0 ? 'same-version' : 'non-monotonic'
    throw new Error(`refusing ${relation} release ${targetVersion} before mutating GitHub assets; published manifest is ${currentManifest.version}`)
  }
}

export function assertReleaseCdnPublication(targetManifest, targetBytes, currentManifest, currentBytes, publicKeys) {
  const trusted = hasTrustedReleaseSignature(currentManifest, publicKeys)
  const legacyUnsigned = currentManifest?.version === '5.9.0' && currentManifest.signature === undefined
  if (!trusted && !legacyUnsigned) throw new Error('live app-release.json does not have a trusted valid signature')

  const comparison = compareReleaseVersions(targetManifest.version, currentManifest?.version)
  if (comparison < 0) {
    throw new Error(`refusing non-monotonic publication ${targetManifest.version} over ${currentManifest.version}`)
  }
  if (comparison !== 0) return false
  if (!trusted) throw new Error('legacy unsigned manifests cannot be republished at the same version')
  if (!Buffer.from(currentBytes).equals(Buffer.from(targetBytes))) {
    throw new Error(`refusing changed same-version publication ${targetManifest.version}: authenticated manifest bytes differ`)
  }
  return true
}

export function canonicalReleaseManifestPayload(manifest) {
  const signature = manifest?.signature
  if (signature?.algorithm !== signatureAlgorithm || !keyIdPattern.test(signature?.keyId || '')) {
    throw new Error('manifest has invalid signature metadata')
  }
  const platforms = releasePlatforms(manifest)
  let payload = signatureHeader
  payload += canonicalField('algorithm', signature.algorithm)
  payload += canonicalField('keyId', signature.keyId)
  payload += canonicalField('version', stringField(manifest.version, 'version'))
  payload += canonicalField('notes', optionalStringField(manifest.notes, 'notes'))
  payload += canonicalField('pubDate', optionalStringField(manifest.pubDate, 'pubDate'))
  payload += canonicalField('platformCount', String(platforms.length))
  for (const platform of platforms) {
    const artifact = manifest.artifacts[platform]
    payload += canonicalField('platform', platform)
    payload += canonicalField('download', stringField(manifest.downloads[platform], `${platform} download`))
    payload += canonicalField('digest', stringField(artifact.digest, `${platform} digest`))
    if (!Number.isSafeInteger(artifact.size)) throw new Error(`${platform} size is invalid`)
    payload += canonicalField('size', String(artifact.size))
  }
  return Buffer.from(payload, 'utf8')
}

function releasePlatforms(manifest) {
  if (!manifest?.downloads || Array.isArray(manifest.downloads) || typeof manifest.downloads !== 'object') {
    throw new Error('manifest downloads must be an object')
  }
  if (!manifest?.artifacts || Array.isArray(manifest.artifacts) || typeof manifest.artifacts !== 'object') {
    throw new Error('manifest artifacts must be an object')
  }
  const downloads = Object.keys(manifest.downloads).sort()
  const artifacts = Object.keys(manifest.artifacts).sort()
  if (downloads.length === 0 || JSON.stringify(downloads) !== JSON.stringify(artifacts)) {
    throw new Error('manifest download and artifact platforms do not match')
  }
  return downloads
}

function canonicalField(name, value) {
  return `${name}:${Buffer.byteLength(value, 'utf8')}:${value}\n`
}

function stringField(value, name) {
  if (typeof value !== 'string' || !value) throw new Error(`manifest ${name} is invalid`)
  return value
}

function optionalStringField(value, name) {
  if (value === undefined) return ''
  if (typeof value !== 'string') throw new Error(`manifest ${name} is invalid`)
  return value
}

function decodeCanonicalBase64(value, name) {
  if (typeof value !== 'string' || !value) throw new Error(`app update ${name} is missing`)
  const decoded = Buffer.from(value, 'base64')
  if (decoded.toString('base64') !== value) throw new Error(`app update ${name} is not canonical Base64`)
  return decoded
}

function hasTrustedReleaseSignature(manifest, publicKeys) {
  const keyId = manifest?.signature?.keyId
  if (!publicKeys || Array.isArray(publicKeys) || typeof publicKeys !== 'object' || typeof publicKeys[keyId] !== 'string') {
    return false
  }
  try {
    return verifyReleaseManifestSignature(manifest, publicKeys[keyId])
  } catch {
    return false
  }
}

export function retainedReleaseTags(currentTag, tags) {
  const current = parseReleaseTag(currentTag)
  if (!current) throw new Error(`invalid release tag: ${currentTag}`)
  const previous = [...tags]
    .map((tag) => ({ tag, version: parseStableTag(tag) }))
    .filter((entry) => entry.version && compareVersions(entry.version, current.version) < 0)
    .sort((a, b) => compareVersions(b.version, a.version))[0]
  return new Set(previous ? [currentTag, previous.tag] : [currentTag])
}

export function obsoleteStableReleaseTags(currentTag, tags) {
  const keep = retainedReleaseTags(currentTag, tags)
  const current = parseReleaseTag(currentTag)
  return [...tags].filter((tag) => {
    const version = parseStableTag(tag)
    return version && compareVersions(version, current.version) < 0 && !keep.has(tag)
  })
}

export function compareReleaseVersions(a, b) {
  const left = parseSemanticVersion(a)
  const right = parseSemanticVersion(b)
  for (let i = 0; i < 3; i++) {
    if (left.core[i] !== right.core[i]) return left.core[i] < right.core[i] ? -1 : 1
  }
  if (!left.prerelease.length || !right.prerelease.length) {
    if (left.prerelease.length === right.prerelease.length) return 0
    return left.prerelease.length ? -1 : 1
  }
  for (let i = 0; i < Math.min(left.prerelease.length, right.prerelease.length); i++) {
    const l = left.prerelease[i]
    const r = right.prerelease[i]
    if (l === r) continue
    const ln = /^\d+$/.test(l)
    const rn = /^\d+$/.test(r)
    if (ln && rn) return l.length !== r.length ? (l.length < r.length ? -1 : 1) : (l < r ? -1 : 1)
    if (ln !== rn) return ln ? -1 : 1
    return l < r ? -1 : 1
  }
  if (left.prerelease.length === right.prerelease.length) return 0
  return left.prerelease.length < right.prerelease.length ? -1 : 1
}

function parseSemanticVersion(value) {
  const match = /^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.exec(value)
  if (!match) throw new Error(`invalid release version: ${value}`)
  const core = match.slice(1, 4).map((part) => {
    const number = Number(part)
    if (!Number.isSafeInteger(number)) throw new Error(`release version component is too large: ${value}`)
    return number
  })
  const prerelease = match[4]?.split('.') ?? []
  if (prerelease.some((part) => /^\d+$/.test(part) && part.length > 1 && part.startsWith('0'))) {
    throw new Error(`invalid release prerelease: ${value}`)
  }
  return { core, prerelease }
}

function platformForAsset(name) {
  if (/\.dmg$/i.test(name)) return 'darwin-aarch64'
  if (/\.exe$/i.test(name)) return 'windows-amd64'
  throw new Error(`unsupported release asset: ${name}`)
}

function parseStableTag(tag) {
  const match = /^v(\d+)\.(\d+)\.(\d+)$/.exec(tag)
  return match ? match.slice(1).map(Number) : null
}

function parseReleaseTag(tag) {
  const match = /^v(\d+)\.(\d+)\.(\d+)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/.exec(tag)
  return match ? { version: match.slice(1).map(Number) } : null
}

function compareVersions(a, b) {
  for (let i = 0; i < 3; i++) {
    if (a[i] !== b[i]) return a[i] - b[i]
  }
  return 0
}
