#!/usr/bin/env node

import { spawn } from 'node:child_process'
import { constants as fsConstants, readFileSync } from 'node:fs'
import { access, mkdir, readdir, rm, stat } from 'node:fs/promises'
import { homedir } from 'node:os'
import { basename, dirname, isAbsolute, join, relative, resolve } from 'node:path'
import process from 'node:process'
import { fileURLToPath } from 'node:url'

const scriptPath = fileURLToPath(import.meta.url)
const repoRoot = resolve(dirname(scriptPath), '..')
const packageMetadata = JSON.parse(readFileSync(join(repoRoot, 'package.json'), 'utf8'))
const apkOutputRoot = join(repoRoot, 'src-tauri', 'gen', 'android', 'app', 'build', 'outputs', 'apk')

export const RELEASE_UNSIGNED_APK = join(
  apkOutputRoot,
  'universal',
  'release',
  'app-universal-release-unsigned.apk',
)
export const DEFAULT_QA_APK = join(
  apkOutputRoot,
  'qa',
  `SekaiText-${packageMetadata.version}-android-arm64-qa.apk`,
)
export const QA_ENV = Object.freeze({
  alias: 'SEKAI_ANDROID_QA_KEY_ALIAS',
  buildTools: 'SEKAI_ANDROID_BUILD_TOOLS',
  keyPassword: 'SEKAI_ANDROID_QA_KEY_PASSWORD',
  keystore: 'SEKAI_ANDROID_QA_KEYSTORE',
  keytool: 'SEKAI_ANDROID_KEYTOOL',
  output: 'SEKAI_ANDROID_QA_OUTPUT',
  storePassword: 'SEKAI_ANDROID_QA_STORE_PASSWORD',
})

export function resolveQaConfig(rawEnv = process.env, options = {}) {
  const env = { ...rawEnv }
  const cwd = options.cwd ?? process.cwd()
  const repositoryRoot = resolve(options.repoRoot ?? repoRoot)
  const homeDirectory = resolve(options.homeDirectory ?? env.HOME ?? homedir())
  const keystore = resolveConfiguredPath(
    env[QA_ENV.keystore] ?? join(homeDirectory, '.android', 'debug.keystore'),
    { cwd, homeDirectory },
  )
  const outputApk = resolveConfiguredPath(env[QA_ENV.output] ?? DEFAULT_QA_APK, {
    cwd,
    homeDirectory,
  })
  const keyAlias = stringSetting(env[QA_ENV.alias], 'androiddebugkey', QA_ENV.alias, true)
  const storePassword = stringSetting(env[QA_ENV.storePassword], 'android', QA_ENV.storePassword)
  const keyPassword = stringSetting(env[QA_ENV.keyPassword], storePassword, QA_ENV.keyPassword)
  const unsignedApk = resolve(options.unsignedApk ?? RELEASE_UNSIGNED_APK)

  assertPathOutsideRepository(keystore, repositoryRoot)
  if (outputApk === unsignedApk) {
    throw new Error('QA output APK must not overwrite the unsigned release input')
  }
  if (outputApk === keystore) {
    throw new Error('QA output APK must not overwrite the signing keystore')
  }
  if (unsignedApk === keystore) {
    throw new Error('Unsigned release input must not be the signing keystore')
  }

  return {
    cwd,
    env,
    homeDirectory,
    keyAlias,
    keyPassword,
    keystore,
    outputApk,
    repositoryRoot,
    storePassword,
    unsignedApk,
  }
}

export async function discoverAndroidBuildTools(options = {}) {
  const env = { ...(options.env ?? process.env) }
  const platform = options.platform ?? process.platform
  const cwd = options.cwd ?? process.cwd()
  const homeDirectory = resolve(options.homeDirectory ?? env.HOME ?? homedir())
  const explicitDirectory = env[QA_ENV.buildTools]
    ? resolveConfiguredPath(env[QA_ENV.buildTools], { cwd, homeDirectory })
    : null

  if (explicitDirectory) {
    const explicitTools = await inspectBuildToolsDirectory(explicitDirectory, platform)
    if (!explicitTools) {
      throw new Error(
        `${QA_ENV.buildTools} must point to an Android SDK build-tools directory containing executable zipalign and apksigner: ${explicitDirectory}`,
      )
    }
    return explicitTools
  }

  const sdkRoots = unique([
    env.ANDROID_HOME,
    env.ANDROID_SDK_ROOT,
    platform === 'darwin' ? join(homeDirectory, 'Library', 'Android', 'sdk') : null,
    platform === 'linux' ? join(homeDirectory, 'Android', 'Sdk') : null,
    platform === 'win32' && env.LOCALAPPDATA ? join(env.LOCALAPPDATA, 'Android', 'Sdk') : null,
  ].map(value => value ? resolveConfiguredPath(value, { cwd, homeDirectory }) : null))

  for (const sdkRoot of sdkRoots) {
    const buildToolsRoot = join(sdkRoot, 'build-tools')
    for (const version of await childDirectoriesNewestFirst(buildToolsRoot)) {
      const tools = await inspectBuildToolsDirectory(join(buildToolsRoot, version), platform, sdkRoot)
      if (tools) return tools
    }
  }

  throw new Error(
    `No Android SDK build-tools installation with zipalign and apksigner was found. Set ANDROID_HOME/ANDROID_SDK_ROOT or ${QA_ENV.buildTools}.`,
  )
}

