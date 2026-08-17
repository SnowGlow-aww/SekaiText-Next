// Backend origin is injected by the Tauri shell (Rust `initialization_script`)
// as window.__SEKAI_ORIGIN__, which runs before the app JS. In packaged builds it
// points at the custom scheme (sekai://localhost or http://sekai.localhost); in
// dev (plain browser / Vite) the global is absent, so requests stay same-origin
// and Vite's proxy adds the per-run TCP capability without exposing it to JS.
import { capabilities } from '../platform/capabilities'
import { mobileCore } from '../platform/mobileCore'
import {
  clearMobileRecovery,
  loadMobileRecovery,
  loadMobileSettings,
  saveMobileRecovery,
  saveMobileSettings,
} from '../platform/mobileStorage'

export const ORIGIN = (typeof window !== 'undefined' && window.__SEKAI_ORIGIN__) || ''
export const BASE_URL = ORIGIN + '/api/v1'

function unsupportedOnAndroid<T>(feature: string): Promise<T> {
  return Promise.reject(new Error(`${feature} is not available on Android`))
}

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

export const DEFAULT_TIMEOUT_MS = 30_000
export const LONG_MUTATION_TIMEOUT_MS = 30 * 60_000

export interface RequestOptions extends RequestInit {
  timeoutMs?: number
}

export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const method = (options.method || 'GET').toUpperCase()
  const url = `${BASE_URL}${path}`
  const start = Date.now()
  const { timeoutMs = DEFAULT_TIMEOUT_MS, signal: callerSignal, ...fetchOptions } = options
  const controller = new AbortController()
  const forwardAbort = () => controller.abort(callerSignal?.reason)
  let timeout: ReturnType<typeof setTimeout> | undefined

  if (callerSignal) {
    if (callerSignal.aborted) forwardAbort()
    else callerSignal.addEventListener('abort', forwardAbort, { once: true })
  }
  if (timeoutMs > 0) {
    timeout = setTimeout(() => {
      controller.abort(new DOMException(`Request timed out after ${timeoutMs}ms`, 'TimeoutError'))
    }, timeoutMs)
  }

  try {
    if (controller.signal.aborted) throw controller.signal.reason
    const headers = new Headers(fetchOptions.headers)
    if (!headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
    const res = await fetch(url, {
      ...fetchOptions,
      headers,
      signal: controller.signal,
    })

    const elapsed = Date.now() - start

    if (!res.ok) {
      let body: any = null
      // Read the body exactly once as text, then try to parse it as JSON. The
      // stream can only be consumed one time, so a res.json()→res.text() fallback
      // would always reject ("body stream already read"). Non-JSON error bodies
      // (plain text / HTML) fall through as the raw string.
      const raw = await res.text().catch(() => '')
      try { body = raw ? JSON.parse(raw) : null } catch { body = raw }
      const errMsg = (typeof body === 'string' ? body : body?.error) || res.statusText
      const err = new ApiError(res.status, `${method} ${path} → ${res.status}: ${errMsg}`)
      console.error(`[API] ${method} ${path} → ${res.status} (${elapsed}ms)`, { error: errMsg, body })
      throw err
    }

    const raw = await res.text()
    const data = raw ? JSON.parse(raw) : undefined
    console.log(`[API] ${method} ${path} → ${res.status} (${elapsed}ms)`)
    return data
  } catch (e) {
    const elapsed = Date.now() - start
    if (e instanceof ApiError) throw e
    if (controller.signal.aborted) throw controller.signal.reason ?? e
    const wrap = new Error(`${method} ${path} → 网络请求失败: ${(e as Error).message}`)
    console.error(`[API] ${method} ${path} → NETWORK ERROR (${elapsed}ms)`, (e as Error).message)
    throw wrap
  } finally {
    if (timeout) clearTimeout(timeout)
    callerSignal?.removeEventListener('abort', forwardAbort)
  }
}


