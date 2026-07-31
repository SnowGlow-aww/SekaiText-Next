import assert from 'node:assert/strict'
import { chmodSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import test, { after } from 'node:test'

import {
  completeAndroidSdkEnvironment,
  runAndroidTauri,
  selectCompatibleJavaHome,
  spawnLocalTauriAndroid,
} from './run-android-tauri.mjs'

const temporaryDirectories = []
const posixOnly = { skip: process.platform === 'win32' }

after(() => {
  for (const directory of temporaryDirectories) {
    rmSync(directory, { force: true, recursive: true })
  }
})

test('package Android commands pass exact variant artifacts to the verifier and expose QA signing', () => {
  const packageJson = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8'))
  assert.equal(packageJson.scripts['android:init'], 'node scripts/run-android-tauri.mjs init')
  assert.equal(packageJson.scripts['android:dev'], 'npm run android:core && node scripts/run-android-tauri.mjs dev')
  assert.equal(
    packageJson.scripts['android:build'],
    'npm run android:core && node scripts/run-android-tauri.mjs build --apk --target aarch64 --ci && node scripts/verify-android-apk.mjs --expect-unsigned src-tauri/gen/android/app/build/outputs/apk/universal/release/app-universal-release-unsigned.apk',
  )
  assert.equal(
    packageJson.scripts['android:build:debug'],
    'npm run android:core && node scripts/run-android-tauri.mjs build --debug --apk --target aarch64 --ci && node scripts/verify-android-apk.mjs --expect-debug src-tauri/gen/android/app/build/outputs/apk/universal/debug/app-universal-debug.apk',
  )
  assert.equal(packageJson.scripts['android:build:qa'], 'npm run android:build && node scripts/sign-android-qa.mjs')
  assert.match(packageJson.scripts.test, /scripts\/sign-android-qa\.test\.mjs/u)
})

test('JDK 26 is rejected and a compatible JDK 23 fallback is selected', posixOnly, () => {
  const jdk26 = fakeJdk(26)
  const jdk23 = fakeJdk(23)
  const selected = selectCompatibleJavaHome({
    env: { ...process.env, JAVA_HOME: jdk26, SEKAI_ANDROID_JAVA_HOME: '' },
    platform: 'linux',
    commonJavaHomes: [jdk23],
  })

  assert.equal(selected.javaHome, resolve(jdk23))
  assert.equal(selected.major, 23)
  assert.match(selected.rejections[0].reason, /JDK 26 is outside the supported 17-23 range/)
})

test('SEKAI_ANDROID_JAVA_HOME has priority over a compatible JAVA_HOME', posixOnly, () => {
  const jdk23 = fakeJdk(23)
  const jdk21 = fakeJdk(21)
  const selected = selectCompatibleJavaHome({
    env: {
      ...process.env,
      JAVA_HOME: jdk21,
      SEKAI_ANDROID_JAVA_HOME: jdk23,
    },
    platform: 'linux',
    commonJavaHomes: [],
  })

  assert.equal(selected.javaHome, resolve(jdk23))
  assert.equal(selected.major, 23)
  assert.equal(selected.source, 'SEKAI_ANDROID_JAVA_HOME')
})

test('missing compatible JDK reports supported installations and the override variable', () => {
  assert.throws(
    () => selectCompatibleJavaHome({
      env: { ...process.env, JAVA_HOME: '', SEKAI_ANDROID_JAVA_HOME: '' },
      platform: 'linux',
      commonJavaHomes: [],
    }),
    /Install JDK 17, 21, or 23, or set SEKAI_ANDROID_JAVA_HOME/,
  )
})

test('missing Android SDK variables use and validate the macOS default', () => {
  const homeDirectory = temporaryDirectory('sekaitext-android-home-')
  const expectedSdk = join(homeDirectory, 'Library', 'Android', 'sdk')
  mkdirSync(expectedSdk, { recursive: true })

  const completed = completeAndroidSdkEnvironment({}, {
    homeDirectory,
    platform: 'darwin',
  })
  assert.equal(completed.env.ANDROID_HOME, expectedSdk)
  assert.equal(completed.env.ANDROID_SDK_ROOT, expectedSdk)
})

test('arguments, generated auth token, and non-zero exit code pass through without a shell', posixOnly, async () => {
  const jdk23 = fakeJdk(23)
  const sdk = temporaryDirectory('sekaitext-android-sdk-')
  const fixture = fakeTauriCli()
  const args = ['build', '--debug', '--apk', '--target', 'aarch64', '--ci']
  const outputPath = join(fixture.directory, 'invocation.json')
  const logMessages = []
  const originalProcessToken = process.env.SEKAI_TEXT_AUTH_TOKEN

  const status = await runAndroidTauri(args, {
    commonJavaHomes: [],
    env: {
      ...process.env,
      ANDROID_HOME: sdk,
      ANDROID_SDK_ROOT: '',
      FAKE_TAURI_EXIT_CODE: '37',
      FAKE_TAURI_OUTPUT: outputPath,
      JAVA_HOME: '',
      SEKAI_ANDROID_JAVA_HOME: jdk23,
      SEKAI_TEXT_AUTH_TOKEN: '',
    },
    logger: recordingLogger(logMessages),
    platform: 'linux',
    stdio: 'ignore',
    tauriCliPath: fixture.cliPath,
  })

  const invocation = JSON.parse(readFileSync(outputPath, 'utf8'))
  const { authToken, ...invocationWithoutToken } = invocation
  assert.deepEqual(status, { code: 37, signal: null })
  assert.deepEqual(invocationWithoutToken, {
    androidHome: sdk,
    androidSdkRoot: sdk,
    args: ['android', ...args],
    javaHome: resolve(jdk23),
  })
  assert.match(authToken, /^[A-Za-z0-9_-]{43}$/)
  assert.ok(logMessages.every(message => !message.includes(authToken)))
  assert.equal(process.env.SEKAI_TEXT_AUTH_TOKEN, originalProcessToken)
})

test('an explicit auth token is preserved and never logged', posixOnly, async () => {
  const jdk23 = fakeJdk(23)
  const sdk = temporaryDirectory('sekaitext-android-sdk-')
  const fixture = fakeTauriCli()
  const outputPath = join(fixture.directory, 'invocation.json')
  const explicitToken = 'user-provided-android-dev-token'
  const logMessages = []

  const status = await runAndroidTauri(['dev'], {
    commonJavaHomes: [],
    env: {
      ...process.env,
      ANDROID_HOME: sdk,
      ANDROID_SDK_ROOT: sdk,
      FAKE_TAURI_OUTPUT: outputPath,
      JAVA_HOME: '',
      SEKAI_ANDROID_JAVA_HOME: jdk23,
      SEKAI_TEXT_AUTH_TOKEN: explicitToken,
    },
    logger: recordingLogger(logMessages),
    platform: 'linux',
    stdio: 'ignore',
    tauriCliPath: fixture.cliPath,
  })

  assert.deepEqual(status, { code: 0, signal: null })
  assert.equal(JSON.parse(readFileSync(outputPath, 'utf8')).authToken, explicitToken)
  assert.ok(logMessages.every(message => !message.includes(explicitToken)))
})

test('child termination signal is reported for CLI propagation', posixOnly, async () => {
  const fixture = fakeTauriCli()
  const status = await spawnLocalTauriAndroid(['--help'], {
    cwd: fixture.directory,
    env: { ...process.env, FAKE_TAURI_SIGNAL: 'SIGTERM' },
    nodeExecutable: process.execPath,
    stdio: 'ignore',
    tauriCliPath: fixture.cliPath,
  })

  assert.deepEqual(status, { code: null, signal: 'SIGTERM' })
})

function fakeJdk(major) {
  const javaHome = temporaryDirectory(`sekaitext-jdk-${major}-`)
  const bin = join(javaHome, 'bin')
  mkdirSync(bin)
  const javaScript = `#!/usr/bin/env node\nprocess.stderr.write('openjdk version "${major}.0.1" 2099-01-01\\n')\n`
  for (const command of ['java', 'javac']) {
    const commandPath = join(bin, command)
    writeFileSync(commandPath, javaScript)
    chmodSync(commandPath, 0o755)
  }
  return javaHome
}

function fakeTauriCli() {
  const directory = temporaryDirectory('sekaitext-fake-tauri-')
  const cliPath = join(directory, 'tauri.mjs')
  writeFileSync(cliPath, `
import { writeFileSync } from 'node:fs'

if (process.env.FAKE_TAURI_OUTPUT) {
  writeFileSync(process.env.FAKE_TAURI_OUTPUT, JSON.stringify({
    androidHome: process.env.ANDROID_HOME,
    androidSdkRoot: process.env.ANDROID_SDK_ROOT,
    args: process.argv.slice(2),
    authToken: process.env.SEKAI_TEXT_AUTH_TOKEN,
    javaHome: process.env.JAVA_HOME,
  }))
}
if (process.env.FAKE_TAURI_SIGNAL) process.kill(process.pid, process.env.FAKE_TAURI_SIGNAL)
else process.exit(Number.parseInt(process.env.FAKE_TAURI_EXIT_CODE || '0', 10))
`)
  return { cliPath, directory }
}

function temporaryDirectory(prefix) {
  const directory = mkdtempSync(join(tmpdir(), prefix))
  temporaryDirectories.push(directory)
  return directory
}

function recordingLogger(messages) {
  return {
    log(message) { messages.push(String(message)) },
    warn(message) { messages.push(String(message)) },
  }
}
