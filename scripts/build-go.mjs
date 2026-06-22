import { execSync } from 'child_process'
import { existsSync, mkdirSync, statSync, readdirSync } from 'fs'
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

if (existsSync(outPath) && statSync(outPath).mtimeMs >= newestMtime(backendDir)) {
  console.log(`[build-go] ${binaryName} is up to date, skipping. (Delete it to force rebuild.)`)
  process.exit(0)
}

mkdirSync(binariesDir, { recursive: true })

const ldFlags = target.includes('windows')
  ? '-s -w -H windowsgui'
  : '-s -w'

console.log(`[build-go] Compiling Go backend for ${target} (${info.goos}/${info.goarch})...`)

execSync(
  `go build -ldflags="${ldFlags}" -o "${outPath}" ./cmd/sekaitext/`,
  {
    cwd: backendDir,
    env: { ...process.env, GOOS: info.goos, GOARCH: info.goarch, CGO_ENABLED: '0' },
    stdio: 'inherit',
  },
)

console.log(`[build-go] -> ${outPath}`)
