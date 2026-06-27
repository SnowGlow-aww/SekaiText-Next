export const BASE_URL = 'http://localhost:9800/api/v1'

class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const method = (options.method || 'GET').toUpperCase()
  const url = `${BASE_URL}${path}`
  const start = Date.now()

  try {
    const res = await fetch(url, {
      headers: {
        'Content-Type': 'application/json',
        ...options.headers,
      },
      ...options,
    })

    const elapsed = Date.now() - start

    if (!res.ok) {
      let body: any = null
      try { body = await res.json() } catch { body = await res.text().catch(() => null) }
      const errMsg = body?.error || res.statusText
      const err = new ApiError(res.status, `${method} ${path} → ${res.status}: ${errMsg}`)
      console.error(`[API] ${method} ${path} → ${res.status} (${elapsed}ms)`, { error: errMsg, body })
      throw err
    }

    const data = await res.json()
    console.log(`[API] ${method} ${path} → ${res.status} (${elapsed}ms)`)
    return data
  } catch (e) {
    const elapsed = Date.now() - start
    if (e instanceof ApiError) throw e
    const wrap = new Error(`${method} ${path} → 网络请求失败: ${(e as Error).message}`)
    console.error(`[API] ${method} ${path} → NETWORK ERROR (${elapsed}ms)`, (e as Error).message)
    throw wrap
  }
}


