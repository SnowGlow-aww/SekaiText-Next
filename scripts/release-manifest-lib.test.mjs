import assert from 'node:assert/strict'
import { generateKeyPairSync } from 'node:crypto'
import { readFileSync } from 'node:fs'
import test from 'node:test'

import {
  assertReleaseCdnPublication,
  assertReleasePublicationMutable,
  buildReleaseManifest,
  canonicalReleaseManifestPayload,
  compareReleaseVersions,
  obsoleteStableReleaseTags,
  retainedReleaseTags,
  signReleaseManifest,
  verifyReleaseManifest,
  verifyReleaseManifestSignature,
} from './release-manifest-lib.mjs'

const digestA = `sha256:${'a'.repeat(64)}`
const digestB = `sha256:${'b'.repeat(64)}`
const assets = [
  { name: 'SekaiText-aarch64.dmg', url: 'https://github.example/mac', digest: digestA, size: 101 },
  { name: 'SekaiText-x64.exe', url: 'https://github.example/windows', digest: digestB, size: 202 },
]
const { privateKey, publicKey } = generateKeyPairSync('ed25519')
const signing = {
  keyId: 'test-app-update',
  privateKey: privateKey.export({ format: 'der', type: 'pkcs8' }).toString('base64'),
}
const rawPublicKey = publicKey.export({ format: 'der', type: 'spki' }).subarray(-32).toString('base64')
const signatureFixture = JSON.parse(readFileSync(new URL('./fixtures/app-release-signature-v1.json', import.meta.url), 'utf8'))

test('buildReleaseManifest preserves legacy download strings and adds integrity metadata', () => {
  const manifest = buildReleaseManifest(
    { notes: 'release notes', custom: true },
    { version: '6.0.0', assets, urlForAsset: (asset) => asset.url },
  )

  assert.deepEqual(manifest.downloads, {
    'darwin-aarch64': 'https://github.example/mac',
    'windows-amd64': 'https://github.example/windows',
  })
  assert.deepEqual(manifest.artifacts, {
    'darwin-aarch64': { digest: digestA, size: 101 },
    'windows-amd64': { digest: digestB, size: 202 },
  })
  assert.equal(manifest.custom, true)
})

test('buildReleaseManifest rejects assets without a valid GitHub digest or size', () => {
  assert.throws(
    () => buildReleaseManifest({}, { version: '6.0.0', assets: [{ ...assets[0], digest: null }], urlForAsset: () => 'https://cdn.example/file' }),
    /digest/,
  )
  assert.throws(
    () => buildReleaseManifest({}, { version: '6.0.0', assets: [{ ...assets[0], size: 0 }], urlForAsset: () => 'https://cdn.example/file' }),
    /size/,
  )
})

test('verifyReleaseManifest rejects an online manifest missing integrity fields', () => {
  const expected = buildReleaseManifest({}, { version: '6.0.0', assets, urlForAsset: (asset) => asset.url })
  const online = structuredClone(expected)
  delete online.artifacts['windows-amd64'].digest

  assert.throws(() => verifyReleaseManifest(online, expected), /windows-amd64/)
})

test('signReleaseManifest authenticates every update field', () => {
  const manifest = signReleaseManifest(
    buildReleaseManifest(
      { notes: 'Unicode notes: 初音ミク', pubDate: '2026-07-19' },
      { version: '6.0.0', assets, urlForAsset: (asset) => asset.url },
    ),
    signing,
  )
  assert.equal(verifyReleaseManifestSignature(manifest, rawPublicKey), true)

  const tampered = structuredClone(manifest)
  tampered.artifacts['darwin-aarch64'].size++
  assert.equal(verifyReleaseManifestSignature(tampered, rawPublicKey), false)
})

test('shared Go/Node signature fixture remains interoperable', () => {
  assert.equal(canonicalReleaseManifestPayload(signatureFixture.manifest).toString('base64'), signatureFixture.canonicalPayload)
  assert.equal(verifyReleaseManifestSignature(signatureFixture.manifest, signatureFixture.publicKey), true)
})

test('verifyReleaseManifest compares the signed payload and signature', () => {
  const manifest = signReleaseManifest(
    buildReleaseManifest({}, { version: '6.0.0', assets, urlForAsset: (asset) => asset.url }),
    signing,
  )
  assert.doesNotThrow(() => verifyReleaseManifest(structuredClone(manifest), manifest))
  const changed = structuredClone(manifest)
  changed.notes = 'changed after signing'
  assert.throws(() => verifyReleaseManifest(changed, manifest), /signed payload/)
})

