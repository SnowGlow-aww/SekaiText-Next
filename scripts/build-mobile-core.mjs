import { access, mkdir, readFile, rename, rm, stat } from 'node:fs/promises'
import { randomUUID } from 'node:crypto'
import { homedir } from 'node:os'
import { dirname, join, posix, resolve, win32 } from 'node:path'
import { spawn } from 'node:child_process'
import process from 'node:process'
import { fileURLToPath } from 'node:url'

const scriptPath = fileURLToPath(import.meta.url)
const repoRoot = resolve(dirname(scriptPath), '..')
const backendDir = join(repoRoot, 'backend')
const outputPath = join(repoRoot, 'src-tauri', 'mobile', 'android', 'libs', 'mobilecore.aar')
const temporaryPath = join(dirname(outputPath), 'mobilecore.next.aar')
const fixedTargets = 'android/arm64'
const fixedAndroidApi = '28'
const fallbackMobileVersion = 'v0.0.0-20260709172247-6129f5bee9d5'
const requiredAarEntries = [
  'classes.jar',
  'jni/arm64-v8a/libgojni.so',
]

export async function main() {
  const sdkRoot = process.env.ANDROID_HOME || process.env.ANDROID_SDK_ROOT || join(homedir(), 'Library', 'Android', 'sdk')
  const ndkVersion = process.env.SEKAI_ANDROID_NDK_VERSION || '27.2.12479018'
  const ndkRoot = process.env.ANDROID_NDK_HOME || join(sdkRoot, 'ndk', ndkVersion)
  const targets = process.env.SEKAI_GOMOBILE_TARGETS ?? fixedTargets
  const androidApi = process.env.SEKAI_ANDROID_MIN_API ?? fixedAndroidApi

  assertFixedAndroidConfig(targets, androidApi)

  const mobileVersion = await readMobileVersion(join(backendDir, 'go.mod'))

  console.log('[mobilecore] locating Go and Android toolchains')
  const goPath = await capture('go', ['env', 'GOPATH'], { cwd: backendDir })
  const gomobile = process.env.GOMOBILE || gomobileExecutablePath(goPath.trim(), process.platform)
  await Promise.all([
    requirePath(gomobile, `gomobile (install with: go install golang.org/x/mobile/cmd/gomobile@${mobileVersion})`),
    requirePath(sdkRoot, 'Android SDK'),
    requirePath(ndkRoot, `Android NDK ${ndkVersion}`),
  ])

  await mkdir(dirname(outputPath), { recursive: true })
  await rm(temporaryPath, { force: true })

  const args = [
    'bind',
    '-target', targets,
    '-androidapi', androidApi,
    '-javapkg', 'com.sekaitext.mobilecore',
    '-trimpath',
    '-o', temporaryPath,
    './mobilecore',
  ]

  console.log(`[mobilecore] building ${targets} AAR (min API ${androidApi})`)
  console.log(`[mobilecore] SDK=${sdkRoot}`)
  console.log(`[mobilecore] NDK=${ndkRoot}`)
  try {
    await run(gomobile, args, {
      cwd: backendDir,
      env: {
        ...process.env,
        PATH: prependExecutableDirectory(process.env.PATH || '', gomobile, process.platform),
        ANDROID_HOME: sdkRoot,
        ANDROID_SDK_ROOT: sdkRoot,
        ANDROID_NDK_HOME: ndkRoot,
      },
    })
    console.log(`[mobilecore] validating ${temporaryPath}`)
    const inspector = await validateAar(temporaryPath)
    console.log(`[mobilecore] validated temporary AAR with ${inspector}`)
    // Keep the last known-good AAR until gomobile has produced a complete
    // replacement. POSIX rename overwrites atomically; Windows needs an explicit
    // same-directory backup/restore transaction because rename rejects an existing target.
    await replaceFilePreservingPrevious(temporaryPath, outputPath)
  } catch (error) {
    await rm(temporaryPath, { force: true }).catch(() => {})
    throw error
  }
  console.log(`[mobilecore] wrote ${outputPath}`)
}

export function gomobileExecutablePath(goPath, platform = process.platform) {
  const pathApi = platform === 'win32' ? win32 : posix
  return pathApi.join(goPath, 'bin', platform === 'win32' ? 'gomobile.exe' : 'gomobile')
}

export function prependExecutableDirectory(existingPath, executable, platform = process.platform) {
  const pathApi = platform === 'win32' ? win32 : posix
  const delimiter = platform === 'win32' ? ';' : ':'
  return `${pathApi.dirname(executable)}${existingPath ? delimiter + existingPath : ''}`
}

