#!/usr/bin/env node

import { spawn, spawnSync } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { constants as fsConstants, accessSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { createRequire } from 'node:module'
import { homedir } from 'node:os'
import { delimiter, dirname, join, resolve } from 'node:path'
import process from 'node:process'
import { fileURLToPath } from 'node:url'

const scriptPath = fileURLToPath(import.meta.url)
const repoRoot = resolve(dirname(scriptPath), '..')
const requireFromRepo = createRequire(join(repoRoot, 'package.json'))
const supportedJavaMin = 17
const supportedJavaMax = 23
const preferredJavaMajors = [23, 21, 17]
const commonJavaMajorOrder = [23, 21, 17, 22, 20, 19, 18]

export async function runAndroidTauri(args = process.argv.slice(2), options = {}) {
  const platform = options.platform ?? process.platform
  const architecture = options.architecture ?? process.arch
  const baseEnv = { ...process.env, ...(options.env ?? {}) }
  const logger = options.logger ?? console

  const java = selectCompatibleJavaHome({
    env: baseEnv,
    platform,
    architecture,
    javaHomeCommand: options.javaHomeCommand,
    commonJavaHomes: options.commonJavaHomes,
  })

  for (const rejection of java.rejections) {
    logger.warn(`[android:tauri] Ignoring ${rejection.source}=${rejection.javaHome}: ${rejection.reason}`)
  }
  logger.log(`[android:tauri] JAVA_HOME=${java.javaHome} (JDK ${java.major}, ${java.source})`)

  const android = completeAndroidSdkEnvironment(baseEnv, {
    platform,
    homeDirectory: options.homeDirectory,
  })
  logger.log(`[android:tauri] ANDROID_HOME=${android.env.ANDROID_HOME}`)
  logger.log(`[android:tauri] ANDROID_SDK_ROOT=${android.env.ANDROID_SDK_ROOT}`)

  const childEnv = withEphemeralAuthToken(withSelectedJava(android.env, java.javaHome))
  const tauriCliPath = options.tauriCliPath ?? resolveLocalTauriCli()
  return spawnLocalTauriAndroid(args, {
    cwd: options.cwd ?? repoRoot,
    env: childEnv,
    nodeExecutable: options.nodeExecutable ?? process.execPath,
    stdio: options.stdio ?? 'inherit',
    tauriCliPath,
  })
}

export function selectCompatibleJavaHome(options = {}) {
  const env = { ...process.env, ...(options.env ?? {}) }
  const platform = options.platform ?? process.platform
  const architecture = options.architecture ?? process.arch
  const seen = new Set()
  const rejections = []

  const inspectCandidate = (rawJavaHome, source, reportRejection = false) => {
    if (!rawJavaHome || !String(rawJavaHome).trim()) return null
    const javaHome = normalizeJavaHome(String(rawJavaHome), env.HOME)
    if (seen.has(javaHome)) return null
    seen.add(javaHome)

    const inspection = inspectJavaHome(javaHome, env)
    if (inspection.accepted) {
      return {
        javaHome: inspection.javaHome,
        major: inspection.major,
        source,
        rejections,
      }
    }
    if (reportRejection) {
      rejections.push({ source, javaHome, reason: inspection.reason })
    }
    return null
  }

  const explicitCandidates = [
    [env.SEKAI_ANDROID_JAVA_HOME, 'SEKAI_ANDROID_JAVA_HOME'],
    [env.JAVA_HOME, 'JAVA_HOME'],
  ]
  for (const [javaHome, source] of explicitCandidates) {
    const selected = inspectCandidate(javaHome, source, true)
    if (selected) return selected
  }

  if (platform === 'darwin') {
    const javaHomeCommand = options.javaHomeCommand === undefined
      ? '/usr/libexec/java_home'
      : options.javaHomeCommand
    if (javaHomeCommand) {
      for (const major of preferredJavaMajors) {
        const discovered = discoverMacJavaHome(javaHomeCommand, major, env)
        const selected = inspectCandidate(discovered, `/usr/libexec/java_home -v ${major}`)
        if (selected) return selected
      }
    }
  }

  const commonJavaHomes = options.commonJavaHomes === undefined
    ? commonJavaHomeCandidates({ env, platform, architecture })
    : options.commonJavaHomes
  for (const candidate of commonJavaHomes) {
    const rawJavaHome = typeof candidate === 'string' ? candidate : candidate.javaHome
    const source = typeof candidate === 'string' ? 'common JDK path' : candidate.source
    const selected = inspectCandidate(rawJavaHome, source)
    if (selected) return selected
  }

  const rejected = rejections.length > 0
    ? ` Rejected environment candidates: ${rejections.map(item => `${item.source} (${item.reason})`).join('; ')}.`
    : ''
  throw new Error(
    `No compatible Android JDK was found. Install JDK 17, 21, or 23, or set SEKAI_ANDROID_JAVA_HOME to its JAVA_HOME. `
    + `Only JDK majors ${supportedJavaMin} through ${supportedJavaMax} are accepted.${rejected}`,
  )
}

export function inspectJavaHome(rawJavaHome, env = process.env) {
  const javaHome = normalizeJavaHome(rawJavaHome, env.HOME)
  if (!isDirectory(javaHome)) {
    return { accepted: false, javaHome, reason: 'directory does not exist' }
  }

  const executableSuffix = process.platform === 'win32' ? '.exe' : ''
  const javaPath = join(javaHome, 'bin', `java${executableSuffix}`)
  const javacPath = join(javaHome, 'bin', `javac${executableSuffix}`)
  if (!isExecutableFile(javaPath)) {
    return { accepted: false, javaHome, reason: `missing executable ${javaPath}` }
  }
  if (!isExecutableFile(javacPath)) {
    return { accepted: false, javaHome, reason: `missing JDK compiler ${javacPath}` }
  }

  const result = spawnSync(javaPath, ['-version'], {
    encoding: 'utf8',
    env: { ...process.env, ...env, JAVA_HOME: javaHome },
    shell: false,
    timeout: 10_000,
    windowsHide: true,
  })
  if (result.error) {
    return { accepted: false, javaHome, reason: `java -version failed: ${result.error.message}` }
  }
  if (result.status !== 0) {
    const detail = `${result.stderr ?? ''}${result.stdout ?? ''}`.trim()
    return {
      accepted: false,
      javaHome,
      reason: `java -version exited with code ${result.status}${detail ? `: ${detail}` : ''}`,
    }
  }

  const versionOutput = `${result.stderr ?? ''}\n${result.stdout ?? ''}`
  const major = parseJavaMajor(versionOutput) ?? parseJavaReleaseFile(javaHome)
  if (major === null) {
    return { accepted: false, javaHome, reason: 'could not determine the JDK major version' }
  }
  if (major < supportedJavaMin || major > supportedJavaMax) {
    return {
      accepted: false,
      javaHome,
      major,
      reason: `JDK ${major} is outside the supported ${supportedJavaMin}-${supportedJavaMax} range`,
    }
  }

  return { accepted: true, javaHome, major }
}

export function completeAndroidSdkEnvironment(rawEnv = process.env, options = {}) {
  const env = { ...rawEnv }
  const platform = options.platform ?? process.platform
  const homeDirectory = options.homeDirectory ?? env.HOME ?? homedir()
  let androidHome = cleanPathValue(env.ANDROID_HOME, homeDirectory)
  let androidSdkRoot = cleanPathValue(env.ANDROID_SDK_ROOT, homeDirectory)

  if (!androidHome && !androidSdkRoot) {
    if (platform === 'darwin') {
      androidHome = join(homeDirectory, 'Library', 'Android', 'sdk')
    } else if (platform === 'linux') {
      androidHome = join(homeDirectory, 'Android', 'Sdk')
    } else {
      throw new Error(
        'ANDROID_HOME and ANDROID_SDK_ROOT are unset, and no default Android SDK path is defined for this platform. Set both variables to an existing Android SDK directory.',
      )
    }
    androidSdkRoot = androidHome
  } else if (!androidHome) {
    androidHome = androidSdkRoot
  } else if (!androidSdkRoot) {
    androidSdkRoot = androidHome
  }

  for (const [name, sdkPath] of [['ANDROID_HOME', androidHome], ['ANDROID_SDK_ROOT', androidSdkRoot]]) {
    if (!isDirectory(sdkPath)) {
      throw new Error(
        `${name} does not point to an existing Android SDK directory: ${sdkPath}. `
        + `Install the Android SDK there or set ANDROID_HOME and ANDROID_SDK_ROOT correctly.`,
      )
    }
  }

  return {
    env: {
      ...env,
      ANDROID_HOME: androidHome,
      ANDROID_SDK_ROOT: androidSdkRoot,
    },
  }
}

export function spawnLocalTauriAndroid(args, options) {
  return new Promise((resolvePromise, reject) => {
    const child = spawn(
      options.nodeExecutable,
      [options.tauriCliPath, 'android', ...args],
      {
        cwd: options.cwd,
        env: options.env,
        shell: false,
        stdio: options.stdio,
      },
    )
    const signalHandlers = new Map()
    let settled = false

    const cleanup = () => {
      for (const [signal, handler] of signalHandlers) {
        process.removeListener(signal, handler)
      }
    }
    const rejectOnce = error => {
      if (settled) return
      settled = true
      cleanup()
      reject(error)
    }
    const resolveOnce = status => {
      if (settled) return
      settled = true
      cleanup()
      resolvePromise(status)
    }

    for (const signal of forwardableSignals()) {
      const handler = () => {
        if (child.exitCode !== null || child.signalCode !== null) return
        try {
          child.kill(signal)
        } catch {
          // The child may have exited between the status check and kill().
        }
      }
      signalHandlers.set(signal, handler)
      process.on(signal, handler)
    }

    child.once('error', rejectOnce)
    child.once('exit', (code, signal) => resolveOnce({ code, signal }))
  })
}

export function parseJavaMajor(output) {
  const match = String(output).match(/\bversion\s+"([^"]+)"/iu)
  if (!match) return null
  const parts = match[1].split(/[._+-]/u)
  const first = Number.parseInt(parts[0], 10)
  if (!Number.isInteger(first)) return null
  if (first === 1 && parts.length > 1) {
    const legacyMajor = Number.parseInt(parts[1], 10)
    return Number.isInteger(legacyMajor) ? legacyMajor : null
  }
  return first
}