export function createQaCommandPlan(config, tools, options = {}) {
  const alignedApk = resolve(options.alignedApk ?? join(
    dirname(config.outputApk),
    `.${basename(config.outputApk)}.aligned.apk`,
  ))
  const verifierPath = resolve(options.verifierPath ?? join(config.repositoryRoot, 'scripts', 'verify-android-apk.mjs'))
  const nodeExecutable = options.nodeExecutable ?? process.execPath

  return {
    align: {
      command: tools.zipalign,
      args: ['-f', '-P', '16', '4', config.unsignedApk, alignedApk],
    },
    sign: {
      command: tools.apksigner,
      args: [
        'sign',
        '--ks', config.keystore,
        '--ks-key-alias', config.keyAlias,
        '--ks-pass', `env:${QA_ENV.storePassword}`,
        '--key-pass', `env:${QA_ENV.keyPassword}`,
        '--v4-signing-enabled', 'false',
        '--debuggable-apk-permitted', 'false',
        '--out', config.outputApk,
        alignedApk,
      ],
    },
    verifyAlignment: {
      command: tools.zipalign,
      args: ['-c', '-P', '16', '4', config.outputApk],
    },
    verifySignature: {
      command: tools.apksigner,
      args: ['verify', '--verbose', '--print-certs', '--Werr', config.outputApk],
    },
    verifyContents: {
      command: nodeExecutable,
      args: [verifierPath, config.outputApk],
    },
  }
}

export function createDebugKeystoreCommand(config, keytool) {
  return {
    command: keytool,
    args: [
      '-genkeypair',
      '-noprompt',
      '-keystore', config.keystore,
      '-storetype', 'JKS',
      '-storepass:env', QA_ENV.storePassword,
      '-alias', config.keyAlias,
      '-keypass:env', QA_ENV.keyPassword,
      '-keyalg', 'RSA',
      '-keysize', '2048',
      '-validity', '10000',
      '-dname', 'CN=Android Debug,O=Android,C=US',
    ],
  }
}

