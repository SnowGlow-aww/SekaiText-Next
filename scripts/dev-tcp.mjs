import { randomBytes } from 'node:crypto'
import { spawn, spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const root = dirname(dirname(fileURLToPath(import.meta.url)))
const backendOnly = process.argv.includes('--backend-only')
const cleanupPorts = backendOnly ? ['9800'] : ['9800', '5173']

const cleanup = spawnSync(process.execPath, [join(root, 'scripts', 'cleanup.mjs'), ...cleanupPorts], {
  cwd: root,
  stdio: 'inherit',
})
if (cleanup.error) throw cleanup.error
if (cleanup.status !== 0) process.exit(cleanup.status ?? 1)

const authToken = process.env.SEKAI_TEXT_AUTH_TOKEN || randomBytes(32).toString('base64url')
const env = { ...process.env, SEKAI_TEXT_AUTH_TOKEN: authToken }
const children = []

children.push(spawn('go', [
  'run', './cmd/sekaitext/', '--dir', '.', '--auth-token', authToken,
], {
  cwd: join(root, 'backend'),
  env,
  stdio: 'inherit',
}))

if (!backendOnly) {
  const npm = process.platform === 'win32' ? 'npm.cmd' : 'npm'
  children.push(spawn(npm, ['run', 'dev'], {
    cwd: root,
    env,
    stdio: 'inherit',
  }))
}

let stopping = false
function stop(signal) {
  if (stopping) return
  stopping = true
  for (const child of children) {
    if (!child.killed) child.kill(signal)
  }
}

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => stop(signal))
}

const exits = children.map((child) => new Promise((resolve) => {
  child.once('error', (error) => resolve({ code: 1, error }))
  child.once('exit', (code, signal) => resolve({ code, signal }))
}))
const result = await Promise.race(exits)
stop('SIGTERM')
if (result.error) console.error(result.error)
process.exitCode = result.code ?? (result.signal ? 1 : 0)
