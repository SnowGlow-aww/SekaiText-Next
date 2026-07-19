import { execFileSync } from 'node:child_process'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, sep } from 'node:path'

const root = import.meta.dirname
const dist = join(root, '.vitepress', 'dist')
const destination = 'oss://sakimizuki/web/'
const origin = 'https://sakimizuki.accr.cc'
const base = '/web/'
const verifyOnly = process.argv.includes('--verify-only')
const configuredBase = normalizeBase(process.env.SITE_BASE || base)

if (configuredBase !== base) {
  console.error(`[deploy-site] production deploy requires SITE_BASE=${base}; received ${configuredBase}`)
  process.exit(1)
}

if (!verifyOnly) {
  // Hashed resources go first. HTML is uploaded last and never cached, so a
  // failed deployment leaves the previous page pointing at valid old assets.
  ossSync(join(dist, 'assets') + sep, destination + 'assets/', [
    '--cache-control', 'public, max-age=31536000, immutable',
  ])
  ossSync(dist + sep, destination, [
    '--exclude', 'assets/**',
    '--exclude', '*.html',
    '--cache-control', 'public, max-age=3600',
  ])
  ossSync(dist + sep, destination, [
    '--include', '*.html',
    '--cache-control', 'no-cache, no-store, must-revalidate',
  ])
}

await verifyRemote()

function ossSync(source, target, extra) {
  execFileSync('ossutil', [
    'sync', source, target,
    '--force',
    '--checksum',
    ...extra,
  ], { stdio: 'inherit' })
}

async function verifyRemote() {
  const files = walk(dist)
  const htmlFiles = files.filter((file) => file.endsWith('.html'))
  const assetUrls = new Set()
  const failures = []
  const cacheBust = Date.now()

  for (const htmlFile of htmlFiles) {
    const path = relative(dist, htmlFile).split(sep).join('/')
    const localHtml = readFileSync(htmlFile, 'utf8')
    const pageUrl = `${origin}${base}${path}`
    const response = await fetch(`${pageUrl}?deploy-check=${cacheBust}`, {
      headers: { 'cache-control': 'no-cache' },
    })
    if (!response.ok) {
      failures.push(`${pageUrl} returned ${response.status}`)
      continue
    }
    if (await response.text() !== localHtml) {
      failures.push(`${pageUrl} does not match the local build`)
      continue
    }
    collectReferences(localHtml, pageUrl, assetUrls)
  }

  const cssFiles = files.filter((file) => file.endsWith('.css'))
  for (const cssFile of cssFiles) {
    const path = relative(dist, cssFile).split(sep).join('/')
    collectCssReferences(readFileSync(cssFile, 'utf8'), `${origin}${base}${path}`, assetUrls)
  }

  const jsFiles = files.filter((file) => file.endsWith('.js'))
  for (const jsFile of jsFiles) {
    const path = relative(dist, jsFile).split(sep).join('/')
    collectJsReferences(readFileSync(jsFile, 'utf8'), `${origin}${base}${path}`, assetUrls)
  }

  // Verify every deployed build artifact, including lazy page chunks that may
  // not be reachable from the initial HTML until a user navigates.
  for (const file of files) {
    if (file.endsWith('.html')) continue
    const path = relative(dist, file).split(sep).join('/')
    assetUrls.add(`${origin}${base}${path}`)
  }

  const urls = [...assetUrls]
  for (let i = 0; i < urls.length; i += 12) {
    await Promise.all(urls.slice(i, i + 12).map(async (url) => {
      const response = await fetch(url, { method: 'HEAD' })
      if (!response.ok) failures.push(`${url} returned ${response.status}`)
    }))
  }

  if (failures.length > 0) {
    console.error('[deploy-site] remote verification failed:')
    for (const failure of failures) console.error(`  - ${failure}`)
    process.exit(1)
  }

  console.log(
    `[deploy-site] ${htmlFiles.length} live page(s), ${urls.length} unique asset(s): OK`,
  )
}

function collectReferences(content, sourceUrl, output) {
  for (const match of content.matchAll(/\b(?:href|src)=(['"])(.*?)\1/g)) {
    addLocalUrl(match[2], sourceUrl, output)
  }
}

function collectCssReferences(content, sourceUrl, output) {
  for (const match of content.matchAll(/url\((['"]?)(.*?)\1\)/g)) {
    addLocalUrl(match[2], sourceUrl, output)
  }
}

function collectJsReferences(content, sourceUrl, output) {
  for (const match of content.matchAll(/\b(?:from\s*|import\s*\(\s*)(['"])(.*?)\1/g)) {
    addLocalUrl(match[2], sourceUrl, output)
  }
}

function addLocalUrl(value, sourceUrl, output) {
  if (!value || /^(?:data:|#)/i.test(value)) return
  const url = new URL(value, sourceUrl)
  if (url.origin !== origin || !url.pathname.startsWith(base)) return
  url.hash = ''
  output.add(url.href)
}

function walk(directory) {
  const result = []
  for (const name of readdirSync(directory)) {
    const file = join(directory, name)
    if (statSync(file).isDirectory()) result.push(...walk(file))
    else result.push(file)
  }
  return result
}

function normalizeBase(value) {
  const withLeading = value.startsWith('/') ? value : `/${value}`
  return withLeading.endsWith('/') ? withLeading : `${withLeading}/`
}
