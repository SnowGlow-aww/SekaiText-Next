import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { dirname, join, relative, resolve, sep } from 'node:path'

const root = import.meta.dirname
const dist = join(root, '.vitepress', 'dist')
const base = normalizeBase(process.env.SITE_BASE || '/web/')

if (!existsSync(dist)) fail(`build output is missing: ${dist}`)

const files = walk(dist)
const htmlFiles = files.filter((file) => file.endsWith('.html'))
const cssFiles = files.filter((file) => file.endsWith('.css'))
const jsFiles = files.filter((file) => file.endsWith('.js'))
const failures = []
let checkedReferences = 0
let stylesheetReferences = 0
let scriptReferences = 0

for (const htmlFile of htmlFiles) {
  const html = readFileSync(htmlFile, 'utf8')
  const attributes = html.matchAll(/\b(href|src)=(['"])(.*?)\2/g)
  for (const [, attribute, , value] of attributes) {
    const target = localTarget(value, htmlFile)
    if (!target) continue
    checkedReferences++
    if (attribute === 'href' && target.clean.endsWith('.css')) stylesheetReferences++
    if (attribute === 'src' && target.clean.endsWith('.js')) scriptReferences++
    verifyTarget(target, htmlFile)
  }
}

for (const cssFile of cssFiles) {
  const css = readFileSync(cssFile, 'utf8')
  for (const match of css.matchAll(/url\((['"]?)(.*?)\1\)/g)) {
    const target = localTarget(match[2], cssFile)
    if (!target) continue
    checkedReferences++
    verifyTarget(target, cssFile)
  }
}

// VitePress splits pages into lazy JS chunks. HTML only points at the entry
// bundle, so checking markup alone misses broken dynamic imports.
for (const jsFile of jsFiles) {
  const js = readFileSync(jsFile, 'utf8')
  for (const match of js.matchAll(/\b(?:from\s*|import\s*\(\s*)(['"])(.*?)\1/g)) {
    const target = localTarget(match[2], jsFile)
    if (!target) continue
    checkedReferences++
    verifyTarget(target, jsFile)
  }
}

if (htmlFiles.length === 0) failures.push('no HTML pages were generated')
if (stylesheetReferences === 0) failures.push('no stylesheet reference was found')
if (scriptReferences === 0) failures.push('no module script reference was found')

if (failures.length > 0) {
  console.error(`[verify-site] failed for base ${base}:`)
  for (const failure of failures) console.error(`  - ${failure}`)
  process.exit(1)
}

console.log(
  `[verify-site] ${htmlFiles.length} page(s), ${checkedReferences} local reference(s), base ${base}: OK`,
)

function normalizeBase(value) {
  const withLeading = value.startsWith('/') ? value : `/${value}`
  return withLeading.endsWith('/') ? withLeading : `${withLeading}/`
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

function localTarget(rawValue, sourceFile) {
  const value = rawValue.trim()
  if (!value || /^(?:[a-z]+:|#|\/\/)/i.test(value)) return null

  const clean = decodeURIComponent(value.split(/[?#]/, 1)[0])
  if (!clean) return null

  if (clean.startsWith('/')) {
    if (base !== '/' && !clean.startsWith(base)) {
      failures.push(
        `${display(sourceFile)} references ${value}, which escapes configured base ${base}`,
      )
      return null
    }
    const pathFromBase = base === '/' ? clean.slice(1) : clean.slice(base.length)
    return { clean, value, file: resolveOutput(pathFromBase) }
  }

  return {
    clean,
    value,
    file: resolve(dirname(sourceFile), clean),
  }
}

function resolveOutput(pathFromBase) {
  return resolve(dist, pathFromBase || 'index.html')
}

function verifyTarget(target, sourceFile) {
  let file = target.file
  if (existsSync(file) && statSync(file).isDirectory()) file = join(file, 'index.html')
  if (!file.startsWith(dist + sep) && file !== dist) {
    failures.push(`${display(sourceFile)} references path outside dist: ${target.value}`)
    return
  }
  if (!existsSync(file)) {
    failures.push(`${display(sourceFile)} references missing file: ${target.value}`)
  }
}

function display(file) {
  return relative(dist, file) || 'index.html'
}

function fail(message) {
  console.error(`[verify-site] ${message}`)
  process.exit(1)
}