function resolveLocalTauriCli() {
  try {
    return requireFromRepo.resolve('@tauri-apps/cli/tauri.js')
  } catch (error) {
    throw new Error(
      `Local @tauri-apps/cli was not found. Run npm install before using Android commands (${error.message}).`,
    )
  }
}

function withSelectedJava(env, javaHome) {
  const javaBin = join(javaHome, 'bin')
  const existingPath = env.PATH ?? ''
  const pathParts = existingPath.split(delimiter).filter(Boolean).filter(path => path !== javaBin)
  return {
    ...env,
    JAVA_HOME: javaHome,
    PATH: [javaBin, ...pathParts].join(delimiter),
  }
}

function withEphemeralAuthToken(env) {
  if (env.SEKAI_TEXT_AUTH_TOKEN) return env
  return {
    ...env,
    SEKAI_TEXT_AUTH_TOKEN: randomBytes(32).toString('base64url'),
  }
}

function discoverMacJavaHome(command, major, env) {
  const result = spawnSync(command, ['-v', String(major)], {
    encoding: 'utf8',
    env,
    shell: false,
    timeout: 10_000,
    windowsHide: true,
  })
  if (result.error || result.status !== 0) return null
  return String(result.stdout ?? '').trim().split(/\r?\n/u).filter(Boolean)[0] ?? null
}