export const api = {
  // Story navigation
  storyTypes: () => capabilities.isAndroid
    ? mobileCore.storyTypes()
    : request<string[]>('/story/types'),

  storySorts: (type: string) => capabilities.isAndroid
    ? mobileCore.storySorts(type)
    : request<{ label: string; value: string }[]>(`/story/sorts?type=${encodeURIComponent(type)}`),

  storyIndex: (type: string, sort: string) => capabilities.isAndroid
    ? mobileCore.storyIndex(type, sort)
    : request<{ label: string; value: string; chapters?: number[] }[]>(
      `/story/index?type=${encodeURIComponent(type)}&sort=${encodeURIComponent(sort)}`,
    ),

  storyChapter: (type: string, sort: string, index: string) => capabilities.isAndroid
    ? mobileCore.storyChapters(type, sort, index)
    : request<{ number: number; label: string }[]>(
      `/story/chapter?type=${encodeURIComponent(type)}&sort=${encodeURIComponent(sort)}&index=${encodeURIComponent(index)}`,
    ),

  jsonPath: (type: string, sort: string, index: string, chapter: number, source: string) => capabilities.isAndroid
    ? mobileCore.storyJsonPath(type, sort, index, chapter, source)
    : request<{ url: string; fileName: string; saveTitle: string; chapterTitle: string }>(
      `/story/json-path?type=${encodeURIComponent(type)}&sort=${encodeURIComponent(sort)}&index=${encodeURIComponent(index)}&chapter=${chapter}&source=${encodeURIComponent(source)}`,
    ),

  storyLoad: (data: {
    storyType: string
    sort: string
    index: string
    chapter: number
    source: string
  }) => capabilities.isAndroid
    ? mobileCore.storyLoad(data)
    : request<{ scenarioId: string; sourceTalks: import('../types/translation').SourceTalk[]; saveTitle: string; chapterTitle: string; indexLabel: string }>(
      '/story/load',
      { method: 'POST', body: JSON.stringify(data) },
    ),

  storyLoadLocal: (content: string) => capabilities.isAndroid
    ? mobileCore.storyLoadLocal(content)
    : request<{ scenarioId: string; sourceTalks: import('../types/translation').SourceTalk[]; saveTitle: string; chapterTitle: string; indexLabel?: string }>(
      '/story/load-local',
      { method: 'POST', body: JSON.stringify({ content }) },
    ),

  resolveLabel: (label: string) => capabilities.isAndroid
    ? mobileCore.resolveStoryLabel(label)
    : request<{
      ok: boolean
      storyType: string
      index: string
      indexLabel: string
      chapter: number
      matchKind?: 'exact' | 'legacy'
      reason?: 'not-found' | 'exact-ambiguous' | 'legacy-ambiguous'
    }>(
      '/story/resolve-label',
      { method: 'POST', body: JSON.stringify({ label }) },
    ),

  storyCatalogStatus: () => capabilities.isAndroid
    ? mobileCore.storyCatalogStatus()
    : Promise.resolve({ ready: true, generation: 0, updating: false }),

  // Translation
  translationLoadContent: (content: string) => capabilities.isAndroid
    ? mobileCore.loadTranslation(content)
    : request<{
      talks: import('../types/translation').DstTalk[]
      meta: import('../types/api').SaveMetadata | null
    }>('/translation/load-content', {
      method: 'POST',
      body: JSON.stringify({ content }),
    }),

  translationSerialize: (data: {
    talks: import('../types/translation').DstTalk[]
    saveN: boolean
    meta?: import('../types/api').SaveMetadata
  }) => capabilities.isAndroid
    ? mobileCore.serializeTranslation(data)
    : request<{ content: string }>('/translation/serialize', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  translationCreate: (data: {
    sourceTalks: import('../types/translation').SourceTalk[]
    jp: boolean
  }) => capabilities.isAndroid
    ? mobileCore.createTranslation(data)
    : request<import('../types/translation').DstTalk[]>('/translation/create', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  translationLoad: (filePath: string) =>
    request<{
      talks: import('../types/translation').DstTalk[]
      meta: import('../types/api').SaveMetadata | null
    }>('/translation/load', {
      method: 'POST',
      body: JSON.stringify({ filePath }),
    }),

  translationSave: (
    filePath: string,
    talks: import('../types/translation').DstTalk[],
    saveN: boolean,
    meta?: import('../types/api').SaveMetadata,
    expectedExistingDigest = '',
  ) =>
    request<{
      status: 'saved' | 'unchanged' | 'overwrite-required' | 'overwrite-stale'
      existingDigest?: string
    }>('/translation/save', {
      method: 'POST',
      body: JSON.stringify({ filePath, talks, saveN, meta, expectedExistingDigest }),
    }),

  ensureDir: (path: string) =>
    request<{ dir: string }>('/translation/ensure-dir', {
      method: 'POST',
      body: JSON.stringify({ path }),
    }),

  renameFile: (oldPath: string, newPath: string) =>
    request<{ path: string }>('/translation/rename-file', {
      method: 'POST',
      body: JSON.stringify({ oldPath, newPath }),
    }),

  checkLines: (data: {
    sourceTalks: import('../types/translation').SourceTalk[]
    loadedTalks: import('../types/translation').DstTalk[]
  }) => capabilities.isAndroid
    ? mobileCore.checkLines(data)
    : request<import('../types/translation').DstTalk[]>('/translation/check-lines', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  compareText: (data: {
    referTalks: import('../types/translation').DstTalk[]
    checkTalks: import('../types/translation').DstTalk[]
    editorMode: number
  }) => capabilities.isAndroid
    ? mobileCore.compareText(data)
    : request<{ talks: import('../types/translation').DstTalk[]; dstTalks: import('../types/translation').DstTalk[] }>('/editor/compare', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  // Editor
  changeText: (data: {
    row: number
    text: string
    editorMode: number
    talks: import('../types/translation').DstTalk[]
    dstTalks: import('../types/translation').DstTalk[]
    referTalks: import('../types/translation').DstTalk[]
  }) => capabilities.isAndroid
    ? mobileCore.changeText(data)
    : request<{ talks: import('../types/translation').DstTalk[]; dstTalks: import('../types/translation').DstTalk[] }>(
      '/editor/change-text',
      { method: 'POST', body: JSON.stringify(data) },
    ),

  addLine: (data: {
    row: number
    talks: import('../types/translation').DstTalk[]
    dstTalks: import('../types/translation').DstTalk[]
    isProofreading: boolean
  }) => capabilities.isAndroid
    ? mobileCore.addLine(data)
    : request<{ talks: import('../types/translation').DstTalk[]; dstTalks: import('../types/translation').DstTalk[] }>(
      '/editor/add-line',
      { method: 'POST', body: JSON.stringify(data) },
    ),

  removeLine: (data: {
    row: number
    talks: import('../types/translation').DstTalk[]
    dstTalks: import('../types/translation').DstTalk[]
  }) => capabilities.isAndroid
    ? mobileCore.removeLine(data)
    : request<{ talks: import('../types/translation').DstTalk[]; dstTalks: import('../types/translation').DstTalk[] }>(
      '/editor/remove-line',
      { method: 'POST', body: JSON.stringify(data) },
    ),

  compare: (data: {
    referTalks: import('../types/translation').DstTalk[]
    checkTalks: import('../types/translation').DstTalk[]
    editorMode: number
  }) => request<{ talks: import('../types/translation').DstTalk[]; dstTalks: import('../types/translation').DstTalk[] }>('/editor/compare', {
    method: 'POST',
    body: JSON.stringify(data),
  }),

  replaceBrackets: (data: {
    row: number
    brackets: string
    talks: import('../types/translation').DstTalk[]
    dstTalks: import('../types/translation').DstTalk[]
  }) => capabilities.isAndroid
    ? mobileCore.replaceBrackets(data)
    : request<{ talks: import('../types/translation').DstTalk[]; dstTalks: import('../types/translation').DstTalk[] }>('/editor/replace-brackets', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  // Text check
  checkText: (data: { speaker: string; text: string }) => capabilities.isAndroid
    ? mobileCore.checkText(data)
    : request<{ text: string; checked: boolean; message?: string }>('/check/text', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  // Flashback
  flashbackAnalyze: (sourceTalks: import('../types/translation').SourceTalk[]) =>
    request<{ sourceTalks: import('../types/translation').SourceTalk[] }>(
      '/flashback/analyze',
      { method: 'POST', body: JSON.stringify({ sourceTalks }) },
    ),

  clueHints: (clue: string, lang = 'zh-cn') => capabilities.isAndroid
    ? Promise.resolve({ clue, hints: [clue] })
    : request<{ clue: string; hints: string[] }>(`/flashback/clue-hints?clue=${encodeURIComponent(clue)}&lang=${encodeURIComponent(lang)}`),

  voiceClues: () => request<Record<string, { id: number; title: string; name: string; chapters: { title: string }[]; cards: number[]; inferredVoiceIDs?: Record<string, unknown> }>>('/flashback/voice-clues'),

  // Voice
  voiceUrl: (scenarioId: string, voiceId: string, source: string, chara2d?: number) => capabilities.isAndroid
    ? mobileCore.voiceUrl(scenarioId, voiceId, source, chara2d)
    : request<{ url: string }>(
      `/voice/url?scenarioId=${encodeURIComponent(scenarioId)}&voiceId=${encodeURIComponent(voiceId)}&source=${encodeURIComponent(source)}` +
      (chara2d != null ? `&chara2d=${chara2d}` : ''),
    ),

  // Speaker
  speakerCount: (data: {
    talks: import('../types/translation').DstTalk[]
    sourceTalks: import('../types/translation').SourceTalk[]
  }) => capabilities.isAndroid
    ? mobileCore.speakerCount(data)
    : request<{ speakers: { japanese: string; chinese: string; count: number }[] }>('/speaker/count', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  // Recovery (autosave)
  recoverySave: (data: import('../editor/recovery').RecoverySaveRequestV2) => capabilities.isAndroid
    ? saveMobileRecovery(data)
    : request<{ status: string }>('/recovery/save', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  recoveryLoad: () => capabilities.isAndroid
    ? loadMobileRecovery()
    : request<import('../editor/recovery').RecoveryLoadResult>('/recovery/load'),

  recoveryClear: () => capabilities.isAndroid
    ? clearMobileRecovery()
    : request<{ status: string }>('/recovery/clear', { method: 'DELETE' }),

  // Debug log viewer/export
  debugLogs: (signal?: AbortSignal) =>
    request<{ timestamp: string; message: string }[]>('/debug/logs', { signal }),
  debugSaveLogs: (lines: string[]) =>
    request<{ status: string; lines: number; path: string }>('/debug/save', {
      method: 'POST',
      body: JSON.stringify({ lines }),
    }),

  // Settings
  getSettings: () => capabilities.isAndroid
    ? Promise.resolve(loadMobileSettings())
    : request<import('../types/api').Settings>('/settings'),
  putSettings: (settings: import('../types/api').Settings) => capabilities.isAndroid
    ? Promise.resolve(saveMobileSettings(settings))
    : request<import('../types/api').Settings>('/settings', {
      method: 'PUT',
      body: JSON.stringify(settings),
    }),

  openDataDir: () => capabilities.isAndroid
    ? unsupportedOnAndroid<{ dir: string }>('Opening the desktop data directory')
    : request<{ dir: string }>('/open-data-dir', { method: 'POST' }),
  openSaveDir: () => capabilities.isAndroid
    ? unsupportedOnAndroid<{ dir: string }>('Opening the desktop save directory')
    : request<{ dir: string }>('/save-dir/open', { method: 'POST' }),
  migrateSaveDir: (newDir: string) => capabilities.isAndroid
    ? unsupportedOnAndroid<import('../types/api').MigrateSaveDirResult>('Migrating the desktop save directory')
    : request<import('../types/api').MigrateSaveDirResult>('/save-dir/migrate', {
      method: 'POST',
      body: JSON.stringify({ newDir }),
      timeoutMs: LONG_MUTATION_TIMEOUT_MS,
    }),
  importSaveDir: (srcDir: string, targetDir?: string) => capabilities.isAndroid
    ? unsupportedOnAndroid<import('../types/api').ImportSaveDirResult>('Importing into the desktop save directory')
    : request<import('../types/api').ImportSaveDirResult>('/save-dir/import', {
      method: 'POST',
      body: JSON.stringify({ srcDir, targetDir }),
      timeoutMs: LONG_MUTATION_TIMEOUT_MS,
    }),
  openUrl: (url: string) => capabilities.isAndroid
    ? mobileCore.openUrl(url)
    : request<{ status: string }>('/open-url', { method: 'POST', body: JSON.stringify({ url }) }),

  importLive2D: (srcDir: string) =>
    request<{ dir: string; moved: number }>('/live2d/import', {
      method: 'POST',
      body: JSON.stringify({ srcDir }),
      timeoutMs: LONG_MUTATION_TIMEOUT_MS,
    }),

  // Built-in Android Live2D resolves reviewed CDN URLs through the native,
  // app-private cache. Desktop's signed plugin keeps using its local HTTP proxy.
  live2dAssetUrl: async (url: string) => capabilities.isAndroid
    ? mobileCore.resolveLive2DAsset(url)
    : { url: `${BASE_URL}/live2d/proxy?url=${encodeURIComponent(url)}`, mime: '', size: 0, cacheHit: false },

  // Plugins (management). The listing/entry-serving is handled directly by the
  // plugin-host loader against /plugins/*; these cover enable/disable + uninstall.
  pluginSetEnabled: (id: string, enabled: boolean, approveLocal = false) =>
    request<{ ok: boolean }>(`/plugins/${id}/enabled`, {
      method: 'POST',
      body: JSON.stringify({ enabled, approveLocal }),
    }),
  pluginUninstall: (id: string) =>
    request<{ ok: boolean }>(`/plugins/${id}`, { method: 'DELETE' }),
  pluginRollback: (id: string) =>
    request<Omit<import('../plugin-host/autoload').InstalledPlugin, 'enabled' | 'local' | 'loadToken' | 'provenance'>>(
      `/plugins/${id}/rollback`,
      { method: 'POST', timeoutMs: LONG_MUTATION_TIMEOUT_MS },
    ),
  pluginMarkGood: (
    id: string,
    version: string,
    loadToken: string,
    provenance?: import('../plugin-host/autoload').PluginProvenance,
    signal?: AbortSignal,
  ) => request<{ ok: boolean }>(`/plugins/${id}/mark-good`, {
    method: 'POST',
    body: JSON.stringify({ version, loadToken, provenance: provenance ?? null }),
    signal,
  }),
  // Install a .sekplugin package from a local file path (Tauri dialog → path,
  // or marketplace download → temp path). hostVersion gates minHostVersion.
  pluginInstall: (srcPath: string, hostVersion: string) =>
    // Backend returns a PluginManifest (no runtime `enabled` field); callers
    // re-fetch the list for enable-state. Omit `enabled` so it can't be misread.
    request<Omit<import('../plugin-host/autoload').InstalledPlugin, 'enabled'>>('/plugins/install', {
      method: 'POST',
      body: JSON.stringify({ srcPath, hostVersion }),
      timeoutMs: LONG_MUTATION_TIMEOUT_MS,
    }),

  // Plugin marketplace
  marketIndex: () =>
    request<import('../stores/market').MarketListing[]>('/market/index'),
  marketInstall: (id: string, hostVersion: string) =>
    // Backend returns a PluginManifest (no runtime `enabled` field); callers
    // re-fetch the list for enable-state. Omit `enabled` so it can't be misread.
    request<Omit<import('../plugin-host/autoload').InstalledPlugin, 'enabled'>>('/market/install', {
      method: 'POST',
      body: JSON.stringify({ id, hostVersion }),
      timeoutMs: LONG_MUTATION_TIMEOUT_MS,
    }),
  // Reinstall every installed plugin that has a newer market version. Silent
  // auto-update on boot; the summary drives a "已更新 N 个插件" toast.
  marketAutoUpdate: (hostVersion: string) =>
    request<import('../stores/appUpdate').AutoUpdateSummary>('/market/auto-update', {
      method: 'POST',
      body: JSON.stringify({ hostVersion }),
      timeoutMs: LONG_MUTATION_TIMEOUT_MS,
    }),

  // App self-update (本体 检查 → 下载 → 打开安装)
  appUpdateCheck: (current: string) =>
    request<import('../stores/appUpdate').AppUpdateInfo>(
      '/app/update/check?current=' + encodeURIComponent(current),
    ),
  appUpdateDownload: (current: string) =>
    request<{ taskId: string }>('/app/update/download', {
      method: 'POST',
      body: JSON.stringify({ current }),
    }),
  appUpdateDownloadProgress: (taskId: string) =>
    request<{ taskId: string; status: string; read: number; total: number; filePath?: string; error?: string }>(
      '/app/update/download-progress?task=' + encodeURIComponent(taskId),
    ),
  appUpdateOpen: (path: string) =>
    request<{ opened: string }>('/app/open', {
      method: 'POST',
      body: JSON.stringify({ path }),
    }),

  // Update (CDN refresh)
  update: () => capabilities.isAndroid
    ? mobileCore.updateStoryCatalog()
    : request<{ status: string }>('/update', {
      method: 'POST',
      timeoutMs: LONG_MUTATION_TIMEOUT_MS,
    }),
  updateProgress: (): Promise<{
    current: number
    total: number
    message?: string
    done: boolean
    status?: 'idle' | 'running' | 'done' | 'error'
    error?: string
  }> => capabilities.isAndroid
    ? mobileCore.storyUpdateProgress()
    : request<{ current: number; total: number; message?: string; done: boolean }>('/update/progress'),

  // JSON Download
  downloadJson: (data: {
    storyType: string
    sort: string
    index: string
    chapter: number
    source: string
    outputDir: string
  }) =>
    request<{ taskId: string }>('/story/download-json', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  downloadProgress: (taskId: string) =>
    request<{ taskId: string; status: string; read: number; total: number; filePath?: string; error?: string }>(
      '/story/download-progress?task=' + encodeURIComponent(taskId),
    ),

  // 导出原文 txt：译文槽位填日语原文，格式与正式翻译档一致（同名 .txt 落输出目录）
  exportOriginalTxt: (data: {
    storyType: string
    sort: string
    index: string
    chapter: number
    source: string
    outputDir: string
  }) =>
    request<{ filePath: string }>('/story/export-original-txt', {
      method: 'POST',
      body: JSON.stringify(data),
      timeoutMs: LONG_MUTATION_TIMEOUT_MS,
    }),

  // Assets
  characters: () =>
    request<import('../types/dictionary').CharacterInfo[]>('/assets/characters'),
  units: () => request<import('../types/dictionary').UnitInfo[]>('/assets/units'),
  areas: () => request<string[]>('/assets/areas'),
  characterIconUrl: (index: number) => capabilities.isAndroid
    ? `/character-icons/chr_${index}.png`
    : `${BASE_URL}/assets/character-icon/${index}`,
  chrIconCustomStatus: () =>
    request<{ active: boolean; count: number }>('/assets/character-icon-custom'),
  chrIconCustomImport: (dir: string) =>
    request<{ active: boolean; count: number }>('/assets/character-icon-custom', {
      method: 'POST',
      body: JSON.stringify({ dir }),
      timeoutMs: LONG_MUTATION_TIMEOUT_MS,
    }),
  chrIconCustomReset: () =>
    request<{ active: boolean; count: number }>('/assets/character-icon-custom', { method: 'DELETE' }),

  // --- Glossary (term library) ---
  glossarySearch: (q: string, category = '', limit = 50) => capabilities.isAndroid
    ? mobileCore.glossarySearch(q, category, limit)
    : request<import('../types/glossary').GlossaryEntry[]>(
      `/glossary/search?q=${encodeURIComponent(q)}&category=${encodeURIComponent(category)}&limit=${limit}`,
    ),
  glossaryCategories: () => capabilities.isAndroid
    ? mobileCore.glossaryCategories()
    : request<import('../types/glossary').CategoryCount[]>('/glossary/categories'),
  glossaryEntries: (category = '', offset = 0, limit = 200) => capabilities.isAndroid
    ? mobileCore.glossaryEntries(category, offset, limit)
    : request<{ items: import('../types/glossary').GlossaryEntry[]; total: number }>(
      `/glossary/entries?category=${encodeURIComponent(category)}&offset=${offset}&limit=${limit}`,
    ),
  glossaryAddEntry: (entry: Partial<import('../types/glossary').GlossaryEntry>) => capabilities.isAndroid
    ? mobileCore.glossaryAddEntry(entry)
    : request<import('../types/glossary').GlossaryEntry>('/glossary/entries', {
      method: 'POST', body: JSON.stringify(entry),
    }),
  glossaryUpdateEntry: (id: string, entry: Partial<import('../types/glossary').GlossaryEntry>) => capabilities.isAndroid
    ? mobileCore.glossaryUpdateEntry(id, entry)
    : request<import('../types/glossary').GlossaryEntry>(`/glossary/entries/${encodeURIComponent(id)}`, {
      method: 'PUT', body: JSON.stringify(entry),
    }),
  glossaryDeleteEntry: (id: string) => capabilities.isAndroid
    ? mobileCore.glossaryDeleteEntry(id)
    : request<{ status: string }>(`/glossary/entries/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  glossaryImport: (srcPath: string) => capabilities.isAndroid
    ? unsupportedOnAndroid<import('../types/glossary').ImportReport>('Path-based glossary import')
    : request<import('../types/glossary').ImportReport>('/glossary/import', {
      method: 'POST', body: JSON.stringify({ srcPath }),
      timeoutMs: LONG_MUTATION_TIMEOUT_MS,
    }),
  glossaryReload: () => capabilities.isAndroid
    ? unsupportedOnAndroid<{ status: string }>('Desktop glossary reload')
    : request<{ status: string }>('/glossary/reload', { method: 'POST' }),
  glossarySync: (remoteUrl: string) => capabilities.isAndroid
    ? unsupportedOnAndroid<{ status: string; entries: number; appellations: number }>('Legacy glossary URL sync')
    : request<{ status: string; entries: number; appellations: number }>('/glossary/sync', {
      method: 'POST', body: JSON.stringify({ remoteUrl }),
      timeoutMs: LONG_MUTATION_TIMEOUT_MS,
    }),
  // Appellation lookup (人称表)
  glossaryAppellationSpeakers: () => capabilities.isAndroid
    ? mobileCore.glossaryAppellationSpeakers()
    : request<string[]>('/glossary/appellations/speakers'),
  glossaryAppellationTargets: (speaker: string) => capabilities.isAndroid
    ? mobileCore.glossaryAppellationTargets(speaker)
    : request<string[]>(`/glossary/appellations/targets?speaker=${encodeURIComponent(speaker)}`),
  glossaryAppellationLookup: (speaker: string, target: string) => capabilities.isAndroid
    ? mobileCore.glossaryAppellationLookup(speaker, target)
    : request<import('../types/glossary').AppellationResult>(
      `/glossary/appellations?speaker=${encodeURIComponent(speaker)}&target=${encodeURIComponent(target)}`,
    ),
  glossaryAppellationUpsert: (a: import('../types/glossary').Appellation) => capabilities.isAndroid
    ? mobileCore.glossaryAppellationUpsert(a)
    : request<import('../types/glossary').Appellation>('/glossary/appellations', {
      method: 'PUT', body: JSON.stringify(a),
    }),
  // Grammar (语法用例) + export
  glossaryGrammar: (q = '', limit = 0) => capabilities.isAndroid
    ? mobileCore.glossaryGrammar(q, limit)
    : request<import('../types/glossary').GrammarUsage[]>(
      `/glossary/grammar?q=${encodeURIComponent(q)}&limit=${limit}`,
    ),
  glossaryExport: () => capabilities.isAndroid
    ? mobileCore.glossaryExport()
    : request<import('../types/glossary').GlossaryData>('/glossary/export'),

  // --- Team mode (proxied to remote glossary-server via local backend) ---
  teamStatus: () => capabilities.isAndroid
    ? mobileCore.teamStatus()
    : request<import('../types/glossary').TeamStatus>('/team/status'),
  teamLogin: (serverUrl: string, username: string, password: string) => capabilities.isAndroid
    ? mobileCore.teamLogin(serverUrl, username, password)
    : request<{ loggedIn: boolean; user: import('../types/glossary').TeamUser }>('/team/login', {
      method: 'POST', body: JSON.stringify({ serverUrl, username, password }),
    }),
  teamLogout: () => capabilities.isAndroid
    ? mobileCore.teamLogout()
    : request<{ status: string }>('/team/logout', { method: 'POST' }),
  teamConnect: (serverUrl: string) => capabilities.isAndroid
    ? mobileCore.teamConnect(serverUrl)
    : request<{ connected: boolean; readonly: boolean }>('/team/connect', {
      method: 'POST', body: JSON.stringify({ serverUrl }),
    }),
  teamDisconnect: () => capabilities.isAndroid
    ? mobileCore.teamDisconnect()
    : request<{ status: string }>('/team/disconnect', { method: 'POST' }),
  teamSync: (force = false) => capabilities.isAndroid
    ? mobileCore.teamSync(force)
    : request<{ status: string; version: number; changed: boolean; entries?: number }>(
      `/team/sync${force ? '?force=1' : ''}`, { method: 'POST', timeoutMs: LONG_MUTATION_TIMEOUT_MS },
    ),
  teamCreateProposal: (p: {
    kind: string; targetType?: string; targetId?: string; category: string
    payload: unknown; baseVersion?: number
  }) => capabilities.isAndroid
    ? mobileCore.teamCreateProposal(p)
    : request<import('../types/glossary').Proposal>('/team/proposals', {
      method: 'POST', body: JSON.stringify(p),
    }),
  teamMyProposals: () => capabilities.isAndroid
    ? mobileCore.teamMyProposals()
    : request<import('../types/glossary').Proposal[]>('/team/proposals/mine'),
  teamWithdrawProposal: (id: string) => capabilities.isAndroid
    ? mobileCore.teamWithdrawProposal(id)
    : request<{ status: string }>(`/team/proposals/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  teamPendingProposals: (category = '') => capabilities.isAndroid
    ? mobileCore.teamPendingProposals(category)
    : request<import('../types/glossary').Proposal[]>(
      `/team/proposals${category ? `?category=${encodeURIComponent(category)}` : ''}`,
    ),
  teamApproveProposal: (id: string, note = '') => capabilities.isAndroid
    ? mobileCore.teamApproveProposal(id, note)
    : request<{ status: string }>(`/team/proposals/${encodeURIComponent(id)}/approve`, {
      method: 'POST', body: JSON.stringify({ note }),
    }),
  teamRejectProposal: (id: string, note: string) => capabilities.isAndroid
    ? mobileCore.teamRejectProposal(id, note)
    : request<{ status: string }>(`/team/proposals/${encodeURIComponent(id)}/reject`, {
      method: 'POST', body: JSON.stringify({ note }),
    }),
  teamSetReviewer: (userId: string, categories: string[]) => capabilities.isAndroid
    ? mobileCore.teamSetReviewer(userId, categories)
    : request<{ userId: string; categories: string[] }>('/team/admin/reviewers', {
      method: 'POST', body: JSON.stringify({ userId, categories }),
    }),
  teamListUsers: () => capabilities.isAndroid
    ? mobileCore.teamListUsers()
    : request<import('../types/glossary').TeamUser[]>('/team/admin/users'),

  // account self-service
  teamChangePassword: (oldPassword: string, newPassword: string) => capabilities.isAndroid
    ? mobileCore.teamChangePassword(oldPassword, newPassword)
    : request<{ status: string }>('/team/account/password', {
      method: 'POST', body: JSON.stringify({ oldPassword, newPassword }),
    }),
  teamUpdateProfile: (displayName: string, avatarColor?: string) => capabilities.isAndroid
    ? mobileCore.teamUpdateProfile(displayName, avatarColor)
    : request<import('../types/glossary').TeamUser>('/team/account/profile', {
      method: 'POST',
      body: JSON.stringify(avatarColor === undefined ? { displayName } : { displayName, avatarColor }),
    }),
  teamAccountUsers: () => capabilities.isAndroid
    ? mobileCore.teamAccountUsers()
    : request<import('../types/glossary').TeamUser[]>('/team/account/users'),

  // admin user management
  teamCreateUser: (username: string, password: string, role: string, displayName: string) => capabilities.isAndroid
    ? mobileCore.teamCreateUser(username, password, role, displayName)
    : request<import('../types/glossary').TeamUser>('/team/admin/users', {
      method: 'POST', body: JSON.stringify({ username, password, role, displayName }),
    }),
  teamSetUserRole: (id: string, role: string) => capabilities.isAndroid
    ? mobileCore.teamSetUserRole(id, role)
    : request<{ id: string; role: string }>(`/team/admin/users/${encodeURIComponent(id)}/role`, {
      method: 'POST', body: JSON.stringify({ role }),
    }),
  teamSetUserStatus: (id: string, status: string) => capabilities.isAndroid
    ? mobileCore.teamSetUserStatus(id, status)
    : request<{ id: string; status: string }>(`/team/admin/users/${encodeURIComponent(id)}/status`, {
      method: 'POST', body: JSON.stringify({ status }),
    }),
  teamResetUserPassword: (id: string, newPassword: string) => capabilities.isAndroid
    ? mobileCore.teamResetUserPassword(id, newPassword)
    : request<{ status: string }>(`/team/admin/users/${encodeURIComponent(id)}/reset-password`, {
      method: 'POST', body: JSON.stringify({ newPassword }),
    }),
  teamDeleteUser: (id: string) => capabilities.isAndroid
    ? mobileCore.teamDeleteUser(id)
    : request<{ status: string }>(`/team/admin/users/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  // Bulk-upload the entire LOCAL glossary to the server (superadmin only; the
  // server upserts by entry ID and bumps the version once so every client re-syncs).
  // Accepts the full GlossaryData (entries + appellations + grammar) — the server
  // now also upserts appellations/grammar. Appellations/grammar are always sent
  // (as [] when absent) so the older entries-only path keeps working unchanged.
  // 完全替换线上术语库（管理员）：服务器删掉上传里没有的行、其余 upsert，
  // 单事务原子完成；空 entries 服务端直接拒绝（防误清空）。
  teamGlossaryReplace: (data: import('../types/glossary').GlossaryData) => capabilities.isAndroid
    ? mobileCore.teamGlossaryReplace(data)
    : request<{ deleted: number; written: number; entries: number; appellations: number; grammar: number; version: number }>(
      '/team/admin/glossary/replace', {
        method: 'POST',
        timeoutMs: LONG_MUTATION_TIMEOUT_MS,
        body: JSON.stringify({
          entries: data.entries,
          appellations: data.appellations ?? [],
          grammar: data.grammar ?? [],
        }),
      }),
  teamBulkImport: (data: import('../types/glossary').GlossaryData) => capabilities.isAndroid
    ? mobileCore.teamBulkImport(data)
    : request<{ upserted: number; version: number }>('/team/admin/glossary/bulk-import', {
      method: 'POST',
      timeoutMs: LONG_MUTATION_TIMEOUT_MS,
      body: JSON.stringify({
        entries: data.entries,
        appellations: data.appellations ?? [],
        grammar: data.grammar ?? [],
      }),
    }),
}
