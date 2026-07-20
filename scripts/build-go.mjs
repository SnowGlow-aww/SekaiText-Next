import { execFileSync } from 'child_process'
import { createHash } from 'crypto'
import { existsSync, mkdirSync, statSync, readdirSync, readFileSync, writeFileSync } from 'fs'
import { platform, arch } from 'os'
import { join } from 'path'

// Map target triple (TAURI_TARGET env) -> Go GOOS/GOARCH
const TARGET_MAP = {
  'x86_64-pc-windows-msvc':    { goos: 'windows', goarch: 'amd64', ext: '.exe' },
  'aarch64-pc-windows-msvc':   { goos: 'windows', goarch: 'arm64', ext: '.exe' },
  'x86_64-unknown-linux-gnu':  { goos: 'linux',   goarch: 'amd64', ext: '' },
  'aarch64-unknown-linux-gnu': { goos: 'linux',   goarch: 'arm64', ext: '' },
  'x86_64-apple-darwin':       { goos: 'darwin',  goarch: 'amd64', ext: '' },
  'aarch64-apple-darwin':      { goos: 'darwin',  goarch: 'arm64', ext: '' },
}

const AUTO_DETECT = {
  'win32-x64':   'x86_64-pc-windows-msvc',
  'linux-x64':   'x86_64-unknown-linux-gnu',
  'darwin-x64':  'x86_64-apple-darwin',
  'darwin-arm64':'aarch64-apple-darwin',
}

const target = process.env.TAURI_TARGET || AUTO_DETECT[`${platform()}-${arch()}`] || null

if (!target) {
  console.error('Cannot determine target triple. Set TAURI_TARGET env var.')
  process.exit(1)
}

const info = TARGET_MAP[target]
if (!info) {
  console.error(`Unknown target triple: ${target}`)
  process.exit(1)
}

const binaryName = `sekaitext-backend-${target}${info.ext}`
const binariesDir = join(import.meta.dirname, '..', 'src-tauri', 'binaries')
const outPath = join(binariesDir, binaryName)
const backendDir = join(import.meta.dirname, '..', 'backend')

// Build-time trust root. The value is a JSON object of keyId -> standard-base64
// raw Ed25519 public key. It is linked into the sidecar, never read from the
// runtime environment. Empty is an intentional fail-closed staging state.
function validatedPublicKeys(raw, name) {
  const value = (raw || '').trim()
  if (!value) return ''
  let keys
  try {
    keys = JSON.parse(value)
  } catch {
    throw new Error(`${name} must be a JSON object`)
  }
  if (!keys || Array.isArray(keys) || typeof keys !== 'object') {
    throw new Error(`${name} must be a JSON object`)
  }
  for (const [keyId, encoded] of Object.entries(keys)) {
    if (!/^[A-Za-z0-9._-]{1,64}$/.test(keyId)) {
      throw new Error(`${name} contains an invalid keyId: ${keyId}`)
    }
    if (typeof encoded !== 'string') {
      throw new Error(`public key ${keyId} must be standard Base64`)
    }
    const decoded = Buffer.from(encoded, 'base64')
    if (decoded.length !== 32 || decoded.toString('base64') !== encoded) {
      throw new Error(`public key ${keyId} must be a 32-byte standard-Base64 Ed25519 key`)
    }
  }
  return JSON.stringify(keys)
}

const pluginPublicKeysJSON = validatedPublicKeys(process.env.SEKAITEXT_PLUGIN_PUBLIC_KEYS, 'SEKAITEXT_PLUGIN_PUBLIC_KEYS')
const appUpdatePublicKeysJSON = validatedPublicKeys(process.env.SEKAITEXT_APP_UPDATE_PUBLIC_KEYS, 'SEKAITEXT_APP_UPDATE_PUBLIC_KEYS')
const keyFingerprint = createHash('sha256')
  .update(pluginPublicKeysJSON)
  .update('\0')
  .update(appUpdatePublicKeysJSON)
  .digest('hex')
const keyStatePath = `${outPath}.trust-keys.sha256`

// Rebuild only when the binary is missing or older than any backend source
// file. (Previously this skipped whenever the binary merely existed, which
// silently shipped a stale backend after source changes.)
function newestMtime(dir) {
  let newest = 0
  for (const ent of readdirSync(dir, { withFileTypes: true })) {
    if (ent.name === 'resources' || ent.name === '.git') continue
    const p = join(dir, ent.name)
    if (ent.isDirectory()) {
      newest = Math.max(newest, newestMtime(p))
    } else if (ent.name.endsWith('.go') || ent.name === 'go.mod' || ent.name === 'go.sum') {
      newest = Math.max(newest, statSync(p).mtimeMs)
    }
  }
  return newest
}

const keyStateMatches = existsSync(keyStatePath) && readFileSync(keyStatePath, 'utf8').trim() === keyFingerprint
if (existsSync(outPath) && statSync(outPath).mtimeMs >= newestMtime(backendDir) && keyStateMatches) {
  console.log(`[build-go] ${binaryName} is up to date, skipping. (Delete it to force rebuild.)`)
  process.exit(0)
}

mkdirSync(binariesDir, { recursive: true })

let ldFlags = target.includes('windows')
  ? '-s -w -H windowsgui'
  : '-s -w'
if (pluginPublicKeysJSON) {
  ldFlags += ` -X 'sekaitext/backend/internal/service.OfficialPluginPublicKeysJSON=${pluginPublicKeysJSON}'`
}
if (appUpdatePublicKeysJSON) {
  ldFlags += ` -X 'sekaitext/backend/internal/service.OfficialAppUpdatePublicKeysJSON=${appUpdatePublicKeysJSON}'`
}

console.log(`[build-go] Compiling Go backend for ${target} (${info.goos}/${info.goarch})...`)

execFileSync('go', ['build', `-ldflags=${ldFlags}`, '-o', outPath, './cmd/sekaitext/'], {
  cwd: backendDir,
  env: { ...process.env, GOOS: info.goos, GOARCH: info.goarch, CGO_ENABLED: '0' },
  stdio: 'inherit',
})
writeFileSync(keyStatePath, `${keyFingerprint}\n`, { mode: 0o644 })

console.log(`[build-go] -> ${outPath}`)
