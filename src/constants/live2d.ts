// Live2D asset CDN config + URL builders.
//
// Hybrid sourcing (all fetched through the Go proxy /api/v1/live2d/proxy):
// - model_list.json (costume -> modelPath): sekai.best (only host with the index).
// - model bodies / textures / physics: exmeaning (storage2) full mirror, under
//   live2d/model/{modelPath}/ with .model3/.moc3/.physics3 (names from
//   buildmodeldata.json).
// - MOTION DATA: sekai.best only. exmeaning's motion/{name}.json are 233-byte
//   metadata stubs (no curves) -> models stay in T-pose. sekai.best ships full
//   motion3.json (with curves) under live2d/motion/{modelDir}/{base}_motion_base/.
// - backgrounds / BGM / scenario JSON: exmeaning.

export const SEKAI_BEST_LIVE2D = 'https://storage.sekai.best/sekai-live2d-assets'
export const EXMEANING_BASE = 'https://storage2.exmeaning.com/sekai-jp-assets'

// Backend origin. In the packaged Tauri app there is no vite dev-proxy and
// location.origin is `tauri://localhost`, so a relative `/api/...` URL resolves
// against the custom protocol and WebKit throws "The string did not match the
// expected pattern". Always target the Go sidecar's absolute address, matching
// api/client.ts's BASE_URL.
export const BACKEND_ORIGIN = 'http://localhost:9800'

/** Wrap an upstream CDN URL so it is fetched through the local backend proxy. */
export function proxied(url: string): string {
  return `${BACKEND_ORIGIN}/api/v1/live2d/proxy?url=${encodeURIComponent(url)}`
}

export const MODEL_LIST_URL = proxied(`${SEKAI_BEST_LIVE2D}/live2d/model_list.json`)

/** One entry of model_list.json (from sekai.best). */
export interface Live2DModelListElement {
  modelName: string
  modelBase: string // matches scenario CostumeType
  modelPath: string // e.g. "v2/main/20_mizuki/v2_20mizuki_casual"
  modelFile: string // sekai.best filename (unused for exmeaning loading)
}

// --- Model body: exmeaning ---

/** Directory URL (un-proxied) of a model on exmeaning. */
export function getModelDir(modelPath: string): string {
  return `${EXMEANING_BASE}/live2d/model/${modelPath}/`
}

/** buildmodeldata.json URL for a model on exmeaning (proxied). */
export function getBuildModelDataUrl(modelPath: string): string {
  return proxied(`${getModelDir(modelPath)}buildmodeldata.json`)
}

// --- Motion data: sekai.best ---

/** BuildMotionData.json (lists motions/expressions) on sekai.best (proxied). */
export function getMotionListUrl(modelDir: string, motionBase: string): string {
  return proxied(`${SEKAI_BEST_LIVE2D}/live2d/motion/${modelDir}/${motionBase}_motion_base/BuildMotionData.json`)
}

/** A single full motion3.json clip on sekai.best (proxied). */
export function getMotionUrl(modelDir: string, motionBase: string, name: string): string {
  return proxied(`${SEKAI_BEST_LIVE2D}/live2d/motion/${modelDir}/${motionBase}_motion_base/motion/${name}.motion3.json`)
}

/** A single expression motion3.json on sekai.best (proxied). */
export function getExpressionUrl(modelDir: string, motionBase: string, name: string): string {
  return proxied(`${SEKAI_BEST_LIVE2D}/live2d/motion/${modelDir}/${motionBase}_motion_base/facial/${name}.motion3.json`)
}

// --- Scenario media: exmeaning ---

/** Scenario background image (proxied, exmeaning). */
export function getBackgroundUrl(img: string): string {
  return proxied(`${EXMEANING_BASE}/scenario/background/${img}/${img}.webp`)
}

/** Scenario BGM (proxied, exmeaning). */
export function getBgmUrl(bgm: string): string {
  return proxied(`${EXMEANING_BASE}/sound/scenario/bgm/${bgm}/${bgm}.mp3`)
}
