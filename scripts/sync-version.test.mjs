import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'

import {
  VERSION_FILES,
  buildReleasePlan,
  checkVersionConsistency,
  syncVersion,
} from './sync-version.mjs'

test('version file collection and release plan cover every manifest, lock, and current-version document', () => {
  assert.deepEqual(VERSION_FILES, [
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
  assert.deepEqual(buildReleasePlan('5.9.0'), {
    branch: 'main',
    tag: 'v5.9.0',
    files: [...VERSION_FILES],
  })
})

test('syncVersion updates every version location and check validates the tag', () => {
  const root = fixture('5.9.0', '1.0.0')
  assert.equal(syncVersion(root), '5.9.0')
  assert.equal(checkVersionConsistency(root, 'v5.9.0'), '5.9.0')

  const lock = JSON.parse(readFileSync(join(root, 'package-lock.json'), 'utf8'))
  assert.equal(lock.version, '5.9.0')
  assert.equal(lock.packages[''].version, '5.9.0')
  assert.match(
    readFileSync(join(root, 'backend', 'internal', 'service', 'app_update.go'), 'utf8'),
    /CurrentAppVersion = "5\.9\.0"/,
  )
  assert.match(readFileSync(join(root, 'README.md'), 'utf8'), /version-5\.9\.0-blue/)
  assert.match(readFileSync(join(root, 'README.en.md'), 'utf8'), /Current version: \*\*5\.9\.0\*\*/)
  assert.match(readFileSync(join(root, 'website', 'guide', 'autotiming.md'), 'utf8'), /主程序 ≥ 5\.9\.0/)
  assert.match(readFileSync(join(root, 'website', 'guide', 'faq.md'), 'utf8'), /升级到 \*\*5\.9\.0\*\*/)
})

test('checkVersionConsistency rejects drift and a mismatched release tag', () => {
  const root = fixture('5.9.0', '5.8.13')
  assert.throws(() => checkVersionConsistency(root), /version mismatch/)
  syncVersion(root)
  assert.throws(() => checkVersionConsistency(root, 'v5.8.13'), /does not match/)
})

test('checkVersionConsistency identifies documentation-only drift', () => {
  const root = fixture('5.9.0', '1.0.0')
  syncVersion(root)
  const readme = join(root, 'README.md')
  writeFileSync(readme, readFileSync(readme, 'utf8').replace('当前版本：**5.9.0**', '当前版本：**5.8.13**'))
  assert.throws(() => checkVersionConsistency(root), /README\.md \(current version\)=5\.8\.13/)
})

function fixture(packageVersion, otherVersion) {
  const root = mkdtempSync(join(tmpdir(), 'sekaitext-version-'))
  mkdirSync(join(root, 'src-tauri'))
  mkdirSync(join(root, 'backend', 'internal', 'service'), { recursive: true })
  mkdirSync(join(root, 'website', 'guide'), { recursive: true })
  writeJSON(join(root, 'package.json'), { version: packageVersion })
  writeJSON(join(root, 'package-lock.json'), {
    version: otherVersion,
    packages: { '': { version: otherVersion } },
  })
  writeJSON(join(root, 'src-tauri', 'tauri.conf.json'), { version: otherVersion })
  writeFileSync(join(root, 'src-tauri', 'Cargo.toml'), `[package]\nname = "sekaitext"\nversion = "${otherVersion}"\n`)
  writeFileSync(join(root, 'src-tauri', 'Cargo.lock'), `[[package]]\nname = "sekaitext"\nversion = "${otherVersion}"\n`)
  writeFileSync(
    join(root, 'backend', 'internal', 'service', 'app_update.go'),
    `package service\n\nvar CurrentAppVersion = "${otherVersion}"\n`,
  )
  writeFileSync(
    join(root, 'README.md'),
    `badge/version-${otherVersion}-blue\n当前版本：**${otherVersion}**\n`,
  )
  writeFileSync(
    join(root, 'README.en.md'),
    `badge/version-${otherVersion}-blue\nCurrent version: **${otherVersion}**\n`,
  )
  writeFileSync(
    join(root, 'website', 'guide', 'autotiming.md'),
    `本页按最新版描述（**主程序 ≥ ${otherVersion}，「自动轴机 + 压制」插件 ≥ 1.0.0**）。\n先升级到 **${otherVersion}**。\n`,
  )
  writeFileSync(
    join(root, 'website', 'guide', 'faq.md'),
    `建议直接升级到 **${otherVersion}**。\n`,
  )
  return root
}

function writeJSON(path, value) {
  writeFileSync(path, JSON.stringify(value, null, 2) + '\n')
}
