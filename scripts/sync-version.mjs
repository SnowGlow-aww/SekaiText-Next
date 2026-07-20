import { readFileSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

export const VERSION_FILES = Object.freeze([
  'package.json',
  'package-lock.json',
  'src-tauri/tauri.conf.json',
  'src-tauri/Cargo.toml',
  'src-tauri/Cargo.lock',
  'backend/internal/service/app_update.go',
  'README.md',
  'README.en.md',
  'website/guide/autotiming.md',
  'website/guide/faq.md',
])

const semver = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/
const versionCapture = '(\\d+\\.\\d+\\.\\d+(?:-[0-9A-Za-z.-]+)?(?:\\+[0-9A-Za-z.-]+)?)'
const documentedVersions = Object.freeze([
  versionLocation('README.md', 'version badge', '(badge/version-)', '(-blue)'),
  versionLocation('README.md', 'current version', '(当前版本：\\*\\*)', '(\\*\\*)'),
  versionLocation('README.en.md', 'version badge', '(badge/version-)', '(-blue)'),
  versionLocation('README.en.md', 'current version', '(Current version: \\*\\*)', '(\\*\\*)'),
  versionLocation(
    'website/guide/autotiming.md',
    'latest documented version',
    '(本页按最新版描述（\\*\\*主程序 ≥ )',
    '(，「自动轴机)',
  ),
  versionLocation(
    'website/guide/autotiming.md',
    'troubleshooting recommendation',
    '(先升级到 \\*\\*)',
    '(\\*\\*)',
  ),
  versionLocation(
    'website/guide/faq.md',
    'troubleshooting recommendation',
    '(建议直接升级到 \\*\\*)',
    '(\\*\\*)',
  ),
])

export function buildReleasePlan(version) {
  if (!semver.test(version)) throw new Error(`invalid release version: ${version}`)
  return {
    branch: 'main',
    tag: `v${version}`,
    files: [...VERSION_FILES],
  }
}

export function syncVersion(root) {
  const packagePath = join(root, 'package.json')
  const pkg = readJSON(packagePath)
  const version = pkg.version
  buildReleasePlan(version)

  const packageLockPath = join(root, 'package-lock.json')
  const packageLock = readJSON(packageLockPath)
  if (!packageLock.packages?.['']) throw new Error('package-lock.json has no root package entry')
  packageLock.version = version
  packageLock.packages[''].version = version
  writeJSON(packageLockPath, packageLock)

  const tauriConfPath = join(root, 'src-tauri', 'tauri.conf.json')
  const tauriConf = readJSON(tauriConfPath)
  tauriConf.version = version
  writeJSON(tauriConfPath, tauriConf)

  const cargoPath = join(root, 'src-tauri', 'Cargo.toml')
  const cargoSource = readFileSync(cargoPath, 'utf8')
  const cargoPackageName = requiredMatch(
    cargoSource,
    /^\[package\][\s\S]*?^name\s*=\s*"([^"]+)"/m,
    'src-tauri/Cargo.toml package name',
  )
  const cargo = replaceRequired(
    cargoSource,
    /^(\[package\][\s\S]*?^version\s*=\s*")[^"]+/m,
    `$1${version}`,
    'src-tauri/Cargo.toml package version',
  )
  writeFileSync(cargoPath, cargo)

  const cargoLockPath = join(root, 'src-tauri', 'Cargo.lock')
  const cargoLock = replaceRequired(
    readFileSync(cargoLockPath, 'utf8'),
    cargoLockVersionPattern(cargoPackageName),
    `$1${version}`,
    `src-tauri/Cargo.lock ${cargoPackageName} version`,
  )
  writeFileSync(cargoLockPath, cargoLock)

  const appUpdatePath = join(root, 'backend', 'internal', 'service', 'app_update.go')
  const appUpdate = replaceRequired(
    readFileSync(appUpdatePath, 'utf8'),
    /^(var CurrentAppVersion = ")[^"]+/m,
    `$1${version}`,
    'backend CurrentAppVersion',
  )
  writeFileSync(appUpdatePath, appUpdate)

  const documents = new Map()
  for (const location of documentedVersions) {
    const path = join(root, location.file)
    const source = documents.get(path) ?? readFileSync(path, 'utf8')
    documents.set(path, replaceRequired(source, location.pattern, `$1${version}$3`, location.label))
  }
  for (const [path, source] of documents) writeFileSync(path, source)
  return version
}