export const api = {
  // Story navigation
  storyTypes: () => request<string[]>('/story/types'),

  storySorts: (type: string) =>
    request<{ label: string; value: string }[]>(`/story/sorts?type=${encodeURIComponent(type)}`),

  storyIndex: (type: string, sort: string) =>
    request<{ label: string; value: string; chapters?: number[] }[]>(
      `/story/index?type=${encodeURIComponent(type)}&sort=${encodeURIComponent(sort)}`,
    ),

  storyChapter: (type: string, sort: string, index: string) =>
    request<{ number: number; label: string }[]>(
      `/story/chapter?type=${encodeURIComponent(type)}&sort=${encodeURIComponent(sort)}&index=${encodeURIComponent(index)}`,
    ),

  jsonPath: (type: string, sort: string, index: string, chapter: number, source: string) =>
    request<{ url: string; fileName: string; saveTitle: string; chapterTitle: string }>(
      `/story/json-path?type=${encodeURIComponent(type)}&sort=${encodeURIComponent(sort)}&index=${encodeURIComponent(index)}&chapter=${chapter}&source=${encodeURIComponent(source)}`,
    ),

  storyLoad: (data: {
    storyType: string
    sort: string
    index: string
    chapter: number
    source: string
  }) =>
    request<{ scenarioId: string; sourceTalks: import('../types/translation').SourceTalk[]; saveTitle: string; chapterTitle: string }>(
      '/story/load',
      { method: 'POST', body: JSON.stringify(data) },
    ),

  storyLoadLocal: (content: string) =>
    request<{ scenarioId: string; sourceTalks: import('../types/translation').SourceTalk[]; saveTitle: string; chapterTitle: string }>(
      '/story/load-local',
      { method: 'POST', body: JSON.stringify({ content }) },
    ),

  resolveLabel: (label: string) =>
    request<{ ok: boolean; storyType: string; index: string; chapter: number }>(
      '/story/resolve-label',
      { method: 'POST', body: JSON.stringify({ label }) },
    ),

  // Translation
  translationLoadContent: (content: string) =>
    request<{
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
  }) =>
    request<{ content: string }>('/translation/serialize', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  translationCreate: (data: {
    sourceTalks: import('../types/translation').SourceTalk[]
    jp: boolean
  }) => request<import('../types/translation').DstTalk[]>('/translation/create', {
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

  translationSave: (filePath: string, talks: import('../types/translation').DstTalk[], saveN: boolean, meta?: import('../types/api').SaveMetadata) =>
    request<{ status: string }>('/translation/save', {
      method: 'POST',
      body: JSON.stringify({ filePath, talks, saveN, meta }),
    }),

  ensureDir: (path: string) =>
    request<{ dir: string }>('/translation/ensure-dir', {
      method: 'POST',
      body: JSON.stringify({ path }),
    }),

  checkLines: (data: {
    sourceTalks: import('../types/translation').SourceTalk[]
    loadedTalks: import('../types/translation').DstTalk[]
  }) => request<import('../types/translation').DstTalk[]>('/translation/check-lines', {
    method: 'POST',
    body: JSON.stringify(data),
  }),

  compareText: (data: {
    referTalks: import('../types/translation').DstTalk[]
    checkTalks: import('../types/translation').DstTalk[]
    editorMode: number
  }) => request<{ talks: import('../types/translation').DstTalk[]; dstTalks: import('../types/translation').DstTalk[] }>('/editor/compare', {
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
  }) =>
    request<{ talks: import('../types/translation').DstTalk[]; dstTalks: import('../types/translation').DstTalk[] }>(
      '/editor/change-text',
      { method: 'POST', body: JSON.stringify(data) },
    ),

  addLine: (data: {
    row: number
    talks: import('../types/translation').DstTalk[]
    dstTalks: import('../types/translation').DstTalk[]
    isProofreading: boolean
  }) =>
    request<{ talks: import('../types/translation').DstTalk[]; dstTalks: import('../types/translation').DstTalk[] }>(
      '/editor/add-line',
      { method: 'POST', body: JSON.stringify(data) },
    ),

  removeLine: (data: {
    row: number
    talks: import('../types/translation').DstTalk[]
    dstTalks: import('../types/translation').DstTalk[]
  }) =>
    request<{ talks: import('../types/translation').DstTalk[]; dstTalks: import('../types/translation').DstTalk[] }>(
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
  }) => request<{ talks: import('../types/translation').DstTalk[]; dstTalks: import('../types/translation').DstTalk[] }>('/editor/replace-brackets', {
    method: 'POST',
    body: JSON.stringify(data),
  }),

  // Text check
  checkText: (data: { speaker: string; text: string }) =>
    request<{ text: string; checked: boolean; message?: string }>('/check/text', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  // Flashback
  flashbackAnalyze: (sourceTalks: import('../types/translation').SourceTalk[]) =>
    request<{ sourceTalks: import('../types/translation').SourceTalk[] }>(
      '/flashback/analyze',
      { method: 'POST', body: JSON.stringify({ sourceTalks }) },
    ),

  clueHints: (clue: string, lang = 'zh-cn') =>
    request<{ clue: string; hints: string[] }>(`/flashback/clue-hints?clue=${encodeURIComponent(clue)}&lang=${encodeURIComponent(lang)}`),

  voiceClues: () => request<Record<string, { id: number; title: string; name: string; chapters: { title: string }[]; cards: number[]; inferredVoiceIDs?: Record<string, unknown> }>>('/flashback/voice-clues'),

  // Voice
  voiceUrl: (scenarioId: string, voiceId: string, source: string, chara2d?: number) =>
    request<{ url: string }>(
      `/voice/url?scenarioId=${encodeURIComponent(scenarioId)}&voiceId=${encodeURIComponent(voiceId)}&source=${encodeURIComponent(source)}` +
      (chara2d != null ? `&chara2d=${chara2d}` : ''),
    ),

  // Speaker
  speakerCount: (data: {
    talks: import('../types/translation').DstTalk[]
    sourceTalks: import('../types/translation').SourceTalk[]
  }) =>
    request<{ speakers: { japanese: string; chinese: string; count: number }[] }>('/speaker/count', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  // Recovery (autosave)
  recoverySave: (data: {
    talks: import('../types/translation').DstTalk[]
    saveN: boolean
    filePath: string
    editorMode: number
    storyType?: string
    storySort?: string
    storyIndex?: string
    storyChapter?: number
    storySource?: string
  }) =>
    request<{ status: string }>('/recovery/save', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  recoveryLoad: () =>
    request<{
      exists: boolean
      content?: string
      filePath?: string
      editorMode?: number
      savedAt?: string
      storyType?: string
      storySort?: string
      storyIndex?: string
      storyChapter?: number
      storySource?: string
    }>('/recovery/load'),

  recoveryClear: () =>
    request<{ status: string }>('/recovery/clear', { method: 'DELETE' }),

  // Settings
  getSettings: () => request<import('../types/api').Settings>('/settings'),
  putSettings: (settings: import('../types/api').Settings) =>
    request<import('../types/api').Settings>('/settings', {
      method: 'PUT',
      body: JSON.stringify(settings),
    }),

  openDataDir: () =>
    request<{ dir: string }>('/open-data-dir', { method: 'POST' }),

  importLive2D: (srcDir: string) =>
    request<{ dir: string; moved: number }>('/live2d/import', {
      method: 'POST',
      body: JSON.stringify({ srcDir }),
    }),

  // Plugins (management). The listing/entry-serving is handled directly by the
  // plugin-host loader against /plugins/*; these cover enable/disable + uninstall.
  pluginSetEnabled: (id: string, enabled: boolean) =>
    request<{ ok: boolean }>(`/plugins/${id}/enabled`, {
      method: 'POST',
      body: JSON.stringify({ enabled }),
    }),
  pluginUninstall: (id: string) =>
    request<{ ok: boolean }>(`/plugins/${id}`, { method: 'DELETE' }),
  // Install a .sekplugin package from a local file path (Tauri dialog → path,
  // or marketplace download → temp path). hostVersion gates minHostVersion.
  pluginInstall: (srcPath: string, hostVersion: string) =>
    // Backend returns a PluginManifest (no runtime `enabled` field); callers
    // re-fetch the list for enable-state. Omit `enabled` so it can't be misread.
    request<Omit<import('../plugin-host/autoload').InstalledPlugin, 'enabled'>>('/plugins/install', {
      method: 'POST',
      body: JSON.stringify({ srcPath, hostVersion }),
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
    }),
  // Reinstall every installed plugin that has a newer market version. Silent
  // auto-update on boot; the summary drives a "已更新 N 个插件" toast.
  marketAutoUpdate: (hostVersion: string) =>
    request<import('../stores/appUpdate').AutoUpdateSummary>('/market/auto-update', {
      method: 'POST',
      body: JSON.stringify({ hostVersion }),
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
  update: () => request<{ status: string }>('/update', { method: 'POST' }),
  updateProgress: () =>
    request<{ current: number; total: number; message?: string; done: boolean }>('/update/progress'),

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

  // Assets
  characters: () =>
    request<import('../types/dictionary').CharacterInfo[]>('/assets/characters'),
  units: () => request<import('../types/dictionary').UnitInfo[]>('/assets/units'),
  areas: () => request<string[]>('/assets/areas'),
  characterIconUrl: (index: number) => `${BASE_URL}/assets/character-icon/${index}`,

  // --- Glossary (term library) ---
  glossarySearch: (q: string, category = '', limit = 50) =>
    request<import('../types/glossary').GlossaryEntry[]>(
      `/glossary/search?q=${encodeURIComponent(q)}&category=${encodeURIComponent(category)}&limit=${limit}`,
    ),
  glossaryCategories: () =>
    request<import('../types/glossary').CategoryCount[]>('/glossary/categories'),
  glossaryEntries: (category = '', offset = 0, limit = 200) =>
    request<{ items: import('../types/glossary').GlossaryEntry[]; total: number }>(
      `/glossary/entries?category=${encodeURIComponent(category)}&offset=${offset}&limit=${limit}`,
    ),
  glossaryAddEntry: (entry: Partial<import('../types/glossary').GlossaryEntry>) =>
    request<import('../types/glossary').GlossaryEntry>('/glossary/entries', {
      method: 'POST', body: JSON.stringify(entry),
    }),
  glossaryUpdateEntry: (id: string, entry: Partial<import('../types/glossary').GlossaryEntry>) =>
    request<import('../types/glossary').GlossaryEntry>(`/glossary/entries/${encodeURIComponent(id)}`, {
      method: 'PUT', body: JSON.stringify(entry),
    }),
  glossaryDeleteEntry: (id: string) =>
    request<{ status: string }>(`/glossary/entries/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  glossaryImport: (srcPath: string) =>
    request<import('../types/glossary').ImportReport>('/glossary/import', {
      method: 'POST', body: JSON.stringify({ srcPath }),
    }),
  glossaryReload: () => request<{ status: string }>('/glossary/reload', { method: 'POST' }),
  glossarySync: (remoteUrl: string) =>
    request<{ status: string; entries: number; appellations: number }>('/glossary/sync', {
      method: 'POST', body: JSON.stringify({ remoteUrl }),
    }),
  // Appellation lookup (人称表)
  glossaryAppellationSpeakers: () =>
    request<string[]>('/glossary/appellations/speakers'),
  glossaryAppellationTargets: (speaker: string) =>
    request<string[]>(`/glossary/appellations/targets?speaker=${encodeURIComponent(speaker)}`),
  glossaryAppellationLookup: (speaker: string, target: string) =>
    request<import('../types/glossary').AppellationResult>(
      `/glossary/appellations?speaker=${encodeURIComponent(speaker)}&target=${encodeURIComponent(target)}`,
    ),
  glossaryAppellationUpsert: (a: import('../types/glossary').Appellation) =>
    request<import('../types/glossary').Appellation>('/glossary/appellations', {
      method: 'PUT', body: JSON.stringify(a),
    }),
  // Grammar (语法用例) + export
  glossaryGrammar: (q = '', limit = 0) =>
    request<import('../types/glossary').GrammarUsage[]>(
      `/glossary/grammar?q=${encodeURIComponent(q)}&limit=${limit}`,
    ),
  glossaryExport: () =>
    request<import('../types/glossary').GlossaryData>('/glossary/export'),

  // --- Team mode (proxied to remote glossary-server via local backend) ---
  teamStatus: () => request<import('../types/glossary').TeamStatus>('/team/status'),
  teamLogin: (serverUrl: string, username: string, password: string) =>
    request<{ loggedIn: boolean; user: import('../types/glossary').TeamUser }>('/team/login', {
      method: 'POST', body: JSON.stringify({ serverUrl, username, password }),
    }),
  teamLogout: () => request<{ status: string }>('/team/logout', { method: 'POST' }),
  teamConnect: (serverUrl: string) =>
    request<{ connected: boolean; readonly: boolean }>('/team/connect', {
      method: 'POST', body: JSON.stringify({ serverUrl }),
    }),
  teamDisconnect: () => request<{ status: string }>('/team/disconnect', { method: 'POST' }),
  teamSync: (force = false) =>
    request<{ status: string; version: number; changed: boolean; entries?: number }>(
      `/team/sync${force ? '?force=1' : ''}`, { method: 'POST' },
    ),
  teamCreateProposal: (p: {
    kind: string; targetType?: string; targetId?: string; category: string
    payload: unknown; baseVersion?: number
  }) => request<import('../types/glossary').Proposal>('/team/proposals', {
    method: 'POST', body: JSON.stringify(p),
  }),
  teamMyProposals: () =>
    request<import('../types/glossary').Proposal[]>('/team/proposals/mine'),
  teamWithdrawProposal: (id: string) =>
    request<{ status: string }>(`/team/proposals/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  teamPendingProposals: (category = '') =>
    request<import('../types/glossary').Proposal[]>(
      `/team/proposals${category ? `?category=${encodeURIComponent(category)}` : ''}`,
    ),
  teamApproveProposal: (id: string, note = '') =>
    request<{ status: string }>(`/team/proposals/${encodeURIComponent(id)}/approve`, {
      method: 'POST', body: JSON.stringify({ note }),
    }),
  teamRejectProposal: (id: string, note: string) =>
    request<{ status: string }>(`/team/proposals/${encodeURIComponent(id)}/reject`, {
      method: 'POST', body: JSON.stringify({ note }),
    }),
  teamSetReviewer: (userId: string, categories: string[]) =>
    request<{ userId: string; categories: string[] }>('/team/admin/reviewers', {
      method: 'POST', body: JSON.stringify({ userId, categories }),
    }),
  teamListUsers: () =>
    request<import('../types/glossary').TeamUser[]>('/team/admin/users'),

  // account self-service
  teamChangePassword: (oldPassword: string, newPassword: string) =>
    request<{ status: string }>('/team/account/password', {
      method: 'POST', body: JSON.stringify({ oldPassword, newPassword }),
    }),
  teamUpdateProfile: (displayName: string, avatarColor?: string) =>
    request<import('../types/glossary').TeamUser>('/team/account/profile', {
      method: 'POST',
      body: JSON.stringify(avatarColor === undefined ? { displayName } : { displayName, avatarColor }),
    }),
  teamAccountUsers: () =>
    request<import('../types/glossary').TeamUser[]>('/team/account/users'),

  // admin user management
  teamCreateUser: (username: string, password: string, role: string, displayName: string) =>
    request<import('../types/glossary').TeamUser>('/team/admin/users', {
      method: 'POST', body: JSON.stringify({ username, password, role, displayName }),
    }),
  teamSetUserRole: (id: string, role: string) =>
    request<{ id: string; role: string }>(`/team/admin/users/${encodeURIComponent(id)}/role`, {
      method: 'POST', body: JSON.stringify({ role }),
    }),
  teamSetUserStatus: (id: string, status: string) =>
    request<{ id: string; status: string }>(`/team/admin/users/${encodeURIComponent(id)}/status`, {
      method: 'POST', body: JSON.stringify({ status }),
    }),
  teamResetUserPassword: (id: string, newPassword: string) =>
    request<{ status: string }>(`/team/admin/users/${encodeURIComponent(id)}/reset-password`, {
      method: 'POST', body: JSON.stringify({ newPassword }),
    }),
  teamDeleteUser: (id: string) =>
    request<{ status: string }>(`/team/admin/users/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  // Bulk-upload the entire LOCAL glossary to the server (superadmin only; the
  // server upserts by entry ID and bumps the version once so every client re-syncs).
  teamBulkImport: (entries: import('../types/glossary').GlossaryEntry[]) =>
    request<{ upserted: number; version: number }>('/team/admin/glossary/bulk-import', {
      method: 'POST', body: JSON.stringify({ entries }),
    }),
}
