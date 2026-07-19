import { readFileSync, writeFileSync } from 'fs'
import { join } from 'path'

const root = join(import.meta.dirname, '..')

// Read version from package.json
const pkg = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'))
const version = pkg.version

// npm normally updates its lockfile during `npm version`, but this script is
// also run directly in release preparation. Keep both root entries aligned.
const packageLockPath = join(root, 'package-lock.json')
const packageLock = JSON.parse(readFileSync(packageLockPath, 'utf8'))
packageLock.version = version
if (packageLock.packages?.['']) packageLock.packages[''].version = version
writeFileSync(packageLockPath, JSON.stringify(packageLock, null, 2) + '\n')

// Sync to tauri.conf.json
const tauriConfPath = join(root, 'src-tauri', 'tauri.conf.json')
const tauriConf = JSON.parse(readFileSync(tauriConfPath, 'utf8'))
tauriConf.version = version
writeFileSync(tauriConfPath, JSON.stringify(tauriConf, null, 2) + '\n')

// Sync to Cargo.toml
const cargoPath = join(root, 'src-tauri', 'Cargo.toml')
let cargo = readFileSync(cargoPath, 'utf8')
cargo = cargo.replace(/^version\s*=\s*".*"/m, `version = "${version}"`)
writeFileSync(cargoPath, cargo)

// Cargo.lock records the workspace package version separately from Cargo.toml.
const cargoLockPath = join(root, 'src-tauri', 'Cargo.lock')
let cargoLock = readFileSync(cargoLockPath, 'utf8')
cargoLock = cargoLock.replace(
  /(\[\[package\]\]\nname = "app"\nversion = ")[^"]+/,
  `$1${version}`,
)
writeFileSync(cargoLockPath, cargoLock)

console.log(`[sync-version] Synced version ${version} across npm and Tauri manifests`)