test('published app manifests make same-tag retries immutable before asset upload', () => {
  const current = signReleaseManifest(
    buildReleaseManifest({}, { version: '6.0.0', assets, urlForAsset: (asset) => asset.url }),
    signing,
  )
  const trust = { [signing.keyId]: rawPublicKey }
  assert.throws(() => assertReleasePublicationMutable('6.0.0', current, trust), /same-version/)
  assert.throws(() => assertReleasePublicationMutable('5.9.1', current, trust), /non-monotonic/)
  assert.doesNotThrow(() => assertReleasePublicationMutable('6.0.1', current, trust))

  const tampered = structuredClone(current)
  tampered.notes = 'tampered'
  assert.throws(() => assertReleasePublicationMutable('6.0.1', tampered, trust), /trusted valid signature/)
})

test('legacy 5.9.0 can only migrate forward and cannot be republished', () => {
  const legacy = { version: '5.9.0' }
  assert.doesNotThrow(() => assertReleasePublicationMutable('5.9.1', legacy, {}))
  assert.throws(() => assertReleasePublicationMutable('5.9.0', legacy, {}), /same-version/)
})

test('CDN publication accepts only byte-identical authenticated same-version retries', () => {
  const current = signReleaseManifest(
    buildReleaseManifest({}, { version: '6.0.0', assets, urlForAsset: (asset) => asset.url }),
    signing,
  )
  const bytes = Buffer.from(`${JSON.stringify(current, null, 2)}\n`)
  const trust = { [signing.keyId]: rawPublicKey }

  assert.equal(assertReleaseCdnPublication(current, bytes, current, bytes, trust), true)
  assert.throws(
    () => assertReleaseCdnPublication(current, bytes, current, Buffer.from(JSON.stringify(current)), trust),
    /bytes differ/,
  )
  const changed = signReleaseManifest({ ...current, notes: 'changed' }, signing)
  assert.throws(
    () => assertReleaseCdnPublication(current, bytes, changed, Buffer.from(`${JSON.stringify(changed, null, 2)}\n`), trust),
    /bytes differ/,
  )
  assert.throws(
    () => assertReleaseCdnPublication({ ...current, version: '5.9.1' }, bytes, current, bytes, trust),
    /non-monotonic/,
  )
  assert.throws(
    () => assertReleaseCdnPublication(current, bytes, { version: '6.0.0' }, bytes, trust),
    /trusted valid signature/,
  )
})

test('retainedReleaseTags keeps the current and immediately previous stable release', () => {
  assert.deepEqual(
    [...retainedReleaseTags('v5.9.0', ['v5.7.0', 'v5.8.13', 'v5.9.0', 'v5.10.0-beta.1'])],
    ['v5.9.0', 'v5.8.13'],
  )
  assert.deepEqual(
    [...retainedReleaseTags('v5.10.0-beta.1', ['v5.8.13', 'v5.9.0', 'v5.10.0-beta.1'])],
    ['v5.10.0-beta.1', 'v5.9.0'],
  )
})

test('obsoleteStableReleaseTags deletes only stable releases outside the two-version window', () => {
  assert.deepEqual(
    obsoleteStableReleaseTags('v5.10.0-beta.1', [
      'v5.7.0',
      'v5.8.13',
      'v5.9.0',
      'v5.10.0-beta.1',
      'v5.11.0',
      'manual-backup',
    ]),
    ['v5.7.0', 'v5.8.13'],
  )
})

test('compareReleaseVersions follows SemVer precedence', () => {
  assert.equal(compareReleaseVersions('v6.0.0', '5.99.99'), 1)
  assert.equal(compareReleaseVersions('6.0.0-beta.2', '6.0.0-beta.11'), -1)
  assert.equal(compareReleaseVersions('6.0.0-beta.2', 'v6.0.0-beta.2'), 0)
  assert.equal(compareReleaseVersions('6.0.0', '6.0.0-rc.1'), 1)
  assert.equal(compareReleaseVersions('6.0.0+build.2', '6.0.0+build.1'), 0)
  assert.throws(() => compareReleaseVersions('6.0', '5.0.0'), /invalid/)
})
