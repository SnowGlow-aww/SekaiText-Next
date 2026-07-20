import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const release = readFileSync(new URL('../.github/workflows/release.yml', import.meta.url), 'utf8')
const manifest = readFileSync(new URL('../.github/workflows/release-manifest.yml', import.meta.url), 'utf8')
const cdnPublisher = readFileSync(new URL('./publish-release-cdn.mjs', import.meta.url), 'utf8')
const marketVerifier = readFileSync(new URL('./verify-plugin-market.mjs', import.meta.url), 'utf8')

test('same-tag immutability is checked before the release action can mutate assets', () => {
  const guard = release.indexOf('node scripts/check-release-mutable.mjs')
  const publisher = release.indexOf('uses: softprops/action-gh-release@')
  assert.ok(guard >= 0 && publisher > guard)
  assert.match(release.slice(publisher, publisher + 500), /overwrite_files:\s*false/)
  assert.match(release, /GH_TOKEN:\s*\$\{\{ github\.token \}\}/)
})

test('published GitHub manifest assets are never clobbered', () => {
  assert.doesNotMatch(manifest, /gh release upload[^\n]*--clobber/)
  assert.match(manifest, /cmp -s [^\n]*app-release\.json/)
})

test('same-version CDN publication verifies exact bytes before the first OSS mutation', () => {
  const check = cdnPublisher.indexOf('assertReleaseCdnPublication(')
  const firstMutation = cdnPublisher.indexOf('oss([')
  assert.ok(check >= 0 && firstMutation > check)
  assert.match(cdnPublisher, /already published byte-for-byte; resuming idempotent artifact verification/)
})

test('manifest-repo publication treats only a clean staged tree as a no-op', () => {
  assert.match(manifest, /if git diff --cached --quiet; then/)
  assert.doesNotMatch(manifest, /git commit[^\n]*\|\|/)
})

test('app release validates the CDN market without owning market publication', () => {
  assert.match(release, /node scripts\/verify-plugin-market\.mjs/)
  assert.doesNotMatch(release, /sync-plugin-market|sync-market:/)
  assert.match(marketVerifier, /requireV3:\s*true/)
})

test('release verification prepares generated Tauri inputs before Rust tests', () => {
  const rustTest = release.indexOf('cargo test --locked --manifest-path src-tauri/Cargo.toml')
  const sidecar = release.indexOf('node scripts/build-go.mjs')
  const enginePath = release.indexOf('mkdir -p src-tauri/resources/engine')
  assert.ok(rustTest >= 0 && sidecar >= 0 && sidecar < rustTest)
  assert.ok(enginePath >= 0 && enginePath < rustTest)
})

test('all external workflow actions are pinned to full commit SHAs', () => {
  for (const [name, source] of [['release', release], ['manifest', manifest]]) {
    for (const match of source.matchAll(/^\s*-\s+uses:\s+([^\s#]+)/gm)) {
      if (match[1].startsWith('./')) continue
      assert.match(match[1], /@[0-9a-f]{40}$/, `${name}: ${match[1]} is not pinned`)
    }
  }
})

test('workflows use supported GitHub concurrency keys', () => {
  assert.doesNotMatch(release + manifest, /^\s*queue:/m)
})