export async function replaceFilePreservingPrevious(
  temporary,
  destination,
  {
    platform = process.platform,
    accessImpl = access,
    renameImpl = rename,
    rmImpl = rm,
  } = {},
) {
  if (platform !== 'win32') {
    await renameImpl(temporary, destination)
    return
  }

  try {
    await accessImpl(destination)
  } catch (error) {
    if (error?.code !== 'ENOENT') throw error
    await renameImpl(temporary, destination)
    return
  }

  const backup = `${destination}.previous-${randomUUID()}`
  await renameImpl(destination, backup)
  try {
    await renameImpl(temporary, destination)
  } catch (replacementError) {
    try {
      await renameImpl(backup, destination)
    } catch (restoreError) {
      throw new AggregateError(
        [replacementError, restoreError],
        `failed to replace ${destination} and restore its previous file`,
      )
    }
    throw replacementError
  }

  // The new validated artifact is committed. A transient cleanup failure must not
  // turn a successful build into a false failure; the uniquely named backup is safe.
  await rmImpl(backup, { force: true }).catch(() => {})
}

export function assertFixedAndroidConfig(targets, androidApi) {
  const errors = []
  if (targets !== fixedTargets) {
    errors.push(`SEKAI_GOMOBILE_TARGETS must be exactly ${JSON.stringify(fixedTargets)} (received ${JSON.stringify(targets)})`)
  }
  if (androidApi !== fixedAndroidApi) {
    errors.push(`SEKAI_ANDROID_MIN_API must be exactly ${JSON.stringify(fixedAndroidApi)} (received ${JSON.stringify(androidApi)})`)
  }
  if (errors.length > 0) {
    throw new Error(`[mobilecore] refusing to build an AAR that would drift from the fixed Tauri APK configuration (arm64, minSdk 28):\n- ${errors.join('\n- ')}`)
  }
}

export async function readMobileVersion(goModPath) {
  try {
    const goMod = await readFile(goModPath, 'utf8')
    const match = goMod.match(/^\s*golang\.org\/x\/mobile\s+(v\S+)(?:\s+\/\/.*)?$/m)
    return match?.[1] || fallbackMobileVersion
  } catch {
    return fallbackMobileVersion
  }
}

export async function validateAar(aarPath) {
  let fileStats
  try {
    fileStats = await stat(aarPath)
  } catch (error) {
    throw new Error(`temporary AAR validation failed: cannot read ${aarPath}: ${errorMessage(error)}`)
  }

  if (!fileStats.isFile() || fileStats.size === 0) {
    throw new Error(`temporary AAR validation failed: ${aarPath} is not a non-empty file`)
  }

  const { command, output } = await listArchiveEntries(aarPath)
  const entries = new Set(output.split(/\r?\n/u).filter(Boolean))
  const missingEntries = requiredAarEntries.filter(entry => !entries.has(entry))
  if (missingEntries.length > 0) {
    throw new Error(`temporary AAR validation failed: missing required archive entries: ${missingEntries.join(', ')}`)
  }

  return command
}

async function listArchiveEntries(aarPath) {
  const inspectors = [
    { command: 'unzip', args: ['-Z1', aarPath] },
    { command: 'zipinfo', args: ['-1', aarPath] },
    { command: 'jar', args: ['tf', aarPath] },
  ]

  for (const inspector of inspectors) {
    try {
      const output = await capture(inspector.command, inspector.args, {})
      return { command: inspector.command, output }
    } catch (error) {
      if (error?.code === 'ENOENT') continue
      throw new Error(`temporary AAR validation failed: ${inspector.command} could not inspect ${aarPath}: ${errorMessage(error)}`)
    }
  }

  throw new Error('temporary AAR validation failed: no supported archive inspector was found (tried unzip, zipinfo, and jar)')
}

async function requirePath(path, label) {
  try {
    await access(path)
  } catch {
    throw new Error(`${label} was not found at ${path}`)
  }
}

function run(command, args, options) {
  return new Promise((resolvePromise, reject) => {
    const child = spawn(command, args, { ...options, stdio: 'inherit' })
    child.once('error', reject)
    child.once('exit', (code, signal) => {
      if (code === 0) resolvePromise()
      else reject(new Error(`${command} exited with ${signal ? `signal ${signal}` : `code ${code}`}`))
    })
  })
}

function capture(command, args, options) {
  return new Promise((resolvePromise, reject) => {
    const child = spawn(command, args, { ...options, stdio: ['ignore', 'pipe', 'inherit'] })
    let output = ''
    child.stdout.setEncoding('utf8')
    child.stdout.on('data', chunk => { output += chunk })
    child.once('error', reject)
    child.once('exit', (code, signal) => {
      if (code === 0) resolvePromise(output)
      else reject(new Error(`${command} exited with ${signal ? `signal ${signal}` : `code ${code}`}`))
    })
  })
}

function errorMessage(error) {
  return error instanceof Error ? error.message : String(error)
}

if (process.argv[1] && resolve(process.argv[1]) === scriptPath) {
  await main()
}
