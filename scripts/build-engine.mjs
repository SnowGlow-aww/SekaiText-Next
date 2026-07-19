// Assemble src-tauri/resources/engine/ for the Tauri bundle: the SekaiCoreEngine
// sidecar (打轴/压制) + its native libs + a static libass ffmpeg. macOS resources
// use Developer ID when APPLE_SIGNING_IDENTITY is present, otherwise ad-hoc signing.
// The packaged backend resolves EnginePath = <Resources>/engine/.
//
// Inputs (env):
//   TAURI_TARGET  target triple (auto-detected from os when unset)
//   ENGINE_DIR    dir holding SekaiCoreEngine[.exe] + native libs (dotnet publish output).
//                 Defaults to ../backend/engine. In CI this is the avalonia engine artifact.
//   FFMPEG_BIN    path to the static libass ffmpeg for this target (osxexperts arm64 / BtbN win).
import { execFileSync, execSync } from 'child_process'
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
const engPath = join(engineSrc, engExe)

if (!existsSync(engPath)) {
  console.error(`[build-engine] engine binary not found: ${engPath} (set ENGINE_DIR)`)
  process.exit(1)
}
if (!ffmpegBin || !existsSync(ffmpegBin)) {
  console.error(`[build-engine] FFMPEG_BIN not set or missing: ${ffmpegBin}`)
  process.exit(1)
}

// This bundler intentionally copies a self-contained single-file engine plus
// its native libraries. A normal `dotnet build` output also contains an apphost
// named SekaiCoreEngine, so an existence check alone accepts it and then omits
// all managed DLL/runtime files below. Reject that shape before touching the
// last known-good resources directory.
const frameworkDependentMarkers = [
  'SekaiCoreEngine.dll',
  'SekaiCoreEngine.deps.json',
  'SekaiCoreEngine.runtimeconfig.json',
].filter((f) => existsSync(join(engineSrc, f)))
const engineBytes = statSync(engPath).size
const MIN_SELF_CONTAINED_BYTES = 5 * 1024 * 1024
if (frameworkDependentMarkers.length || engineBytes < MIN_SELF_CONTAINED_BYTES) {
  console.error(
    `[build-engine] ENGINE_DIR is not a self-contained single-file publish: ${engineSrc}\n` +
    `  engine size: ${(engineBytes / 1e6).toFixed(1)} MB` +
    (frameworkDependentMarkers.length ? `; found: ${frameworkDependentMarkers.join(', ')}` : '') + '\n' +
    '  Run: dotnet publish -c Release -r <RID> --self-contained true -p:PublishSingleFile=true',
  )
  process.exit(1)
}

rmSync(outDir, { recursive: true, force: true })
mkdirSync(outDir, { recursive: true })

// engine binary + native libs (skip .pdb debug symbols) + font licenses.
// 字体许可证（OFL 要求随字体分发）在引擎发布根、不在 fonts/ 里——libass 会把
// fontsdir 目录下每个文件都当字体加载，txt 进 fonts/ 会在每份压制日志里打
// "Error opening memory font" 噪音。白名单拷贝必须显式带上它（5.8.8 曾漏掉）。
let copied = 0
for (const f of readdirSync(engineSrc)) {
  if (f === engExe || f.endsWith(libExt) || /^LICENSE.*\.txt$/i.test(f)) {
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

// Auto-timing resources are Content entries in SekaiToolsEngine.csproj, not
// embedded in the single-file executable. Preserve them beside the engine so
// clean/offline installs retain the bundled templates, fonts and thresholds.
const videoProcessSrc = join(engineSrc, 'videoProcess')
let videoProcessCount = 0
if (existsSync(videoProcessSrc)) {
  cpSync(videoProcessSrc, join(outDir, 'videoProcess'), { recursive: true })
  videoProcessCount = readdirSync(videoProcessSrc).length
} else {
  console.warn('[build-engine] WARNING: no videoProcess/ in ENGINE_DIR — auto-timing may need network fallback')
}
const videoProcessManifest = join(engineSrc, 'videoProcess.json')
if (existsSync(videoProcessManifest)) {
  copyFileSync(videoProcessManifest, join(outDir, 'videoProcess.json'))
} else {
  console.warn('[build-engine] WARNING: no videoProcess.json in ENGINE_DIR')
}
console.log(`[build-engine] ${target}: ${copied} engine file(s) + ffmpeg + ${fontCount} font file(s) + ${videoProcessCount} timing resource(s) -> ${outDir}`)

// Sign nested Mach-O before Tauri seals the outer app. Using ad-hoc signatures here
// during a Developer ID build invalidates the final trust chain after distribution.
if (isMac) {
  const ent = join(root, 'src-tauri', 'engine.entitlements')
  const identity = process.env.APPLE_SIGNING_IDENTITY || '-'
  const sign = (file, entitlements) => {
    const args = ['--force', '--options', 'runtime']
    if (identity !== '-') args.push('--timestamp')
    if (entitlements) args.push('--entitlements', entitlements)
    args.push('--sign', identity, file)
    execFileSync('codesign', args, { stdio: 'inherit' })
  }
  for (const f of readdirSync(outDir)) {
    if (f.endsWith('.dylib')) sign(join(outDir, f))
  }
  sign(join(outDir, ffName))
  sign(join(outDir, engExe), ent)
  console.log(`[build-engine] ${identity === '-' ? 'ad-hoc' : 'Developer ID'} signed engine + libs + ffmpeg`)
}

const total = readdirSync(outDir).reduce((n, f) => n + statSync(join(outDir, f)).size, 0)
console.log(`[build-engine] bundle size: ${(total / 1e6).toFixed(1)} MB`)