export function assertAndroidDebugCertificate(output) {
  const text = String(output)
  const signerCount = text.match(/^Number of signers:\s*(\d+)\s*$/imu)
  const distinguishedNames = [...text.matchAll(/^Signer #\d+ certificate DN:\s*(.+)\s*$/gimu)]
    .map(match => match[1].trim())

  if (!signerCount || Number.parseInt(signerCount[1], 10) !== 1 || distinguishedNames.length !== 1) {
    throw new Error('QA APK must have exactly one inspectable Android Debug signer')
  }
  const identity = new Set(distinguishedNames[0].split(/\s*,\s*/u))
  const expectedIdentity = new Set(['CN=Android Debug', 'O=Android', 'C=US'])
  const exactAndroidDebugIdentity = identity.size === expectedIdentity.size
    && [...expectedIdentity].every(attribute => identity.has(attribute))
  if (!exactAndroidDebugIdentity) {
    throw new Error(
      `Refusing QA artifact signed by ${JSON.stringify(distinguishedNames[0])}: QA signing must use exactly the standard Android Debug identity (CN=Android Debug, O=Android, C=US), never a production or Play Store key`,
    )
  }
  return distinguishedNames[0]
}

export async function runAndroidQa(options = {}) {
  const config = resolveQaConfig(options.env ?? process.env, options)
  const logger = options.logger ?? console
  const execute = options.execute ?? executeCommand
  const tools = options.tools ?? await discoverAndroidBuildTools({
    cwd: config.cwd,
    env: config.env,
    homeDirectory: config.homeDirectory,
    platform: options.platform,
  })
  const alignedApk = join(
    dirname(config.outputApk),
    `.${basename(config.outputApk)}.aligned-${process.pid}-${Date.now()}.apk`,
  )
  const commandPlan = createQaCommandPlan(config, tools, {
    alignedApk,
    nodeExecutable: options.nodeExecutable,
    verifierPath: options.verifierPath,
  })
  const childEnv = qaChildEnvironment(config, tools)
  let completed = false

  logger.log('[android:qa] QA ONLY: using a local Android Debug certificate; this is NOT production or Play Store signing.')
  logger.log(`[android:qa] Unsigned release input: ${config.unsignedApk}`)
  logger.log(`[android:qa] QA output: ${config.outputApk}`)
  logger.log(`[android:qa] Android build-tools: ${tools.directory}`)
  logger.log(`[android:qa] Debug keystore (outside repository): ${config.keystore}`)

  await assertNonEmptyFile(config.unsignedApk, 'Unsigned release APK')
  await mkdir(dirname(config.outputApk), { recursive: true })
  await Promise.all([
    rm(config.outputApk, { force: true }),
    rm(`${config.outputApk}.idsig`, { force: true }),
  ])

  try {
    if (!await isRegularFile(config.keystore)) {
      const keytool = options.keytool ?? await resolveKeytool(config, options.platform)
      const keytoolCommand = createDebugKeystoreCommand(config, keytool)
      await mkdir(dirname(config.keystore), { recursive: true })
      logger.log('[android:qa] Creating the standard local Android debug keystore (QA-only).')
      await execute(keytoolCommand.command, keytoolCommand.args, {
        cwd: config.repositoryRoot,
        env: childEnv,
        shell: false,
        stdio: 'inherit',
      })
    }

    await execute(commandPlan.align.command, commandPlan.align.args, {
      cwd: config.repositoryRoot,
      env: childEnv,
      shell: false,
      stdio: 'inherit',
    })
    await execute(commandPlan.sign.command, commandPlan.sign.args, {
      cwd: config.repositoryRoot,
      env: childEnv,
      shell: false,
      stdio: 'inherit',
    })
    await execute(commandPlan.verifyAlignment.command, commandPlan.verifyAlignment.args, {
      cwd: config.repositoryRoot,
      env: childEnv,
      shell: false,
      stdio: 'inherit',
    })
    const signatureResult = await execute(
      commandPlan.verifySignature.command,
      commandPlan.verifySignature.args,
      {
        capture: true,
        cwd: config.repositoryRoot,
        env: childEnv,
        shell: false,
      },
    )
    printCaptured(signatureResult, logger)
    const certificateDn = assertAndroidDebugCertificate(
      `${signatureResult.stdout ?? ''}\n${signatureResult.stderr ?? ''}`,
    )
    logger.log(`[android:qa] Confirmed QA-only signer: ${certificateDn}`)

    await execute(commandPlan.verifyContents.command, commandPlan.verifyContents.args, {
      cwd: config.repositoryRoot,
      env: childEnv,
      shell: false,
      stdio: 'inherit',
    })
    completed = true
    logger.log(`[android:qa] PASS QA APK: ${config.outputApk}`)
    return config.outputApk
  } finally {
    await rm(alignedApk, { force: true })
    if (!completed) {
      await Promise.all([
        rm(config.outputApk, { force: true }),
        rm(`${config.outputApk}.idsig`, { force: true }),
      ])
    }
  }
}

async function resolveKeytool(config, platform = process.platform) {
  const executable = platform === 'win32' ? 'keytool.exe' : 'keytool'
  const explicit = config.env[QA_ENV.keytool]
  if (explicit) {
    const explicitPath = resolveConfiguredPath(explicit, {
      cwd: config.cwd,
      homeDirectory: config.homeDirectory,
    })
    if (!await isExecutableFile(explicitPath, platform)) {
      throw new Error(`${QA_ENV.keytool} is not executable: ${explicitPath}`)
    }
    return explicitPath
  }

  for (const javaHome of unique([
    config.env.SEKAI_ANDROID_JAVA_HOME,
    config.env.JAVA_HOME,
  ])) {
    const candidate = join(resolveConfiguredPath(javaHome, {
      cwd: config.cwd,
      homeDirectory: config.homeDirectory,
    }), 'bin', executable)
    if (await isExecutableFile(candidate, platform)) return candidate
  }
  return executable
}

async function inspectBuildToolsDirectory(directory, platform, sdkRoot = dirname(dirname(directory))) {
  const zipalignName = platform === 'win32' ? 'zipalign.exe' : 'zipalign'
  const apksignerName = platform === 'win32' ? 'apksigner.bat' : 'apksigner'
  const zipalign = join(directory, zipalignName)
  const apksigner = join(directory, apksignerName)
  if (!await isExecutableFile(zipalign, platform) || !await isExecutableFile(apksigner, platform)) return null
  return {
    apksigner,
    directory,
    sdkRoot,
    version: basename(directory),
    zipalign,
  }
}

async function childDirectoriesNewestFirst(root) {
  try {
    const entries = await readdir(root, { withFileTypes: true })
    return entries
      .filter(entry => entry.isDirectory())
      .map(entry => entry.name)
      .sort((left, right) => right.localeCompare(left, undefined, { numeric: true }))
  } catch {
    return []
  }
}

async function isExecutableFile(path, platform) {
  try {
    if (!(await stat(path)).isFile()) return false
    if (platform !== 'win32') await access(path, fsConstants.X_OK)
    return true
  } catch {
    return false
  }
}

async function isRegularFile(path) {
  try {
    return (await stat(path)).isFile()
  } catch {
    return false
  }
}

async function assertNonEmptyFile(path, label) {
  let fileStat
  try {
    fileStat = await stat(path)
  } catch (error) {
    if (error?.code === 'ENOENT') throw new Error(`${label} does not exist: ${path}`)
    throw error
  }
  if (!fileStat.isFile()) throw new Error(`${label} is not a file: ${path}`)
  if (fileStat.size === 0) throw new Error(`${label} is empty: ${path}`)
}

function qaChildEnvironment(config, tools) {
  const env = {
    ...config.env,
    [QA_ENV.storePassword]: config.storePassword,
    [QA_ENV.keyPassword]: config.keyPassword,
  }
  if (tools.sdkRoot) {
    env.ANDROID_HOME ||= tools.sdkRoot
    env.ANDROID_SDK_ROOT ||= tools.sdkRoot
  }
  return env
}

function resolveConfiguredPath(value, options) {
  let path = String(value).trim()
  if (!path) throw new Error('Configured path must not be empty')
  if (path === '~') path = options.homeDirectory
  else if (path.startsWith('~/') || path.startsWith('~\\')) path = join(options.homeDirectory, path.slice(2))
  return resolve(options.cwd, path)
}

function assertPathOutsideRepository(path, repositoryRoot) {
  const pathRelativeToRepository = relative(repositoryRoot, path)
  const insideRepository = pathRelativeToRepository === ''
    || (!pathRelativeToRepository.startsWith('..') && !isAbsolute(pathRelativeToRepository))
  if (insideRepository) {
    throw new Error(
      `QA keystore must remain outside the repository; choose a machine-local path with ${QA_ENV.keystore}: ${path}`,
    )
  }
}

function stringSetting(value, fallback, name, trim = false) {
  const selected = value === undefined ? fallback : String(value)
  const normalized = trim ? selected.trim() : selected
  if (!normalized) throw new Error(`${name} must not be empty`)
  return normalized
}

function unique(values) {
  return [...new Set(values.filter(Boolean))]
}

function printCaptured(result, logger) {
  const stdout = String(result.stdout ?? '').trim()
  const stderr = String(result.stderr ?? '').trim()
  if (stdout) logger.log(stdout)
  if (stderr) (logger.warn ?? logger.log).call(logger, stderr)
}

export function executeCommand(command, args, options = {}) {
  return new Promise((resolvePromise, reject) => {
    const capture = options.capture === true
    let child
    try {
      child = spawn(command, args, {
        cwd: options.cwd,
        env: options.env,
        shell: false,
        stdio: capture ? ['ignore', 'pipe', 'pipe'] : (options.stdio ?? 'inherit'),
      })
    } catch (error) {
      reject(error)
      return
    }

    const stdout = []
    const stderr = []
    if (capture) {
      child.stdout.on('data', chunk => stdout.push(chunk))
      child.stderr.on('data', chunk => stderr.push(chunk))
    }

    let settled = false
    const finish = (error, code, signal) => {
      if (settled) return
      settled = true
      const result = {
        code,
        signal,
        stderr: capture ? Buffer.concat(stderr).toString('utf8') : '',
        stdout: capture ? Buffer.concat(stdout).toString('utf8') : '',
      }
      if (error) {
        reject(error)
      } else if (code !== 0) {
        const diagnostic = result.stderr.trim().split(/\r?\n/u).at(-1)
        reject(new Error(
          `${command} exited with code ${code}${signal ? ` (signal ${signal})` : ''}${diagnostic ? `: ${diagnostic}` : ''}`,
        ))
      } else {
        resolvePromise(result)
      }
    }

    child.once('error', error => finish(error, null, null))
    child.once('close', (code, signal) => finish(null, code, signal))
  })
}

function errorMessage(error) {
  return error instanceof Error ? error.message : String(error)
}

async function runAsCli() {
  try {
    await runAndroidQa()
  } catch (error) {
    console.error(`[android:qa] ERROR ${errorMessage(error)}`)
    process.exitCode = 1
  }
}

if (process.argv[1] && resolve(process.argv[1]) === scriptPath) {
  await runAsCli()
}
