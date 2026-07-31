import { constants as fsConstants } from 'node:fs'
import { access, mkdtemp, readdir, readFile, rm, stat, writeFile } from 'node:fs/promises'
import { homedir, tmpdir } from 'node:os'
import { isAbsolute, join, resolve } from 'node:path'
import { spawn } from 'node:child_process'
import process from 'node:process'
import { fileURLToPath } from 'node:url'

const packageMetadata = JSON.parse(await readFile(new URL('../package.json', import.meta.url), 'utf8'))
const expectedApplicationId = 'com.snowglow_aww.sekaitext_next'
const expectedVersionName = packageMetadata.version
const expectedVersionCode = androidVersionCode(expectedVersionName)
const requiredPermissions = new Set(['android.permission.INTERNET'])
const allowedPermissions = new Set([
  ...requiredPermissions,
  `${expectedApplicationId}.DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION`,
])
const requiredNativeLibraries = [
  'lib/arm64-v8a/libsekaitext_lib.so',
  'lib/arm64-v8a/libgojni.so',
]
const requiredDexClasses = [
  'com.sekaitext.mobile.SekaiMobilePlugin',
  'com.sekaitext.mobilecore.mobilecore.Mobilecore',
  'go.Seq',
]
const forbiddenShellClassPrefix = 'app.tauri.shell.'
const expectedMinSdk = 28
const expectedTargetSdk = 36
const tauriConfigEntry = 'assets/tauri.conf.json'
const expectedAssetProtocolScope = '$APPCACHE/sekaitext/live2d/objects/**'
const forbiddenArtifactPrefixes = [
  'sekaitext-backend',
  'ffmpeg',
  'libffmpeg',
  'ffprobe',
  'sekaicoreengine',
  'libsekaicoreengine',
  'libavcodec',
  'libavformat',
  'libavutil',
  'libavfilter',
  'libavdevice',
  'libswresample',
  'libswscale',
]

if (resolve(process.argv[1] || '') === fileURLToPath(import.meta.url)) {
  await main().catch(error => {
    console.error(`[android:verify] ERROR ${error.message}`)
    process.exitCode = 1
  })
}

async function main() {
  const parsed = parseCliArguments(process.argv.slice(2))
  if (parsed.error) {
    console.error(`[android:verify] ${parsed.error}`)
    console.error('[android:verify] Usage: npm run android:verify -- [--expect-unsigned | --expect-debug] path/to/app.apk')
    process.exitCode = 2
    return
  }

  const apkPath = resolve(process.cwd(), parsed.apkPath)
  const apkStat = await statApk(apkPath)
  if (!apkStat) return

  console.log(`[android:verify] APK: ${apkPath} (${formatBytes(apkStat.size)})`)
  pass('APK exists and is a non-empty file')

  const archive = await listArchiveEntries(apkPath)
  const entries = new Set(archive.entries)
  console.log(`[android:verify] Archive reader: ${archive.label}`)

  const failures = []
  const sdkRoots = await findAndroidSdkRoots()
  const signatureInspection = await inspectApkSignatureState(apkPath, sdkRoots)
  if (signatureInspection.error) {
    fail(`Unable to inspect APK signature state: ${signatureInspection.error}`, failures)
  } else if (parsed.expectUnsigned && !signatureInspection.signed) {
    pass(`Release APK is unsigned (${signatureInspection.command})`)
  } else if (parsed.expectUnsigned) {
    fail(`Release APK must be unsigned, but ${signatureInspection.command} reports a valid Android signature`, failures)
  } else if (signatureInspection.signed) {
    pass(`APK has a valid Android signature (${signatureInspection.command})`)
  } else {
    fail(`Signed APK expected by default, but ${signatureInspection.command} reports an unsigned artifact`, failures)
  }

  verifyNativeLibrariesAndAbis(archive.entries, entries, failures)
  verifyForbiddenArtifacts(archive.entries, failures)
  await verifyEmbeddedTauriConfig(apkPath, archive, entries, failures)

  const apkanalyzerCandidates = await findApkanalyzerCandidates(sdkRoots)
  const manifestInspection = await inspectManifest(apkPath, sdkRoots, apkanalyzerCandidates)
  if (manifestInspection.error) {
    fail(`Android SDK apkanalyzer is required for manifest checks: ${manifestInspection.error}`, failures)
  } else {
    console.log(`[android:verify] Manifest inspector: apkanalyzer (${manifestInspection.command})`)
    verifyManifestIdentityAndSecurity(manifestInspection, parsed, failures)
  }

  const dexEntries = archive.entries.filter(entry => /^classes(?:\d+)?\.dex$/.test(entry))
  if (dexEntries.length === 0) {
    fail('No classes*.dex files found in the APK', failures)
    for (const className of requiredDexClasses) {
      fail(`DEX does not contain defined class ${className}`, failures)
    }
    fail(`Cannot verify forbidden ${forbiddenShellClassPrefix}* classes without DEX files`, failures)
  } else {
    pass(`Found ${dexEntries.length} DEX file${dexEntries.length === 1 ? '' : 's'}`)
    const inspection = await inspectDexClasses(
      apkPath,
      dexEntries,
      archive,
      sdkRoots,
      apkanalyzerCandidates,
    )
    console.log(`[android:verify] DEX inspector: ${inspection.label}`)
    if (inspection.fallbackReason) {
      console.log(`[android:verify] WARN ${inspection.fallbackReason}`)
    }

    for (const className of requiredDexClasses) {
      if (inspection.found.has(className)) pass(`DEX defines ${className}`)
      else fail(`DEX does not define ${className}`, failures)
    }

    if (inspection.forbiddenShellClasses.size === 0) {
      pass(`DEX defines no ${forbiddenShellClassPrefix}* classes`)
    } else {
      fail(`DEX defines forbidden ${forbiddenShellClassPrefix}* classes (${inspection.forbiddenShellClasses.size})`, failures)
      for (const className of [...inspection.forbiddenShellClasses].sort()) {
        console.error(`[android:verify]        ${className}`)
      }
    }
  }

  if (failures.length > 0) {
    console.error(`[android:verify] RESULT FAIL (${failures.length} failed check${failures.length === 1 ? '' : 's'})`)
    process.exitCode = 1
  } else {
    console.log('[android:verify] RESULT PASS')
  }
}

