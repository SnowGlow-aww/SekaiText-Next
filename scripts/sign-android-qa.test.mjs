import assert from 'node:assert/strict'
import { chmodSync, mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { basename, dirname, join, resolve } from 'node:path'
import test, { after } from 'node:test'
import { fileURLToPath } from 'node:url'

import {
  assertAndroidDebugCertificate,
  createDebugKeystoreCommand,
  createQaCommandPlan,
  DEFAULT_QA_APK,
  discoverAndroidBuildTools,
  QA_ENV,
  RELEASE_UNSIGNED_APK,
  resolveQaConfig,
} from './sign-android-qa.mjs'

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const temporaryDirectories = []
const posixOnly = { skip: process.platform === 'win32' }

after(() => {
  for (const directory of temporaryDirectories) {
    rmSync(directory, { force: true, recursive: true })
  }
})

test('QA defaults select the exact unsigned release artifact and a fixed versioned arm64 output', () => {
  const homeDirectory = temporaryDirectory('sekaitext-qa-home-')
  const config = resolveQaConfig({}, {
    cwd: repoRoot,
    homeDirectory,
    repoRoot,
  })

  assert.equal(config.unsignedApk, RELEASE_UNSIGNED_APK)
  assert.equal(config.outputApk, DEFAULT_QA_APK)
  assert.equal(dirname(config.outputApk), join(dirname(dirname(DEFAULT_QA_APK)), 'qa'))
  assert.match(basename(config.outputApk), /^SekaiText-\d+\.\d+\.\d+(?:[-+].*)?-android-arm64-qa\.apk$/u)
  assert.equal(config.keystore, join(homeDirectory, '.android', 'debug.keystore'))
  assert.equal(config.keyAlias, 'androiddebugkey')
  assert.equal(config.storePassword, 'android')
  assert.equal(config.keyPassword, 'android')
})

test('QA environment overrides feed array-based commands without exposing passwords in argv', () => {
  const workspace = temporaryDirectory('sekaitext-qa-overrides-')
  const keystore = join(workspace, 'keys', 'qa-debug.keystore')
  const outputApk = join(workspace, 'artifacts', 'qa-arm64.apk')
  const config = resolveQaConfig({
    [QA_ENV.alias]: 'qa-debug-key',
    [QA_ENV.keyPassword]: 'key secret',
    [QA_ENV.keystore]: keystore,
    [QA_ENV.output]: outputApk,
    [QA_ENV.storePassword]: 'store secret',
  }, {
    cwd: workspace,
    homeDirectory: workspace,
    repoRoot,
  })
  const tools = {
    apksigner: '/sdk/build-tools/36.0.0/apksigner',
    directory: '/sdk/build-tools/36.0.0',
    sdkRoot: '/sdk',
    version: '36.0.0',
    zipalign: '/sdk/build-tools/36.0.0/zipalign',
  }
  const alignedApk = join(workspace, 'aligned.apk')
  const verifierPath = join(repoRoot, 'scripts', 'verify-android-apk.mjs')
  const plan = createQaCommandPlan(config, tools, {
    alignedApk,
    nodeExecutable: '/node',
    verifierPath,
  })
  const keytool = createDebugKeystoreCommand(config, '/jdk/bin/keytool')

  assert.deepEqual(plan.align, {
    command: tools.zipalign,
    args: ['-f', '-P', '16', '4', RELEASE_UNSIGNED_APK, alignedApk],
  })
  assert.deepEqual(plan.verifyAlignment.args, ['-c', '-P', '16', '4', outputApk])
  assert.deepEqual(plan.verifySignature.args, ['verify', '--verbose', '--print-certs', '--Werr', outputApk])
  assert.deepEqual(plan.verifyContents, {
    command: '/node',
    args: [verifierPath, outputApk],
  })
  assert.deepEqual(plan.sign.args.slice(-3), ['--out', outputApk, alignedApk])
  assert.ok(plan.sign.args.includes('--debuggable-apk-permitted'))
  assert.ok(plan.sign.args.includes(`env:${QA_ENV.storePassword}`))
  assert.ok(plan.sign.args.includes(`env:${QA_ENV.keyPassword}`))
  assert.ok(keytool.args.includes('-storepass:env'))
  assert.ok(keytool.args.includes('-keypass:env'))

  const serializedCommands = JSON.stringify({ keytool, plan })
  assert.doesNotMatch(serializedCommands, /store secret|key secret/u)
})

test('QA keystores are rejected when configured inside the repository', () => {
  assert.throws(
    () => resolveQaConfig({
      [QA_ENV.keystore]: join(repoRoot, 'private', 'debug.keystore'),
    }, {
      cwd: repoRoot,
      homeDirectory: temporaryDirectory('sekaitext-qa-home-'),
      repoRoot,
    }),
    /must remain outside the repository/u,
  )
})

test('QA output may not collide with the signing keystore', () => {
  const workspace = temporaryDirectory('sekaitext-qa-collision-')
  const collision = join(workspace, 'debug.keystore')
  assert.throws(
    () => resolveQaConfig({
      [QA_ENV.keystore]: collision,
      [QA_ENV.output]: collision,
    }, {
      cwd: workspace,
      homeDirectory: workspace,
      repoRoot,
    }),
    /must not overwrite the signing keystore/u,
  )
})

test('build-tools discovery selects the newest complete SDK version', posixOnly, async () => {
  const sdkRoot = temporaryDirectory('sekaitext-android-sdk-')
  const buildToolsRoot = join(sdkRoot, 'build-tools')
  createExecutable(join(buildToolsRoot, '35.0.0', 'zipalign'))
  createExecutable(join(buildToolsRoot, '35.0.0', 'apksigner'))
  createExecutable(join(buildToolsRoot, '36.0.0', 'zipalign'))

  const tools = await discoverAndroidBuildTools({
    env: { ANDROID_HOME: sdkRoot },
    homeDirectory: temporaryDirectory('sekaitext-sdk-home-'),
    platform: 'linux',
  })

  assert.equal(tools.directory, join(buildToolsRoot, '35.0.0'))
  assert.equal(tools.version, '35.0.0')
  assert.equal(tools.zipalign, join(buildToolsRoot, '35.0.0', 'zipalign'))
  assert.equal(tools.apksigner, join(buildToolsRoot, '35.0.0', 'apksigner'))
})

test('QA certificate validation accepts only one Android Debug signer', () => {
  const debugOutput = [
    'Verifies',
    'Number of signers: 1',
    'Signer #1 certificate DN: C=US, O=Android, CN=Android Debug',
  ].join('\n')
  assert.equal(
    assertAndroidDebugCertificate(debugOutput),
    'C=US, O=Android, CN=Android Debug',
  )

  assert.throws(
    () => assertAndroidDebugCertificate([
      'Verifies',
      'Number of signers: 1',
      'Signer #1 certificate DN: CN=Production Release, O=Example',
    ].join('\n')),
    /never a production or Play Store key/u,
  )
  assert.throws(
    () => assertAndroidDebugCertificate([
      'Verifies',
      'Number of signers: 1',
      'Signer #1 certificate DN: CN=Android Debug, O=Production, C=US',
    ].join('\n')),
    /exactly the standard Android Debug identity/u,
  )
})

function createExecutable(path) {
  mkdirSync(dirname(path), { recursive: true })
  writeFileSync(path, '#!/bin/sh\nexit 0\n')
  chmodSync(path, 0o755)
}

function temporaryDirectory(prefix) {
  const directory = mkdtempSync(join(tmpdir(), prefix))
  temporaryDirectories.push(directory)
  return directory
}
