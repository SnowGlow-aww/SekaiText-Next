import assert from 'node:assert/strict'
import { mkdtemp, readFile, rename, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'

import {
  gomobileExecutablePath,
  prependExecutableDirectory,
  replaceFilePreservingPrevious,
} from './build-mobile-core.mjs'

test('gomobile path and PATH separator follow the target host platform', () => {
  assert.equal(gomobileExecutablePath('C:\\Users\\test\\go', 'win32').endsWith('gomobile.exe'), true)
  assert.equal(gomobileExecutablePath('/Users/test/go', 'darwin').endsWith('/gomobile'), true)
  assert.equal(
    prependExecutableDirectory('C:\\Windows\\System32', 'C:\\Go\\bin\\gomobile.exe', 'win32'),
    'C:\\Go\\bin;C:\\Windows\\System32',
  )
  assert.equal(
    prependExecutableDirectory('/usr/bin', '/Users/test/go/bin/gomobile', 'darwin'),
    '/Users/test/go/bin:/usr/bin',
  )
})

test('Windows replacement preserves the old AAR when committing the new file fails', async () => {
  const root = await mkdtemp(join(tmpdir(), 'sekaitext-mobilecore-replace-'))
  const temporary = join(root, 'mobilecore.next.aar')
  const destination = join(root, 'mobilecore.aar')
  await writeFile(temporary, 'new-aar')
  await writeFile(destination, 'old-aar')

  let renameCount = 0
  const failingRename = async (from, to) => {
    renameCount++
    if (renameCount === 2) {
      const error = new Error('simulated Windows target replacement failure')
      error.code = 'EPERM'
      throw error
    }
    await rename(from, to)
  }

  try {
    await assert.rejects(
      replaceFilePreservingPrevious(temporary, destination, {
        platform: 'win32',
        renameImpl: failingRename,
      }),
      /simulated Windows target replacement failure/u,
    )
    assert.equal(await readFile(destination, 'utf8'), 'old-aar')
    assert.equal(await readFile(temporary, 'utf8'), 'new-aar')
  } finally {
    await rm(root, { recursive: true, force: true })
  }
})

test('Windows replacement commits a validated AAR over an existing file', async () => {
  const root = await mkdtemp(join(tmpdir(), 'sekaitext-mobilecore-replace-'))
  const temporary = join(root, 'mobilecore.next.aar')
  const destination = join(root, 'mobilecore.aar')
  await writeFile(temporary, 'new-aar')
  await writeFile(destination, 'old-aar')

  try {
    await replaceFilePreservingPrevious(temporary, destination, { platform: 'win32' })
    assert.equal(await readFile(destination, 'utf8'), 'new-aar')
  } finally {
    await rm(root, { recursive: true, force: true })
  }
})