export function parseCliArguments(args) {
  let expectUnsigned = false
  let expectDebug = false
  const positional = []

  for (const arg of args) {
    if (arg === '--expect-unsigned') {
      if (expectUnsigned) return { error: '--expect-unsigned may only be specified once' }
      expectUnsigned = true
    } else if (arg === '--expect-debug') {
      if (expectDebug) return { error: '--expect-debug may only be specified once' }
      expectDebug = true
    } else if (arg.startsWith('--')) {
      return { error: `Unknown option: ${arg}` }
    } else {
      positional.push(arg)
    }
  }

  if (expectUnsigned && expectDebug) {
    return { error: '--expect-unsigned and --expect-debug are mutually exclusive' }
  }
  if (positional.length !== 1) {
    return { error: 'An exact APK path is required; automatic mtime-based artifact selection is disabled' }
  }
  return { apkPath: positional[0], expectUnsigned, expectDebug, error: null }
}

async function statApk(apkPath) {
  let fileStat
  try {
    fileStat = await stat(apkPath)
  } catch (error) {
    if (error?.code === 'ENOENT') {
      console.error(`[android:verify] FAIL APK does not exist: ${apkPath}`)
      process.exitCode = 1
      return null
    }
    throw error
  }

  if (!fileStat.isFile()) {
    console.error(`[android:verify] FAIL APK path is not a file: ${apkPath}`)
    process.exitCode = 1
    return null
  }
  if (fileStat.size === 0) {
    console.error(`[android:verify] FAIL APK is empty: ${apkPath}`)
    process.exitCode = 1
    return null
  }
  return fileStat
}

async function listArchiveEntries(apkPath) {
  const attempts = [
    ...uniqueCommands([process.env.UNZIP, '/usr/bin/unzip', 'unzip']).map(command => ({
      command,
      args: ['-Z1', apkPath],
      kind: 'unzip',
    })),
    ...uniqueCommands([process.env.JAR, '/usr/bin/jar', 'jar']).map(command => ({
      command,
      args: ['tf', apkPath],
      kind: 'jar',
    })),
  ]
  const errors = []

  for (const attempt of attempts) {
    const result = await runCaptured(attempt.command, attempt.args)
    if (!result.error && result.code === 0) {
      const entries = result.stdout.toString('utf8').split(/\r?\n/).filter(Boolean)
      if (entries.length > 0) {
        return {
          entries,
          kind: attempt.kind,
          command: attempt.command,
          label: `${attempt.kind} (${attempt.command})`,
        }
      }
    }
    errors.push(describeFailure(attempt.command, result))
  }

  throw new Error(`Unable to list APK contents: ${errors.join('; ')}`)
}