function commonJavaHomeCandidates({ env, platform, architecture }) {
  const candidates = []
  const add = (javaHome, source) => {
    if (javaHome) candidates.push({ javaHome, source })
  }
  const brewPrefixes = unique([
    env.HOMEBREW_PREFIX,
    platform === 'linux' ? '/home/linuxbrew/.linuxbrew' : null,
    '/opt/homebrew',
    '/usr/local',
  ])

  if (platform === 'darwin') {
    for (const major of commonJavaMajorOrder) {
      for (const prefix of brewPrefixes) {
        add(join(prefix, 'opt', `openjdk@${major}`, 'libexec', 'openjdk.jdk', 'Contents', 'Home'), `Homebrew openjdk@${major}`)
      }
      for (const bundle of [
        `jdk-${major}.jdk`,
        `openjdk-${major}.jdk`,
        `temurin-${major}.jdk`,
        `zulu-${major}.jdk`,
        `amazon-corretto-${major}.jdk`,
      ]) {
        add(join('/Library/Java/JavaVirtualMachines', bundle, 'Contents', 'Home'), `macOS system JDK ${major}`)
      }
    }
    for (const prefix of brewPrefixes) {
      add(join(prefix, 'opt', 'openjdk', 'libexec', 'openjdk.jdk', 'Contents', 'Home'), 'Homebrew openjdk')
    }
    for (const root of unique([
      '/Library/Java/JavaVirtualMachines',
      join(env.HOME ?? homedir(), 'Library', 'Java', 'JavaVirtualMachines'),
    ])) {
      for (const entry of sortedJavaDirectoryEntries(root)) {
        add(join(root, entry, 'Contents', 'Home'), `installed macOS JDK (${entry})`)
      }
    }
  } else if (platform === 'linux') {
    for (const major of commonJavaMajorOrder) {
      for (const prefix of brewPrefixes) {
        add(join(prefix, 'opt', `openjdk@${major}`), `Homebrew openjdk@${major}`)
        add(join(prefix, 'opt', `openjdk@${major}`, 'libexec', 'openjdk.jdk', 'Contents', 'Home'), `Homebrew openjdk@${major}`)
      }
      for (const suffix of unique([architecture, architecture === 'arm64' ? 'aarch64' : null, architecture === 'x64' ? 'amd64' : null])) {
        add(join('/usr/lib/jvm', `java-${major}-openjdk-${suffix}`), `Linux OpenJDK ${major}`)
      }
      add(join('/usr/lib/jvm', `java-${major}-openjdk`), `Linux OpenJDK ${major}`)
      add(join('/usr/lib/jvm', `jdk-${major}`), `Linux JDK ${major}`)
      add(join('/usr/lib/jvm', `jdk${major}`), `Linux JDK ${major}`)
      add(join('/usr/java', `jdk-${major}`), `Linux system JDK ${major}`)
    }
    add('/usr/lib/jvm/default-java', 'Linux default JDK')
    for (const root of ['/usr/lib/jvm', '/usr/java']) {
      for (const entry of sortedJavaDirectoryEntries(root)) {
        add(join(root, entry), `installed Linux JDK (${entry})`)
      }
    }
  }

  return candidates
}

