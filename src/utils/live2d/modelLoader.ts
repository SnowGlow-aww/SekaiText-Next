// Live2D model resolution + motion injection.
//
// Hybrid flow:
// 1. model_list.json (sekai.best) maps a costume (CostumeType / modelBase) -> modelPath.
// 2. Model body from EXMEANING: buildmodeldata.json gives the moc file name (=>
//    model3 name); fetch that model3 and rewrite moc/textures/physics to proxied
//    exmeaning URLs.
// 3. Motion data from SEKAI.BEST: BuildMotionData.json lists motion/expression
//    names (modelBase shortened until found); each becomes a full motion3.json
//    URL. exmeaning's own motion files are empty stubs, hence this split.

import {
  MODEL_LIST_URL,
  BACKEND_ORIGIN,
  proxied,
  getModelDir,
  getBuildModelDataUrl,
  getMotionListUrl,
  getMotionUrl,
  getExpressionUrl,
  type Live2DModelListElement,
} from '../../constants/live2d'

let modelListCache: Live2DModelListElement[] | null = null

export async function fetchModelList(): Promise<Live2DModelListElement[]> {
  if (modelListCache) return modelListCache
  const res = await fetch(MODEL_LIST_URL)
  if (!res.ok) throw new Error(`model_list.json fetch failed: HTTP ${res.status}`)
  modelListCache = (await res.json()) as Live2DModelListElement[]
  return modelListCache
}

export async function resolveModel(costume: string): Promise<Live2DModelListElement | null> {
  const list = await fetchModelList()
  return list.find((m) => m.modelBase === costume) ?? null
}

interface BuildModelData {
  Moc3FileName: string // e.g. "v2_20mizuki_casual_t08.moc3.bytes"
}

interface MotionList {
  motions: string[]
  expressions: string[]
}

/**
 * Fetch sekai.best's BuildMotionData (motion/expression name list). modelDir =
 * modelPath minus last segment; modelBase is shortened segment-by-segment until
 * a hit (e.g. v2_20mizuki_casual -> v2_20mizuki). Returns the lists + resolved
 * modelDir/motionBase for building clip URLs, or null.
 */
async function loadMotionList(
  modelPath: string,
  modelBase: string,
): Promise<{ list: MotionList; modelDir: string; motionBase: string } | null> {
  const modelDir = modelPath.split('/').slice(0, -1).join('/')
  let base = modelBase
  while (base.length > 0) {
    const res = await fetch(getMotionListUrl(modelDir, base))
    if (res.ok) {
      return { list: (await res.json()) as MotionList, modelDir, motionBase: base }
    }
    if (base.split('_').length <= 1) break
    base = base.split('_').slice(0, -1).join('_')
  }
  return null
}

/**
 * Build a ready-to-load model3 settings object for a costume: body from
 * exmeaning, motions/expressions from sekai.best. Returns null if not in catalog.
 */
export async function loadModelSettings(costume: string): Promise<any | null> {
  const m = await resolveModel(costume)
  if (!m) return null
  const modelPath = m.modelPath
  const dir = getModelDir(modelPath)

  // 1. exmeaning buildmodeldata -> model3 base name
  const bmdRes = await fetch(getBuildModelDataUrl(modelPath))
  if (!bmdRes.ok) throw new Error(`buildmodeldata fetch failed: HTTP ${bmdRes.status}`)
  const bmd = (await bmdRes.json()) as BuildModelData
  const baseName = bmd.Moc3FileName.replace(/\.moc3(\.bytes)?$/, '')

  // 2. exmeaning model3 (standard format, no .json ext)
  const res = await fetch(proxied(`${dir}${baseName}.model3`))
  if (!res.ok) throw new Error(`model3 fetch failed: HTTP ${res.status}`)
  const json = await res.json()

  // 3. rewrite body file refs to proxied exmeaning URLs
  const ref = json.FileReferences ?? {}
  if (ref.Moc) ref.Moc = proxied(dir + ref.Moc)
  if (Array.isArray(ref.Textures)) ref.Textures = ref.Textures.map((t: string) => proxied(dir + t))
  if (ref.Physics) ref.Physics = proxied(dir + ref.Physics.replace(/\.physics3\.json$/, '.physics3'))

  // 4. inject motions/expressions from sekai.best (full motion3.json). Each motion
  // becomes its own group (group name = clip name) -> model.motion(name, 0).
  try {
    const mlist = await loadMotionList(modelPath, m.modelBase)
    if (mlist) {
      const { list, modelDir, motionBase } = mlist
      const motionGroups: Record<string, { File: string; FadeInTime: number; FadeOutTime: number }[]> = {}
      for (const name of list.motions || []) {
        motionGroups[name] = [{ File: getMotionUrl(modelDir, motionBase, name), FadeInTime: 0.5, FadeOutTime: 0.5 }]
      }
      const expressions = (list.expressions || []).map((name) => ({
        Name: name,
        File: getExpressionUrl(modelDir, motionBase, name),
      }))
      ref.Motions = motionGroups
      ref.Expressions = expressions
    }
  } catch {
    /* no motion data — model renders in default pose */
  }

  json.FileReferences = ref
  // All refs are already absolute proxied URLs; point the base at the backend
  // origin (not location.origin, which is tauri://localhost in the packaged app
  // and makes the loader throw "string did not match the expected pattern").
  json.url = BACKEND_ORIGIN + '/'
  return json
}