function verifyNativeLibrariesAndAbis(archiveEntries, entries, failures) {
  const nativeAbis = new Set()
  for (const entry of archiveEntries) {
    const match = entry.match(/^lib\/([^/]+)\//)
    if (match) nativeAbis.add(match[1])
  }

  if (nativeAbis.size === 1 && nativeAbis.has('arm64-v8a')) {
    pass('Native library ABI set is exactly arm64-v8a')
  } else {
    const found = nativeAbis.size > 0 ? [...nativeAbis].sort().join(', ') : '(none)'
    fail(`Native library ABI set must be exactly arm64-v8a, found ${found}`, failures)
  }

  for (const library of requiredNativeLibraries) {
    if (entries.has(library)) pass(`Contains ${library}`)
    else fail(`Missing ${library}`, failures)
  }
}

function verifyForbiddenArtifacts(archiveEntries, failures) {
  const forbiddenEntries = archiveEntries.filter(isForbiddenArtifact)
  if (forbiddenEntries.length === 0) {
    pass('Contains no forbidden desktop backend, engine, FFmpeg, or libav artifacts')
    return
  }

  fail(`Contains forbidden desktop/engine/media artifacts (${forbiddenEntries.length})`, failures)
  for (const entry of forbiddenEntries.slice(0, 20)) {
    console.error(`[android:verify]        ${entry}`)
  }
  if (forbiddenEntries.length > 20) {
    console.error(`[android:verify]        ... and ${forbiddenEntries.length - 20} more`)
  }
}

function isForbiddenArtifact(entry) {
  const segments = entry.replaceAll('\\', '/').split('/').filter(Boolean)
  return segments.some(segment => {
    const lower = segment.toLowerCase()
    if (forbiddenArtifactPrefixes.some(prefix => matchesArtifactPrefix(lower, prefix))) return true
    return lower === 'engine'
      || /^engine(?:-[^.]+)?\.exe$/.test(lower)
      || /^libengine\.(?:so|dylib)$/.test(lower)
  })
}

function matchesArtifactPrefix(value, prefix) {
  if (!value.startsWith(prefix)) return false
  const following = value[prefix.length]
  return following === undefined || following === '.' || following === '_' || following === '-'
}

async function verifyEmbeddedTauriConfig(apkPath, archive, entries, failures) {
  if (!entries.has(tauriConfigEntry)) {
    fail(`Missing ${tauriConfigEntry}`, failures)
    return
  }

  let config
  try {
    const rawConfig = await readArchiveEntry(apkPath, tauriConfigEntry, archive)
    config = JSON.parse(rawConfig.toString('utf8'))
    pass(`${tauriConfigEntry} is valid JSON`)
  } catch (error) {
    fail(`Unable to read/parse ${tauriConfigEntry}: ${error.message}`, failures)
    return
  }

  verifyEmbeddedTauriConfigObject(config, failures)
}

export function verifyEmbeddedTauriConfigObject(config, failures) {
  if (config?.version === expectedVersionName) {
    pass(`embedded Tauri version is ${expectedVersionName}`)
  } else {
    fail(`embedded Tauri version must be ${expectedVersionName}, found ${formatJsonValue(config?.version)}`, failures)
  }

  const assetProtocol = config?.app?.security?.assetProtocol
  if (assetProtocol?.enable === true) {
    pass('assetProtocol.enable is true')
  } else {
    fail(`assetProtocol.enable must be true, found ${formatJsonValue(assetProtocol?.enable)}`, failures)
  }

  const scope = assetProtocol?.scope
  if (Array.isArray(scope) && scope.length === 1 && scope[0] === expectedAssetProtocolScope) {
    pass(`assetProtocol.scope is exactly ${expectedAssetProtocolScope}`)
  } else {
    fail(`assetProtocol.scope must be exactly [${JSON.stringify(expectedAssetProtocolScope)}], found ${formatJsonValue(scope)}`, failures)
  }

  if (hasOwn(config?.plugins, 'shell')) {
    fail('assets/tauri.conf.json must not contain plugins.shell', failures)
  } else {
    pass('assets/tauri.conf.json does not contain plugins.shell')
  }

  if (isMissingOrNull(config?.bundle, 'externalBin')) {
    pass('bundle.externalBin is null or omitted')
  } else {
    fail(`bundle.externalBin must be null or omitted, found ${formatJsonValue(config.bundle.externalBin)}`, failures)
  }

  if (isMissingOrNull(config?.bundle, 'resources')) {
    pass('bundle.resources is null or omitted')
  } else {
    fail(`bundle.resources must be null or omitted, found ${formatJsonValue(config.bundle.resources)}`, failures)
  }
}

async function readArchiveEntry(apkPath, entry, archive) {
  if (archive.kind === 'unzip') {
    const result = await runCaptured(archive.command, ['-p', apkPath, entry])
    if (result.error || result.code !== 0) {
      throw new Error(describeFailure(archive.command, result))
    }
    if (result.stdout.length === 0) throw new Error(`${entry} is empty`)
    return result.stdout
  }

  const temporaryDirectory = await mkdtemp(join(tmpdir(), 'sekaitext-apk-entry-'))
  try {
    const result = await runCaptured(archive.command, ['xf', apkPath, entry], {
      cwd: temporaryDirectory,
    })
    if (result.error || result.code !== 0) {
      throw new Error(describeFailure(archive.command, result))
    }
    const data = await readFile(join(temporaryDirectory, entry))
    if (data.length === 0) throw new Error(`${entry} is empty`)
    return data
  } finally {
    await rm(temporaryDirectory, { recursive: true, force: true })
  }
}

function hasOwn(value, key) {
  return value !== null && typeof value === 'object'
    && Object.prototype.hasOwnProperty.call(value, key)
}

function isMissingOrNull(value, key) {
  return !hasOwn(value, key) || value[key] === null
}

function formatJsonValue(value) {
  if (value === undefined) return '(missing)'
  const formatted = JSON.stringify(value)
  return formatted === undefined ? String(value) : formatted
}

async function inspectManifest(apkPath, sdkRoots, apkanalyzerCandidates) {
  const attempts = []
  const env = androidToolEnv(sdkRoots)
  const queries = {
    applicationId: 'application-id',
    versionName: 'version-name',
    versionCode: 'version-code',
    minSdk: 'min-sdk',
    targetSdk: 'target-sdk',
    permissions: 'permissions',
    debuggable: 'debuggable',
    manifest: 'print',
  }

  for (const command of apkanalyzerCandidates) {
    const output = {}
    let failed = null
    for (const [key, verb] of Object.entries(queries)) {
      const result = await runCaptured(command, ['manifest', verb, apkPath], { env })
      if (result.error || result.code !== 0) {
        failed = `${verb}: ${describeFailure(command, result)}`
        break
      }
      output[key] = result.stdout.toString('utf8').trim()
    }
    if (failed) {
      attempts.push(`${command} ${failed}`)
      continue
    }

    const minSdk = parseInteger(output.minSdk)
    const targetSdk = parseInteger(output.targetSdk)
    const versionCode = parseInteger(output.versionCode)
    const debuggable = parseBoolean(output.debuggable)
    const cleartextMatch = output.manifest.match(/android:usesCleartextTraffic="(true|false)"/u)
    if (minSdk === null || targetSdk === null || versionCode === null || debuggable === null || !cleartextMatch) {
      attempts.push(`${command}: unexpected manifest output`)
      continue
    }
    return {
      applicationId: output.applicationId,
      cleartext: cleartextMatch[1] === 'true',
      command,
      debuggable,
      error: null,
      minSdk,
      permissions: new Set(output.permissions.split(/\r?\n/u).map(line => line.trim()).filter(Boolean)),
      targetSdk,
      versionCode,
      versionName: output.versionName,
    }
  }

  return {
    error: attempts.length > 0 ? attempts.join('; ') : 'apkanalyzer was not found',
  }
}

export function verifyManifestIdentityAndSecurity(inspection, options, failures) {
  if (inspection.applicationId === expectedApplicationId) pass(`applicationId is ${expectedApplicationId}`)
  else fail(`applicationId must be ${expectedApplicationId}, found ${inspection.applicationId}`, failures)

  if (inspection.versionName === expectedVersionName) pass(`versionName is ${expectedVersionName}`)
  else fail(`versionName must be ${expectedVersionName}, found ${inspection.versionName}`, failures)

  if (inspection.versionCode === expectedVersionCode) pass(`versionCode is ${expectedVersionCode}`)
  else fail(`versionCode must be ${expectedVersionCode}, found ${inspection.versionCode}`, failures)

  if (inspection.minSdk === expectedMinSdk) pass(`minSdk is ${expectedMinSdk}`)
  else fail(`minSdk must be ${expectedMinSdk}, found ${inspection.minSdk}`, failures)

  if (inspection.targetSdk === expectedTargetSdk) pass(`targetSdk is ${expectedTargetSdk}`)
  else fail(`targetSdk must be ${expectedTargetSdk}, found ${inspection.targetSdk}`, failures)

  const missingPermissions = [...requiredPermissions].filter(permission => !inspection.permissions.has(permission))
  const unexpectedPermissions = [...inspection.permissions].filter(permission => !allowedPermissions.has(permission))
  if (missingPermissions.length === 0 && unexpectedPermissions.length === 0) {
    pass(`permission set is restricted to ${[...inspection.permissions].sort().join(', ')}`)
  } else {
    if (missingPermissions.length > 0) fail(`Missing required permissions: ${missingPermissions.join(', ')}`, failures)
    if (unexpectedPermissions.length > 0) fail(`Unexpected APK permissions: ${unexpectedPermissions.join(', ')}`, failures)
  }

  const expectedDebuggable = options.expectDebug === true
  if (inspection.debuggable === expectedDebuggable) pass(`debuggable is ${expectedDebuggable}`)
  else fail(`debuggable must be ${expectedDebuggable}, found ${inspection.debuggable}`, failures)

  const expectedCleartext = options.expectDebug === true
  if (inspection.cleartext === expectedCleartext) pass(`usesCleartextTraffic is ${expectedCleartext}`)
  else fail(`usesCleartextTraffic must be ${expectedCleartext}, found ${inspection.cleartext}`, failures)
}

export function androidVersionCode(version) {
  const match = String(version).match(/^(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$/u)
  if (!match) throw new Error(`Android versionCode requires semver, found ${JSON.stringify(version)}`)
  const [, majorRaw, minorRaw, patchRaw] = match
  const major = Number.parseInt(majorRaw, 10)
  const minor = Number.parseInt(minorRaw, 10)
  const patch = Number.parseInt(patchRaw, 10)
  if (minor > 999 || patch > 999) throw new Error(`Android versionCode components exceed three digits: ${version}`)
  const code = major * 1_000_000 + minor * 1_000 + patch
  if (!Number.isSafeInteger(code) || code < 1 || code > 2_100_000_000) {
    throw new Error(`Android versionCode is outside the supported range: ${version}`)
  }
  return code
}

function parseInteger(output) {
  const value = String(output).trim()
  return /^\d+$/u.test(value) ? Number.parseInt(value, 10) : null
}

function parseBoolean(output) {
  const value = String(output).trim()
  if (value === 'true') return true
  if (value === 'false') return false
  return null
}

async function inspectDexClasses(apkPath, dexEntries, archive, sdkRoots, apkanalyzerCandidates) {
  const apkanalyzerAttempts = []

  for (const command of apkanalyzerCandidates) {
    const found = new Set()
    const forbiddenShellClasses = new Set()
    const result = await scanLines(
      command,
      ['dex', 'packages', '--defined-only', apkPath],
      line => {
        for (const className of requiredDexClasses) {
          if (apkanalyzerLineDefines(line, className)) found.add(className)
        }
        const shellClass = apkanalyzerClassWithPrefix(line, forbiddenShellClassPrefix)
        if (shellClass) forbiddenShellClasses.add(shellClass)
      },
      { env: androidToolEnv(sdkRoots) },
    )

    if (!result.error && result.code === 0) {
      return { found, forbiddenShellClasses, label: `apkanalyzer (${command})` }
    }
    apkanalyzerAttempts.push(describeFailure(command, result))
  }

  const temporaryDirectory = await mkdtemp(join(tmpdir(), 'sekaitext-apk-'))
  try {
    const dexFiles = await extractDexFiles(apkPath, dexEntries, archive, temporaryDirectory)
    const dexdumpAttempts = []
    const dexdumpCandidates = await findDexdumpCandidates(sdkRoots)

    for (const command of dexdumpCandidates) {
      const found = new Set()
      const forbiddenShellClasses = new Set()
      let failedResult = null

      for (const dexFile of dexFiles) {
        const result = await scanLines(
          command,
          ['-n', dexFile.path],
          line => {
            const match = line.match(/^\s*Class descriptor\s*:\s*'([^']+)'\s*$/)
            if (!match) return
            for (const className of requiredDexClasses) {
              if (match[1] === dexDescriptor(className)) found.add(className)
            }
            const shellClass = classNameFromShellDescriptor(match[1])
            if (shellClass) forbiddenShellClasses.add(shellClass)
          },
          { env: androidToolEnv(sdkRoots) },
        )
        if (result.error || result.code !== 0) {
          failedResult = result
          break
        }
      }

      if (!failedResult) {
        return {
          found,
          forbiddenShellClasses,
          label: `dexdump (${command})`,
          fallbackReason: summarizeToolFallback('apkanalyzer', apkanalyzerAttempts),
        }
      }
      dexdumpAttempts.push(describeFailure(command, failedResult))
    }

    const found = new Set()
    const forbiddenShellClasses = new Set()
    for (const dexFile of dexFiles) {
      const descriptors = parseDefinedDexClasses(dexFile.data, dexFile.entry)
      for (const className of requiredDexClasses) {
        if (descriptors.has(dexDescriptor(className))) found.add(className)
      }
      for (const descriptor of descriptors) {
        const shellClass = classNameFromShellDescriptor(descriptor)
        if (shellClass) forbiddenShellClasses.add(shellClass)
      }
    }
    const reasons = [
      summarizeToolFallback('apkanalyzer', apkanalyzerAttempts),
      summarizeToolFallback('dexdump', dexdumpAttempts),
    ].filter(Boolean).join('; ')
    return {
      found,
      forbiddenShellClasses,
      label: 'built-in DEX class_defs parser',
      fallbackReason: reasons || 'Android SDK DEX tools were not found',
    }
  } finally {
    await rm(temporaryDirectory, { recursive: true, force: true })
  }
}

function apkanalyzerLineDefines(line, className) {
  const trimmed = line.trimEnd()
  if (!trimmed.startsWith('C ') || !trimmed.endsWith(className)) return false
  const preceding = trimmed[trimmed.length - className.length - 1]
  return preceding === undefined || !/[A-Za-z0-9_.$]/.test(preceding)
}

function apkanalyzerClassWithPrefix(line, prefix) {
  const trimmed = line.trimEnd()
  if (!trimmed.startsWith('C ')) return null

  const index = trimmed.indexOf(prefix)
  if (index === -1) return null
  const preceding = trimmed[index - 1]
  if (preceding !== undefined && /[A-Za-z0-9_.$]/.test(preceding)) return null

  const className = trimmed.slice(index)
  return /^(?:[A-Za-z_$][A-Za-z0-9_$]*\.)+[A-Za-z_$][A-Za-z0-9_$]*$/.test(className)
    ? className
    : null
}

function classNameFromShellDescriptor(descriptor) {
  const prefix = 'Lapp/tauri/shell/'
  if (!descriptor.startsWith(prefix) || !descriptor.endsWith(';')) return null
  return descriptor.slice(1, -1).replaceAll('/', '.')
}

async function extractDexFiles(apkPath, dexEntries, archive, temporaryDirectory) {
  if (archive.kind === 'jar') {
    const result = await runCaptured(archive.command, ['xf', apkPath, ...dexEntries], {
      cwd: temporaryDirectory,
    })
    if (result.error || result.code !== 0) {
      throw new Error(`Unable to extract DEX files with jar: ${describeFailure(archive.command, result)}`)
    }

    return Promise.all(dexEntries.map(async entry => {
      const path = join(temporaryDirectory, entry)
      const data = await readFile(path)
      if (data.length === 0) throw new Error(`${entry} is empty`)
      return { entry, path, data }
    }))
  }

  const dexFiles = []
  for (const [index, entry] of dexEntries.entries()) {
    const result = await runCaptured(archive.command, ['-p', apkPath, entry])
    if (result.error || result.code !== 0) {
      throw new Error(`Unable to extract ${entry}: ${describeFailure(archive.command, result)}`)
    }
    if (result.stdout.length === 0) throw new Error(`${entry} is empty`)

    const path = join(temporaryDirectory, `classes-${index + 1}.dex`)
    await writeFile(path, result.stdout)
    dexFiles.push({ entry, path, data: result.stdout })
  }
  return dexFiles
}

function parseDefinedDexClasses(data, entry) {
  if (data.length < 112 || data.subarray(0, 4).toString('ascii') !== 'dex\n') {
    throw new Error(`${entry} is not a supported standard DEX file`)
  }
  if (data.readUInt32LE(40) !== 0x12345678) {
    throw new Error(`${entry} uses an unsupported DEX byte order`)
  }

  const declaredSize = data.readUInt32LE(32)
  if (declaredSize !== data.length) throw new Error(`${entry} DEX file_size does not match its extracted size`)
  if (data.readUInt32LE(36) !== 112) throw new Error(`${entry} has an unsupported DEX header size`)

  const stringIdsSize = data.readUInt32LE(56)
  const stringIdsOffset = data.readUInt32LE(60)
  const typeIdsSize = data.readUInt32LE(64)
  const typeIdsOffset = data.readUInt32LE(68)
  const classDefsSize = data.readUInt32LE(96)
  const classDefsOffset = data.readUInt32LE(100)

  assertDexTable(data, stringIdsOffset, stringIdsSize, 4, `${entry} string_ids`)
  assertDexTable(data, typeIdsOffset, typeIdsSize, 4, `${entry} type_ids`)
  assertDexTable(data, classDefsOffset, classDefsSize, 32, `${entry} class_defs`)

  const descriptors = new Set()
  for (let index = 0; index < classDefsSize; index += 1) {
    const classIndex = data.readUInt32LE(classDefsOffset + index * 32)
    if (classIndex >= typeIdsSize) throw new Error(`${entry} has an invalid class_idx`)

    const descriptorIndex = data.readUInt32LE(typeIdsOffset + classIndex * 4)
    if (descriptorIndex >= stringIdsSize) throw new Error(`${entry} has an invalid descriptor_idx`)

    const stringOffset = data.readUInt32LE(stringIdsOffset + descriptorIndex * 4)
    if (stringOffset >= data.length) throw new Error(`${entry} has an invalid string_data_off`)

    const stringStart = skipUleb128(data, stringOffset, entry)
    const stringEnd = data.indexOf(0, stringStart)
    if (stringEnd === -1) throw new Error(`${entry} has an unterminated class descriptor`)
    descriptors.add(data.subarray(stringStart, stringEnd).toString('utf8'))
  }
  return descriptors
}

function assertDexTable(data, offset, count, itemSize, label) {
  const end = offset + count * itemSize
  if (!Number.isSafeInteger(end) || offset > data.length || end > data.length) {
    throw new Error(`${label} table is outside the DEX file`)
  }
}

function skipUleb128(data, offset, entry) {
  for (let index = 0; index < 5; index += 1) {
    if (offset + index >= data.length) throw new Error(`${entry} has a truncated ULEB128 value`)
    if ((data[offset + index] & 0x80) === 0) return offset + index + 1
  }
  throw new Error(`${entry} has an invalid ULEB128 value`)
}

async function findAndroidSdkRoots() {
  const candidates = uniqueCommands([
    process.env.ANDROID_HOME,
    process.env.ANDROID_SDK_ROOT,
    join(homedir(), 'Library', 'Android', 'sdk'),
    join(homedir(), 'Android', 'Sdk'),
  ])
  const roots = []
  for (const candidate of candidates) {
    try {
      if ((await stat(candidate)).isDirectory()) roots.push(candidate)
    } catch {
      // Keep searching other conventional SDK locations.
    }
  }
  return roots
}

async function inspectApkSignatureState(apkPath, sdkRoots) {
  const attempts = []
  const candidates = await findApksignerCandidates(sdkRoots)

  for (const command of candidates) {
    const result = await runCaptured(command, ['verify', '--verbose', '--print-certs', apkPath], {
      env: androidToolEnv(sdkRoots),
    })
    if (result.error) {
      attempts.push(describeFailure(command, result))
      continue
    }
    if (result.code === 0) return { signed: true, command, error: null }

    const output = `${result.stdout.toString('utf8')}\n${result.stderr}`
    if (looksLikeUnsignedApk(output)) return { signed: false, command, error: null }
    attempts.push(describeFailure(command, result))
  }

  return {
    error: attempts.length > 0 ? attempts.join('; ') : 'Android SDK apksigner was not found',
  }
}

function looksLikeUnsignedApk(output) {
  return /Missing META-INF\/MANIFEST\.MF|No signatures? found|does not contain (?:any )?signatures?|APK is not signed/iu.test(output)
}

async function findApksignerCandidates(sdkRoots) {
  const candidates = [process.env.APKSIGNER]
  for (const sdkRoot of sdkRoots) {
    const buildToolsRoot = join(sdkRoot, 'build-tools')
    for (const version of await childDirectoriesNewestFirst(buildToolsRoot)) {
      candidates.push(join(buildToolsRoot, version, process.platform === 'win32' ? 'apksigner.bat' : 'apksigner'))
    }
  }
  candidates.push(process.platform === 'win32' ? 'apksigner.bat' : 'apksigner')
  return filterExecutableCandidates(candidates)
}

async function findApkanalyzerCandidates(sdkRoots) {
  const candidates = [process.env.APKANALYZER]
  for (const sdkRoot of sdkRoots) {
    const toolsRoot = join(sdkRoot, 'cmdline-tools')
    candidates.push(join(toolsRoot, 'latest', 'bin', 'apkanalyzer'))
    for (const version of await childDirectoriesNewestFirst(toolsRoot)) {
      candidates.push(join(toolsRoot, version, 'bin', 'apkanalyzer'))
    }
    candidates.push(join(sdkRoot, 'tools', 'bin', 'apkanalyzer'))
  }
  candidates.push('apkanalyzer')
  return filterExecutableCandidates(candidates)
}

async function findDexdumpCandidates(sdkRoots) {
  const candidates = [process.env.DEXDUMP]
  for (const sdkRoot of sdkRoots) {
    const buildToolsRoot = join(sdkRoot, 'build-tools')
    for (const version of await childDirectoriesNewestFirst(buildToolsRoot)) {
      candidates.push(join(buildToolsRoot, version, 'dexdump'))
    }
  }
  candidates.push('dexdump')
  return filterExecutableCandidates(candidates)
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

async function filterExecutableCandidates(candidates) {
  const filtered = []
  for (const candidate of uniqueCommands(candidates)) {
    if (!isPathCommand(candidate)) {
      filtered.push(candidate)
      continue
    }
    try {
      await access(candidate, fsConstants.X_OK)
      filtered.push(candidate)
    } catch {
      // Missing versioned SDK tools are expected; try the next candidate.
    }
  }
  return filtered
}

function androidToolEnv(sdkRoots) {
  if (sdkRoots.length === 0) return process.env
  return {
    ...process.env,
    ANDROID_HOME: sdkRoots[0],
    ANDROID_SDK_ROOT: sdkRoots[0],
  }
}

function isPathCommand(command) {
  return isAbsolute(command) || command.includes('/') || command.includes('\\')
}

function dexDescriptor(className) {
  return `L${className.replaceAll('.', '/')};`
}

function summarizeToolFallback(tool, attempts) {
  if (attempts.length === 0) return `${tool} was not found`
  return `${tool} unavailable (${attempts.join(', ')})`
}

function uniqueCommands(commands) {
  return [...new Set(commands.filter(Boolean))]
}

function scanLines(command, args, onLine, options = {}) {
  return new Promise(resolvePromise => {
    let child
    try {
      child = spawn(command, args, {
        cwd: options.cwd,
        env: options.env,
        shell: false,
        stdio: ['ignore', 'pipe', 'pipe'],
      })
    } catch (error) {
      resolvePromise({ code: null, signal: null, error, stderr: '' })
      return
    }

    let settled = false
    let pending = ''
    let stderr = ''
    child.stdout.setEncoding('utf8')
    child.stderr.setEncoding('utf8')
    child.stdout.on('data', chunk => {
      pending += chunk
      const lines = pending.split(/\r?\n/)
      pending = lines.pop() || ''
      for (const line of lines) onLine(line)
    })
    child.stderr.on('data', chunk => {
      stderr = appendDiagnostic(stderr, chunk)
    })

    const finish = result => {
      if (settled) return
      settled = true
      if (pending) onLine(pending)
      resolvePromise({ ...result, stderr })
    }
    child.once('error', error => finish({ code: null, signal: null, error }))
    child.once('close', (code, signal) => finish({ code, signal, error: null }))
  })
}

function runCaptured(command, args, options = {}) {
  return new Promise(resolvePromise => {
    let child
    try {
      child = spawn(command, args, {
        cwd: options.cwd,
        env: options.env,
        shell: false,
        stdio: ['ignore', 'pipe', 'pipe'],
      })
    } catch (error) {
      resolvePromise({ code: null, signal: null, error, stdout: Buffer.alloc(0), stderr: '' })
      return
    }

    let settled = false
    const stdout = []
    let stderr = ''
    child.stdout.on('data', chunk => stdout.push(chunk))
    child.stderr.setEncoding('utf8')
    child.stderr.on('data', chunk => {
      stderr = appendDiagnostic(stderr, chunk)
    })

    const finish = result => {
      if (settled) return
      settled = true
      resolvePromise({ ...result, stdout: Buffer.concat(stdout), stderr })
    }
    child.once('error', error => finish({ code: null, signal: null, error }))
    child.once('close', (code, signal) => finish({ code, signal, error: null }))
  })
}

function appendDiagnostic(current, chunk) {
  return `${current}${chunk}`.slice(-4000)
}

function describeFailure(command, result) {
  if (result.error) return `${command}: ${result.error.code || result.error.message}`
  const detail = result.stderr.trim().split(/\r?\n/).at(-1)
  if (result.signal) return `${command}: signal ${result.signal}${detail ? ` (${detail})` : ''}`
  return `${command}: exit ${result.code}${detail ? ` (${detail})` : ''}`
}

function pass(message) {
  console.log(`[android:verify] PASS ${message}`)
}

function fail(message, failures) {
  failures.push(message)
  console.error(`[android:verify] FAIL ${message}`)
}

function formatBytes(bytes) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 ** 2) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${(bytes / 1024 ** 2).toFixed(1)} MiB`
}