function sortedJavaDirectoryEntries(root) {
  try {
    return readdirSync(root, { withFileTypes: true })
      .filter(entry => entry.isDirectory() || entry.isSymbolicLink())
      .map(entry => entry.name)
      .sort((left, right) => javaNameRank(left) - javaNameRank(right) || left.localeCompare(right))
  } catch {
    return []
  }
}

function javaNameRank(name) {
  for (const [index, major] of commonJavaMajorOrder.entries()) {
    if (new RegExp(`(^|[^0-9])${major}([^0-9]|$)`, 'u').test(name)) return index
  }
  return commonJavaMajorOrder.length
}

function parseJavaReleaseFile(javaHome) {
  try {
    const release = readFileSync(join(javaHome, 'release'), 'utf8')
    const match = release.match(/^JAVA_VERSION="([^"]+)"/mu)
    return match ? parseJavaMajor(`version "${match[1]}"`) : null
  } catch {
    return null
  }
}

function normalizeJavaHome(value, homeDirectory = homedir()) {
  let javaHome = cleanPathValue(value, homeDirectory)
  if (!javaHome) return javaHome
  const nestedMacHome = join(javaHome, 'Contents', 'Home')
  if (!isExecutableFile(javaExecutablePath(javaHome)) && isExecutableFile(javaExecutablePath(nestedMacHome))) {
    javaHome = nestedMacHome
  }
  return javaHome
}

function cleanPathValue(value, homeDirectory = homedir()) {
  if (value === undefined || value === null) return null
  let cleaned = String(value).trim()
  if (!cleaned) return null
  const first = cleaned[0]
  if ((first === '"' || first === "'") && cleaned.at(-1) === first) {
    cleaned = cleaned.slice(1, -1)
  }
  if (!cleaned) return null
  if (cleaned === '~') cleaned = homeDirectory
  else if (cleaned.startsWith('~/')) cleaned = join(homeDirectory, cleaned.slice(2))
  return resolve(cleaned)
}

function javaExecutablePath(javaHome) {
  return join(javaHome, 'bin', process.platform === 'win32' ? 'java.exe' : 'java')
}

function isDirectory(path) {
  try {
    return statSync(path).isDirectory()
  } catch {
    return false
  }
}

function isExecutableFile(path) {
  try {
    if (!statSync(path).isFile()) return false
    if (process.platform !== 'win32') accessSync(path, fsConstants.X_OK)
    return true
  } catch {
    return false
  }
}

function unique(values) {
  return [...new Set(values.filter(Boolean))]
}

function forwardableSignals() {
  return process.platform === 'win32' ? ['SIGINT', 'SIGTERM'] : ['SIGHUP', 'SIGINT', 'SIGTERM']
}

function errorMessage(error) {
  return error instanceof Error ? error.message : String(error)
}

async function runAsCli() {
  try {
    const status = await runAndroidTauri()
    if (status.signal) {
      try {
        process.kill(process.pid, status.signal)
      } catch (error) {
        console.error(`[android:tauri] ERROR unable to propagate child signal ${status.signal}: ${errorMessage(error)}`)
        process.exitCode = 1
      }
    } else {
      process.exitCode = status.code ?? 1
    }
  } catch (error) {
    console.error(`[android:tauri] ERROR ${errorMessage(error)}`)
    process.exitCode = 1
  }
}

if (process.argv[1] && resolve(process.argv[1]) === scriptPath) {
  await runAsCli()
}
