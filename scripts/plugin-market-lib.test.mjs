import assert from 'node:assert/strict'
import { createHash, generateKeyPairSync, sign } from 'node:crypto'
import test from 'node:test'

import {
  canonicalPluginMetadataPayload,
  canonicalPluginPackagePayload,
  canonicalPluginSnapshotPayload,
  parsePluginPublicKeys,
  verifyPluginMarketIndex,
  verifyPluginPackages,
} from './plugin-market-lib.mjs'

const packageBytes = Buffer.from('signed plugin package')
const { privateKey, publicKey } = generateKeyPairSync('ed25519')
const rawPublicKey = publicKey.export({ format: 'der', type: 'spki' }).subarray(-32).toString('base64')
const keys = parsePluginPublicKeys(JSON.stringify({ 'test-2026': rawPublicKey }))

function signedIndex({ ids = ['demo'], indexOverrides = {}, entryOverrides = {} } = {}) {
  const index = {
    version: 3,
    plugins: [],
    publisher: 'sekaitext-official',
    keyId: 'test-2026',
    signatureAlgorithm: 'ed25519',
    sequence: 42,
    expiresAt: '2030-01-01T00:00:00Z',
    snapshotSignature: '',
    ...indexOverrides,
  }
  index.plugins = ids.map((id) => {
    const entry = {
      id,
      name: id === 'demo' ? 'Demo' : 'Second',
      version: '1.2.3',
      description: 'Visible metadata',
      author: 'Tester',
      icon: 'Puzzle',
      minHostVersion: '5.9.0',
      download: `https://example.test/${id}-1.2.3.sekplugin`,
      sha256: createHash('sha256').update(packageBytes).digest('hex'),
      publisher: index.publisher,
      keyId: index.keyId,
      signatureAlgorithm: index.signatureAlgorithm,
      packageSignature: '',
      homepage: `https://example.test/${id}`,
      sequence: index.sequence,
      expiresAt: index.expiresAt,
      metadataSignature: '',
      ...entryOverrides,
    }
    entry.packageSignature = sign(null, canonicalPluginPackagePayload(entry), privateKey).toString('base64')
    entry.metadataSignature = sign(null, canonicalPluginMetadataPayload(entry), privateKey).toString('base64')
    return entry
  })
  index.snapshotSignature = sign(null, canonicalPluginSnapshotPayload(index), privateKey).toString('base64')
  return index
}

function signedV2Index() {
  const entry = structuredClone(signedIndex().plugins[0])
  delete entry.sequence
  delete entry.expiresAt
  delete entry.metadataSignature
  return { version: 2, plugins: [entry] }
}

test('canonical plugin package payload matches the Go runtime format', () => {
  const entry = signedIndex().plugins[0]
  assert.equal(canonicalPluginPackagePayload(entry).toString(),
    'SekaiText-Plugin-Signature-V1\n' +
    'publisher:18:sekaitext-official\n' +
    'keyId:9:test-2026\n' +
    'algorithm:7:ed25519\n' +
    'id:4:demo\n' +
    'version:5:1.2.3\n' +
    'download:41:https://example.test/demo-1.2.3.sekplugin\n' +
    `sha256:64:${entry.sha256}\n`)
})

test('canonical snapshot payload covers every plugin in original order', () => {
  const index = signedIndex({ ids: ['demo', 'second'] })
  assert.equal(canonicalPluginSnapshotPayload(index).toString(),
    'SekaiText-Plugin-Market-Snapshot-V1\n' +
    'publisher:18:sekaitext-official\n' +
    'keyId:9:test-2026\n' +
    'algorithm:7:ed25519\n' +
    'version:1:3\n' +
    'sequence:2:42\n' +
    'expiresAt:20:2030-01-01T00:00:00Z\n' +
    'pluginCount:1:2\n' +
    `pluginId:4:demo\nmetadataSignature:88:${index.plugins[0].metadataSignature}\n` +
    `pluginId:6:second\nmetadataSignature:88:${index.plugins[1].metadataSignature}\n`)
})

test('valid v3 snapshot and package bytes verify against the embedded trust map', async () => {
  const index = signedIndex()
  assert.deepEqual(verifyPluginMarketIndex(index, keys, { now: new Date('2029-01-01T00:00:00Z') }), {
    version: 3,
    keyId: 'test-2026',
    sequence: 42,
    snapshotSignature: index.snapshotSignature,
  })
  await assert.doesNotReject(() => verifyPluginPackages(index, {
    fetchImpl: async () => new Response(packageBytes),
  }))
})

test('signed v2 remains a runtime bridge but app release requires v3', () => {
  const index = signedV2Index()
  assert.deepEqual(verifyPluginMarketIndex(index, keys), {
    version: 2,
    keyId: null,
    sequence: 0,
    snapshotSignature: null,
  })
  assert.throws(() => verifyPluginMarketIndex(index, keys, { requireV3: true }), /requires.*v3/i)
})

test('v3 verification rejects empty, expired, or mismatched snapshot metadata', () => {
  const empty = signedIndex()
  empty.plugins = []
  assert.throws(() => verifyPluginMarketIndex(empty, keys), /non-empty/)

  const expired = signedIndex({ indexOverrides: { expiresAt: '2026-01-01T00:00:00Z' } })
  assert.throws(
    () => verifyPluginMarketIndex(expired, keys, { now: new Date('2026-01-02T00:00:00Z') }),
    /expired/,
  )
  assert.doesNotThrow(() => verifyPluginMarketIndex(expired, keys, {
    now: new Date('2026-01-02T00:00:00Z'),
    allowExpired: true,
  }))

  const mismatch = signedIndex()
  mismatch.plugins[0].sequence++
  assert.throws(() => verifyPluginMarketIndex(mismatch, keys), /does not match the snapshot/)
})

test('market display limits count UTF-8 bytes rather than JavaScript code units', () => {
  const valid = signedIndex({ entryOverrides: { name: `${'界'.repeat(66)}aa` } })
  assert.doesNotThrow(() => verifyPluginMarketIndex(valid, keys, { now: new Date('2029-01-01T00:00:00Z') }))

  const invalid = signedIndex({ entryOverrides: { name: '界'.repeat(67) } })
  assert.throws(
    () => verifyPluginMarketIndex(invalid, keys, { now: new Date('2029-01-01T00:00:00Z') }),
    /name.*invalid/,
  )
})

test('snapshot signature rejects member removal, reordering, and metadata tampering', () => {
  const reordered = signedIndex({ ids: ['demo', 'second'] })
  reordered.plugins.reverse()
  assert.throws(() => verifyPluginMarketIndex(reordered, keys), /snapshot signature/)

  const metadataTampered = signedIndex()
  metadataTampered.plugins[0].description = 'tampered'
  assert.throws(() => verifyPluginMarketIndex(metadataTampered, keys), /metadata signature/)

  const unknownKey = signedIndex({ indexOverrides: { keyId: 'unknown' } })
  assert.throws(() => verifyPluginMarketIndex(unknownKey, keys), /untrusted keyId/)
})

test('preflight rejects package bytes that do not match the signed digest', async () => {
  await assert.rejects(() => verifyPluginPackages(signedIndex(), {
    fetchImpl: async () => new Response('tampered package'),
  }), /digest/)
})
