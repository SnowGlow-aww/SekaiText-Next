import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

import {
  androidVersionCode,
  parseCliArguments,
  verifyEmbeddedTauriConfigObject,
  verifyManifestIdentityAndSecurity,
} from './verify-android-apk.mjs'

const repoRoot = join(dirname(fileURLToPath(import.meta.url)), '..')
const packageMetadata = JSON.parse(readFileSync(join(repoRoot, 'package.json'), 'utf8'))

function validManifest() {
  return {
    applicationId: 'com.snowglow_aww.sekaitext_next',
    cleartext: false,
    debuggable: false,
    minSdk: 28,
    permissions: new Set([
      'android.permission.INTERNET',
      'com.snowglow_aww.sekaitext_next.DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION',
    ]),
    targetSdk: 36,
    versionCode: androidVersionCode(packageMetadata.version),
    versionName: packageMetadata.version,
  }
}

test('APK verifier CLI makes signed release the default and separates debug/unsigned profiles', () => {
  assert.deepEqual(parseCliArguments(['app.apk']), {
    apkPath: 'app.apk',
    expectDebug: false,
    expectUnsigned: false,
    error: null,
  })
  assert.equal(parseCliArguments(['--expect-debug', 'debug.apk']).expectDebug, true)
  assert.equal(parseCliArguments(['--expect-unsigned', 'release.apk']).expectUnsigned, true)
  assert.match(
    parseCliArguments(['--expect-debug', '--expect-unsigned', 'bad.apk']).error,
    /mutually exclusive/u,
  )
})

test('Android versionCode is deterministically bound to semantic version', () => {
  assert.equal(androidVersionCode('5.9.6'), 5_009_006)
  assert.equal(androidVersionCode('5.9.6-beta.1'), 5_009_006)
  assert.throws(() => androidVersionCode('5.1000.0'), /exceed three digits/u)
})

test('manifest validation accepts the exact release identity and restricted permissions', () => {
  const failures = []
  verifyManifestIdentityAndSecurity(validManifest(), { expectDebug: false }, failures)
  assert.deepEqual(failures, [])
})

test('manifest validation rejects repackaging, unsafe flags, and added permissions', () => {
  const inspection = validManifest()
  inspection.applicationId = 'attacker.repacked.app'
  inspection.versionName = '0.0.1'
  inspection.versionCode = 1
  inspection.debuggable = true
  inspection.cleartext = true
  inspection.permissions.add('android.permission.MANAGE_EXTERNAL_STORAGE')

  const failures = []
  verifyManifestIdentityAndSecurity(inspection, { expectDebug: false }, failures)

  assert.ok(failures.some(message => message.includes('applicationId must be')))
  assert.ok(failures.some(message => message.includes('versionName must be')))
  assert.ok(failures.some(message => message.includes('versionCode must be')))
  assert.ok(failures.some(message => message.includes('Unexpected APK permissions')))
  assert.ok(failures.some(message => message.includes('debuggable must be false')))
  assert.ok(failures.some(message => message.includes('usesCleartextTraffic must be false')))
})

test('embedded Tauri config is bound to the package version', () => {
  const validConfig = {
    version: packageMetadata.version,
    app: {
      security: {
        assetProtocol: {
          enable: true,
          scope: ['$APPCACHE/sekaitext/live2d/objects/**'],
        },
      },
    },
    plugins: {},
    bundle: { externalBin: null, resources: null },
  }
  const validFailures = []
  verifyEmbeddedTauriConfigObject(validConfig, validFailures)
  assert.deepEqual(validFailures, [])

  const staleFailures = []
  verifyEmbeddedTauriConfigObject({ ...validConfig, version: '5.9.5' }, staleFailures)
  assert.ok(staleFailures.some(message => message.includes('embedded Tauri version must be')))
})
