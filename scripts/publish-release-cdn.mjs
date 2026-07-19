import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { createReadStream, existsSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { basename, join } from 'node:path'

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
    if (!asset.digest || actualDigest !== asset.digest) {
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
      '--size-only',
      '--cache-control', 'public, max-age=31536000, immutable',
    ], { stdio: 'inherit' })
    const remoteSize = Number(oss(['stat', object]).match(/Content-Length\s*:\s*(\d+)/)?.[1])
    if (remoteSize !== asset.size) {
      throw new Error(`${object} size mismatch after upload: ${remoteSize} != ${asset.size}`)
    }
  }

  // Keep only the current release on OSS. This runs after both new installers
  // have passed digest and remote-size checks, so cleanup cannot create an empty
  // download channel.
  const releasesRoot = `oss://${bucket}/sekaitext-releases/`
  const listing = oss(['ls', releasesRoot, '--recursive'])
  const escapedRoot = releasesRoot.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const versions = new Set(
    [...listing.matchAll(new RegExp(`${escapedRoot}(v[^/]+)/`, 'g'))].map((match) => match[1]),
  )
  for (const oldTag of versions) {
    if (oldTag === tag) continue
    oss(['rm', `${releasesRoot}${oldTag}/`, '--recursive', '--force'], { stdio: 'inherit' })
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
  manifestSource.downloads = {
    'darwin-aarch64': `${cdnOrigin}/sekaitext-releases/${tag}/${dmg[0].name}`,
    'windows-amd64': `${cdnOrigin}/sekaitext-releases/${tag}/${exe[0].name}`,
  }
  const cdnManifest = join(assetDir, 'app-release-cdn.json')
  writeFileSync(cdnManifest, JSON.stringify(manifestSource, null, 2) + '\n')
  oss([
    'cp', cdnManifest, `oss://${bucket}/sekaitext-plugins/app-release.json`,
    '--force',
    '--cache-control', 'no-cache, no-store, must-revalidate',
  ], { stdio: 'inherit' })

  for (const asset of installers) {
    const url = `${cdnOrigin}/sekaitext-releases/${tag}/${asset.name}`
    const response = await fetch(url, { method: 'HEAD', cache: 'no-store' })
    if (!response.ok || Number(response.headers.get('content-length')) !== asset.size) {
      throw new Error(`${url} failed CDN verification (${response.status})`)
    }
  }
  const manifestUrl = `${cdnOrigin}/sekaitext-plugins/app-release.json?release-check=${Date.now()}`
  const liveManifestResponse = await fetch(manifestUrl, { cache: 'no-store' })
  if (!liveManifestResponse.ok) throw new Error(`${manifestUrl} returned ${liveManifestResponse.status}`)
  const liveManifest = await liveManifestResponse.json()
  if (liveManifest.version !== version || JSON.stringify(liveManifest.downloads) !== JSON.stringify(manifestSource.downloads)) {
    throw new Error('live CDN manifest does not match the release')
  }

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
  return execFileSync(command, args, {
    encoding: 'utf8',
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
