import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { createReadStream, existsSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { basename, join } from 'node:path'

import {
  assertReleaseCdnPublication,
  buildReleaseManifest,
  obsoleteStableReleaseTags,
  signReleaseManifest,
  verifyReleaseManifest,
} from './release-manifest-lib.mjs'

const tag = required('RELEASE_TAG')
const repository = required('GITHUB_REPOSITORY')
const accessKeyId = required('OSS_ACCESS_KEY_ID')
const accessKeySecret = required('OSS_ACCESS_KEY_SECRET')
const region = process.env.OSS_REGION || 'cn-shanghai'
const bucket = process.env.OSS_BUCKET || 'sakimizuki'
const cdnOrigin = (process.env.CDN_ORIGIN || 'https://sakimizuki.accr.cc').replace(/\/$/, '')
const version = tag.replace(/^v/, '')
const suppliedAssetDir = process.env.RELEASE_ASSET_DIR
const assetDir = suppliedAssetDir || mkdtempSync(join(tmpdir(), `sekaitext-${tag}-`))
const signing = {
  keyId: required('APP_UPDATE_SIGNING_KEY_ID'),
  privateKey: required('APP_UPDATE_SIGNING_PRIVATE_KEY'),
}
const appUpdatePublicKeys = JSON.parse(required('SEKAITEXT_APP_UPDATE_PUBLIC_KEYS'))
// The signing key is needed only in this process. Do not leak it to gh/ossutil.
delete process.env.APP_UPDATE_SIGNING_PRIVATE_KEY

const ossAuth = [
  '--region', region,
  '--access-key-id', accessKeyId,
  '--access-key-secret', accessKeySecret,
]

try {
  const latestRelease = JSON.parse(run('gh', [
    'release', 'view',
    '--repo', repository,
    '--json', 'tagName',
  ]))
  if (latestRelease.tagName !== tag) {
    throw new Error(`refusing to replace the CDN latest release ${latestRelease.tagName} with ${tag}`)
  }
  const release = JSON.parse(run('gh', [
    'release', 'view', tag,
    '--repo', repository,
    '--json', 'body,assets,publishedAt',
  ]))
  const installers = release.assets.filter((asset) => /\.(?:dmg|exe)$/i.test(asset.name))
  const dmg = installers.filter((asset) => asset.name.toLowerCase().endsWith('.dmg'))
  const exe = installers.filter((asset) => asset.name.toLowerCase().endsWith('.exe'))
  if (dmg.length !== 1 || exe.length !== 1) {
    throw new Error(`expected one .dmg and one .exe, found ${dmg.length} and ${exe.length}`)
  }

  const manifestSource = existsSync('app-release.json')
    ? JSON.parse(readFileSync('app-release.json', 'utf8'))
    : {
        version,
        notes: release.body || '',
        pubDate: (release.publishedAt || new Date().toISOString()).slice(0, 10),
      }
  manifestSource.version = version
  manifestSource.notes = release.body || manifestSource.notes || ''
  manifestSource.pubDate ||= (release.publishedAt || new Date().toISOString()).slice(0, 10)
  const cdnManifestData = signReleaseManifest(buildReleaseManifest(manifestSource, {
    version,
    assets: installers,
    urlForAsset: (asset) => `${cdnOrigin}/sekaitext-releases/${tag}/${asset.name}`,
  }), signing)
  const cdnManifest = join(assetDir, 'app-release-cdn.json')
  const encodedManifest = JSON.stringify(cdnManifestData, null, 2) + '\n'
  // Every distribution point serves the same signed payload. Download-source
  // selection rewrites the artifact URLs at request time without changing trust.
  writeFileSync('app-release.json', encodedManifest)
  writeFileSync(cdnManifest, encodedManifest)

  const currentManifestResponse = await fetch(
    `${cdnOrigin}/sekaitext-plugins/app-release.json?monotonic-check=${Date.now()}`,
    { cache: 'no-store', signal: AbortSignal.timeout(30_000) },
  )
  if (currentManifestResponse.ok) {
    const currentManifestBytes = Buffer.from(await currentManifestResponse.arrayBuffer())
    let currentManifest
    try {
      currentManifest = JSON.parse(currentManifestBytes.toString('utf8'))
    } catch {
      throw new Error('live app-release.json is not valid JSON')
    }
    if (assertReleaseCdnPublication(
      cdnManifestData,
      Buffer.from(encodedManifest),
      currentManifest,
      currentManifestBytes,
      appUpdatePublicKeys,
    )) {
      console.log(`[release-cdn] ${version} manifest already published byte-for-byte; resuming idempotent artifact verification`)
    }
  } else if (currentManifestResponse.status !== 404) {
    throw new Error(`could not read live app-release.json (${currentManifestResponse.status})`)
  }

  if (!suppliedAssetDir) {
    for (const asset of installers) {
      retry(`download ${asset.name}`, () => run('gh', [
        'release', 'download', tag,
        '--repo', repository,
        '--pattern', asset.name,
        '--dir', assetDir,
        '--clobber',
      ], { stdio: 'inherit' }))
    }
  }

  for (const asset of installers) {
    const file = join(assetDir, asset.name)
    if (!existsSync(file)) throw new Error(`downloaded asset is missing: ${file}`)
    if (statSync(file).size !== asset.size) {
      throw new Error(`${asset.name} size mismatch: ${statSync(file).size} != ${asset.size}`)
    }
    const actualDigest = `sha256:${await sha256(file)}`
    if (!asset.digest || actualDigest !== asset.digest.toLowerCase()) {
      throw new Error(`${asset.name} digest mismatch: ${actualDigest} != ${asset.digest}`)
    }
  }

  const releasePrefix = `oss://${bucket}/sekaitext-releases/${tag}/`
  for (const asset of installers) {
    const file = join(assetDir, asset.name)
    const object = releasePrefix + asset.name
    oss([
      'cp', file, object,
      '--force',
      '--cache-control', 'public, max-age=31536000, immutable',
    ], { stdio: 'inherit' })
    const remoteSize = Number(oss(['stat', object]).match(/Content-Length\s*:\s*(\d+)/)?.[1])
    if (remoteSize !== asset.size) {
      throw new Error(`${object} size mismatch after upload: ${remoteSize} != ${asset.size}`)
    }
  }

  // Retain the current and immediately previous stable installers. Unknown or
  // prerelease prefixes are left alone rather than being deleted accidentally.
  const releasesRoot = `oss://${bucket}/sekaitext-releases/`
  const listing = oss(['ls', releasesRoot, '--recursive'])
  const escapedRoot = releasesRoot.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const versions = new Set(
    [...listing.matchAll(new RegExp(`${escapedRoot}(v[^/]+)/`, 'g'))].map((match) => match[1]),
  )
  for (const oldTag of obsoleteStableReleaseTags(tag, versions)) {
    oss(['rm', `${releasesRoot}${oldTag}/`, '--recursive', '--force'], { stdio: 'inherit' })
  }

  for (const asset of installers) {
    const url = `${cdnOrigin}/sekaitext-releases/${tag}/${asset.name}`
    const response = await fetch(url, { cache: 'no-store', signal: AbortSignal.timeout(10 * 60 * 1000) })
    if (!response.ok || Number(response.headers.get('content-length')) !== asset.size) {
      throw new Error(`${url} failed CDN verification (${response.status})`)
    }
    const actual = await sha256WebResponse(response)
    if (actual.size !== asset.size || `sha256:${actual.digest}` !== asset.digest.toLowerCase()) {
      throw new Error(`${url} content failed CDN digest/size verification`)
    }
  }

  // Publish latest metadata only after every immutable artifact is reachable and
  // byte-for-byte verified. Clients persist this signed version for anti-rollback.
  oss([
    'cp', cdnManifest, `oss://${bucket}/sekaitext-plugins/app-release.json`,
    '--force',
    '--cache-control', 'no-cache, no-store, must-revalidate',
  ], { stdio: 'inherit' })

  const manifestUrl = `${cdnOrigin}/sekaitext-plugins/app-release.json?release-check=${Date.now()}`
  const liveManifestResponse = await fetch(manifestUrl, { cache: 'no-store' })
  if (!liveManifestResponse.ok) throw new Error(`${manifestUrl} returned ${liveManifestResponse.status}`)
  const liveManifest = await liveManifestResponse.json()
  verifyReleaseManifest(liveManifest, cdnManifestData)

  console.log(`[release-cdn] ${tag}: ${installers.map((asset) => basename(asset.name)).join(', ')} published and verified`)
} finally {
  if (!suppliedAssetDir) rmSync(assetDir, { recursive: true, force: true })
}

function required(name) {
  const value = process.env[name]
  if (!value) throw new Error(`${name} is required`)
  return value
}

function run(command, args, options = {}) {
  const inherited = {}
  for (const name of ['PATH', 'HOME', 'TMPDIR', 'LANG', 'LC_ALL', 'CI', 'GITHUB_ACTIONS']) {
    if (process.env[name] != null) inherited[name] = process.env[name]
  }
  if (command === 'gh' && process.env.GH_TOKEN) inherited.GH_TOKEN = process.env.GH_TOKEN
  return execFileSync(command, args, {
    encoding: 'utf8',
    env: inherited,
    ...options,
  })
}

function oss(args, options) {
  return run('ossutil', [...ossAuth, ...args], options)
}

function sha256(file) {
  return new Promise((resolve, reject) => {
    const hash = createHash('sha256')
    createReadStream(file)
      .on('error', reject)
      .on('data', (chunk) => hash.update(chunk))
      .on('end', () => resolve(hash.digest('hex')))
  })
}

async function sha256WebResponse(response) {
  if (!response.body) throw new Error('CDN response has no body')
  const hash = createHash('sha256')
  let size = 0
  for await (const chunk of response.body) {
    hash.update(chunk)
    size += chunk.byteLength
  }
  return { digest: hash.digest('hex'), size }
}

function retry(label, operation, attempts = 4) {
  let lastError
  for (let attempt = 1; attempt <= attempts; attempt++) {
    try {
      return operation()
    } catch (error) {
      lastError = error
      if (attempt < attempts) {
        console.warn(`[release-cdn] ${label} failed (${attempt}/${attempts}), retrying...`)
      }
    }
  }
  throw lastError
}
