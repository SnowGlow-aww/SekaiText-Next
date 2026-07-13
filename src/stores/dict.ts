import { defineStore } from 'pinia'
import { ref, shallowRef } from 'vue'
import { api } from '../api/client'
import { foldForMatch } from '../composables/useGlossaryMatcher'
import type { DictInfo, DictLookupHit } from '../types/glossary'

// 字典 store：只读词典分类的前端侧。持有字典列表、编辑器取词用的表面形匹配
// 数据（折叠 surface → 原样 surface）和查词缓存。与术语库 store 完全独立——
// 字典绝不进入 glossary.json 主库 / 导出 / 团队同步。
export const useDictStore = defineStore('dict', () => {
  const dicts = ref<DictInfo[]>([])

  async function fetchDicts() {
    dicts.value = await api.dictList()
  }

  // --- 编辑器取词匹配数据 ---
  // foldedMap：折叠 surface → 原样 surface（同一折叠形撞车时首个 wins）。
  // maxLen：折叠后 surface 的最大 UTF-16 长度，matchDict 扫描的探测上限——
  // 直接从建好的键算（探测按 UTF-16 切片，服务器给的 maxLen 是 rune 数，单位
  // 不一致，含增补面字符时会漏探）。
  const surfacesLoaded = ref(false)
  // shallowRef 存裸 Map/Set：匹配器在行渲染热路径上做高频 .get/.has 子串探测，
  // 深响应式代理的依赖追踪开销会按探测次数放大；派生数据只会整体替换引用，
  // 浅响应足以触发更新。firstChars 是全部折叠 surface 的首个 UTF-16 码元集合，
  // matchDict 用它一步跳过绝大多数不可能命中的位置。
  const foldedMap = shallowRef<Map<string, string>>(new Map())
  const firstChars = shallowRef<Set<number>>(new Set())
  const maxLen = ref(0)

  async function loadSurfaces() {
    const r = await api.dictSurfaces()
    const m = new Map<string, string>()
    const fc = new Set<number>()
    let ml = 0
    for (const s of r.surfaces) {
      const f = foldForMatch(s)
      if (!m.has(f)) m.set(f, s)
      fc.add(f.charCodeAt(0))
      if (f.length > ml) ml = f.length
    }
    foldedMap.value = m
    firstChars.value = fc
    maxLen.value = ml
    surfacesLoaded.value = true
  }

  // --- 查词（悬浮卡片用），按原样 surface 缓存 ---
  const lookupCache = new Map<string, DictLookupHit[]>()

  async function lookup(surface: string): Promise<DictLookupHit[]> {
    const cached = lookupCache.get(surface)
    if (cached !== undefined) return cached
    const r = await api.dictLookup(surface)
    lookupCache.set(surface, r.items)
    return r.items
  }

  // 重建全部派生数据：列表 + 匹配数据 + 清查词缓存。无字典时置空匹配数据，
  // 编辑器的字典层自然停用（foldedMap.size === 0）。
  async function refresh() {
    lookupCache.clear()
    await fetchDicts()
    if (dicts.value.length > 0) {
      await loadSurfaces()
    } else {
      foldedMap.value = new Map()
      firstChars.value = new Set()
      maxLen.value = 0
      surfacesLoaded.value = true
    }
  }

  // 编辑器开启字典取词时调用；整个会话只拉一次（导入/删除会强制刷新）。
  // 失败时清掉 promise，下次进入编辑器可重试。
  let ensurePromise: Promise<void> | null = null
  function ensureLoaded(): Promise<void> {
    if (!ensurePromise) {
      ensurePromise = refresh().catch((e) => {
        ensurePromise = null
        throw e
      })
    }
    return ensurePromise
  }

  // 导入/删除成功后的派生数据刷新：瞬时失败不该把操作本身报成失败（后端已
  // 落盘），更不能把 rejected promise 留在 ensurePromise 里——那会让此后每次
  // ensureLoaded 都拿到同一个 rejected promise，字典取词整个会话内死掉。失败时
  // 置空允许下次重试，并如实告警。
  async function refreshAfterMutation() {
    const p = refresh().catch((e) => {
      ensurePromise = null
      console.warn('[dict] 字典数据刷新失败（操作本身已成功），下次进入编辑器将重试:', e)
    })
    ensurePromise = p
    await p
  }

  async function importDict(file: File): Promise<DictInfo> {
    const info = await api.dictImport(file)
    await refreshAfterMutation()
    return info
  }

  async function removeDict(name: string) {
    await api.dictDelete(name)
    await refreshAfterMutation()
  }

  return {
    dicts, fetchDicts,
    surfacesLoaded, foldedMap, firstChars, maxLen, loadSurfaces, ensureLoaded,
    lookup, importDict, removeDict,
  }
})