export function checkVersionConsistency(root, tag) {
  const versions = readVersionEntries(root)
  const expected = versions['package.json']
  buildReleasePlan(expected)
  const mismatches = Object.entries(versions).filter(([, version]) => version !== expected)
  if (mismatches.length > 0) {
    const details = mismatches.map(([file, version]) => `${file}=${version}`).join(', ')
    throw new Error(`version mismatch (expected ${expected}): ${details}`)
  }
  if (tag !== undefined && tag !== `v${expected}`) {
    throw new Error(`release tag ${tag} does not match v${expected}`)
  }
  return expected
}

function readVersionEntries(root) {
  const pkg = readJSON(join(root, 'package.json'))
  const packageLock = readJSON(join(root, 'package-lock.json'))
  const tauriConf = readJSON(join(root, 'src-tauri', 'tauri.conf.json'))
  const cargo = readFileSync(join(root, 'src-tauri', 'Cargo.toml'), 'utf8')
  const cargoLock = readFileSync(join(root, 'src-tauri', 'Cargo.lock'), 'utf8')
  const appUpdate = readFileSync(join(root, 'backend', 'internal', 'service', 'app_update.go'), 'utf8')
  const cargoPackageName = requiredMatch(
    cargo,
    /^\[package\][\s\S]*?^name\s*=\s*"([^"]+)"/m,
    'Cargo.toml package name',
  )
  const versions = {
    'package.json': pkg.version,
    'package-lock.json': packageLock.version,
    'package-lock.json packages[""]': packageLock.packages?.['']?.version,
    'src-tauri/tauri.conf.json': tauriConf.version,
    'src-tauri/Cargo.toml': requiredMatch(cargo, /^\[package\][\s\S]*?^version\s*=\s*"([^"]+)"/m, 'Cargo.toml'),
    'src-tauri/Cargo.lock': requiredMatch(cargoLock, cargoLockVersionPattern(cargoPackageName, true), 'Cargo.lock'),
    'backend/internal/service/app_update.go': requiredMatch(
      appUpdate,
      /^var CurrentAppVersion = "([^"]+)"$/m,
      'backend CurrentAppVersion',
    ),
  }
  for (const location of documentedVersions) {
    const source = readFileSync(join(root, location.file), 'utf8')
    versions[`${location.file} (${location.label})`] = requiredMatch(source, location.pattern, location.label, 2)
  }
  return versions
}

function readJSON(path) {
  return JSON.parse(readFileSync(path, 'utf8'))
}

function writeJSON(path, value) {
  writeFileSync(path, JSON.stringify(value, null, 2) + '\n')
}

function requiredMatch(value, pattern, label, group = 1) {
  const match = value.match(pattern)
  if (!match) throw new Error(`${label} version not found`)
  return match[group]
}

function replaceRequired(value, pattern, replacement, label) {
  if (!pattern.test(value)) throw new Error(`${label} not found`)
  return value.replace(pattern, replacement)
}

function versionLocation(file, label, prefix, suffix) {
  return { file, label, pattern: new RegExp(`${prefix}${versionCapture}${suffix}`) }
}

function cargoLockVersionPattern(packageName, captureVersionOnly = false) {
  const escapedName = packageName.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return captureVersionOnly
    ? new RegExp(`\\[\\[package\\]\\]\\nname = "${escapedName}"\\nversion = "([^"]+)"`)
    : new RegExp(`(\\[\\[package\\]\\]\\nname = "${escapedName}"\\nversion = ")[^"]+`)
}

function runCLI(args) {
  const root = join(import.meta.dirname, '..')
  if (args[0] === '--check') {
    const tagIndex = args.indexOf('--tag')
    const tag = tagIndex === -1 ? undefined : args[tagIndex + 1]
    if (tagIndex !== -1 && !tag) throw new Error('--tag requires a value')
    const version = checkVersionConsistency(root, tag)
    console.log(`[sync-version] Version ${version} is consistent${tag ? ` with ${tag}` : ''}`)
    return
  }
  if (args[0] === '--release-plan' || args[0] === '--release-files') {
    const plan = buildReleasePlan(args[1] || '')
    if (args[0] === '--release-files') console.log(plan.files.join('\n'))
    else console.log(JSON.stringify(plan))
    return
  }
  if (args.length > 0) throw new Error(`unknown arguments: ${args.join(' ')}`)
  const version = syncVersion(root)
  console.log(`[sync-version] Synced version ${version} across manifests, locks, and current-version docs`)
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  try {
    runCLI(process.argv.slice(2))
  } catch (error) {
    console.error(`[sync-version] ${error.message}`)
    process.exitCode = 1
  }
}
