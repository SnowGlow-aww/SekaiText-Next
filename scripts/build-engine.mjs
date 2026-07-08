// Assemble src-tauri/resources/engine/ for the Tauri bundle: the SekaiCoreEngine
// sidecar (打轴/压制) + its native libs + a static libass ffmpeg, ad-hoc signed on
// macOS (no Apple cert). The packaged backend resolves EnginePath = <Resources>/engine/.
//
// Inputs (env):
//   TAURI_TARGET  target triple (auto-detected from os when unset)
//   ENGINE_DIR    dir holding SekaiCoreEngine[.exe] + native libs (dotnet publish output).
//                 Defaults to ../backend/engine. In CI this is the avalonia engine artifact.
//   FFMPEG_BIN    path to the static libass ffmpeg for this target (osxexperts arm64 / BtbN win).
import { execSync } from 'child_process'
import { existsSync, mkdirSync, copyFileSync, cpSync, readdirSync, rmSync, statSync } from 'fs'
import { join } from 'path'
import { platform, arch } from 'os'

const AUTO = {
  'darwin-arm64': 'aarch64-apple-darwin',
  'win32-x64': 'x86_64-pc-windows-msvc',
}
const target = process.env.TAURI_TARGET || AUTO[`${platform()}-${arch()}`]
if (!target) { console.error('[build-engine] cannot determine TAURI_TARGET'); process.exit(1) }
const isMac = target.includes('apple-darwin')
const isWin = target.includes('windows')

const root = join(import.meta.dirname, '..')
const engineSrc = process.env.ENGINE_DIR || join(root, 'backend', 'engine')
const ffmpegBin = process.env.FFMPEG_BIN
const outDir = join(root, 'src-tauri', 'resources', 'engine')

const engExe = isWin ? 'SekaiCoreEngine.exe' : 'SekaiCoreEngine'
const libExt = isMac ? '.dylib' : isWin ? '.dll' : '.so'
const ffName = isWin ? 'ffmpeg.exe' : 'ffmpeg'

if (!existsSync(join(engineSrc, engExe))) {
  console.error(`[build-engine] engine binary not found: ${join(engineSrc, engExe)} (set ENGINE_DIR)`)
  process.exit(1)
}
if (!ffmpegBin || !existsSync(ffmpegBin)) {
  console.error(`[build-engine] FFMPEG_BIN not set or missing: ${ffmpegBin}`)
  process.exit(1)
}

rmSync(outDir, { recursive: true, force: true })
mkdirSync(outDir, { recursive: true })

// engine binary + native libs (skip .pdb debug symbols)
let copied = 0
for (const f of readdirSync(engineSrc)) {
  if (f === engExe || f.endsWith(libExt)) {
    copyFileSync(join(engineSrc, f), join(outDir, f))
    copied++
  }
}
copyFileSync(ffmpegBin, join(outDir, ffName))
if (!isWin) execSync(`chmod +x "${join(outDir, engExe)}" "${join(outDir, ffName)}"`)

// bundled subtitle fonts (思源黑体): Suppressor passes <engine dir>/fonts as libass
// fontsdir so machines without the fonts installed still render correct glyphs
// (otherwise macOS silently falls back to PingFang and subtitles come out narrow).
const fontsSrc = join(engineSrc, 'fonts')
let fontCount = 0
if (existsSync(fontsSrc)) {
  cpSync(fontsSrc, join(outDir, 'fonts'), { recursive: true })
  fontCount = readdirSync(fontsSrc).length
} else {
  console.warn('[build-engine] WARNING: no fonts/ in ENGINE_DIR — burned subtitles will fall back to system fonts')
}
console.log(`[build-engine] ${target}: ${copied} engine file(s) + ffmpeg + ${fontCount} font file(s) -> ${outDir}`)

// ad-hoc deep-sign nested Mach-O (no Apple cert). Proven in P0: dylibs ad-hoc, ffmpeg
// ad-hoc, engine with hardened runtime + disable-library-validation so it loads them.
if (isMac) {
  const ent = join(root, 'src-tauri', 'engine.entitlements')
  for (const f of readdirSync(outDir)) {
    if (f.endsWith('.dylib')) execSync(`codesign --force -s - "${join(outDir, f)}"`, { stdio: 'inherit' })
  }
  execSync(`codesign --force -s - "${join(outDir, ffName)}"`, { stdio: 'inherit' })
  execSync(`codesign --force --options runtime --entitlements "${ent}" -s - "${join(outDir, engExe)}"`, { stdio: 'inherit' })
  console.log('[build-engine] ad-hoc signed engine + libs + ffmpeg')
}

const total = readdirSync(outDir).reduce((n, f) => n + statSync(join(outDir, f)).size, 0)
console.log(`[build-engine] bundle size: ${(total / 1e6).toFixed(1)} MB`)
