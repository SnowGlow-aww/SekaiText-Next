import { convertFileSrc, invoke } from '@tauri-apps/api/core'
import type { SaveMetadata } from '../types/api'
import type {
  Appellation,
  AppellationResult,
  CategoryCount,
  GlossaryData,
  GlossaryEntry,
  GrammarUsage,
  Proposal,
  TeamStatus,
  TeamUser,
} from '../types/glossary'
import type { DstTalk, SourceTalk } from '../types/translation'

interface NativeJsonResponse {
  json: string
}

async function invokeJson<T>(command: string, payload: Record<string, unknown> = {}): Promise<T> {
  const response = await invoke<NativeJsonResponse>(command, payload)
  return JSON.parse(response.json) as T
}

function invokeRequestJson<T>(command: string, request: object = {}): Promise<T> {
  return invokeJson<T>(command, { requestJson: JSON.stringify(request) })
}

export const mobileCore = {
  bootstrap: () => invokeJson<{
    platform: 'android'
    editor: boolean
    story: boolean
    live2d: boolean
    glossary: boolean
    team: boolean
  }>('mobile_core_bootstrap'),

  openUrl: (url: string) =>
    invokeRequestJson<{ status: string }>('mobile_open_url', { url }),

  storyTypes: () => invokeJson<string[]>('mobile_story_types'),

  storySorts: (type: string) =>
    invokeJson<{ label: string; value: string }[]>('mobile_story_sorts', {
      requestJson: JSON.stringify({ type }),
    }),

  storyIndex: (type: string, sort: string) =>
    invokeJson<{ label: string; value: string; chapters?: number[] }[]>('mobile_story_index', {
      requestJson: JSON.stringify({ type, sort }),
    }),

  storyChapters: (type: string, sort: string, index: string) =>
    invokeJson<{ number: number; label: string }[]>('mobile_story_chapters', {
      requestJson: JSON.stringify({ type, sort, index }),
    }),

  storyJsonPath: (type: string, sort: string, index: string, chapter: number, source: string) =>
    invokeJson<{ url: string; fileName: string; saveTitle: string; chapterTitle: string }>(
      'mobile_story_json_path',
      { requestJson: JSON.stringify({ type, sort, index, chapter, source }) },
    ),

  storyLoad: (data: { storyType: string; sort: string; index: string; chapter: number; source: string }) =>
    invokeJson<{
      scenarioId: string
      sourceTalks: SourceTalk[]
      saveTitle: string
      chapterTitle: string
      indexLabel: string
    }>('mobile_story_load', { requestJson: JSON.stringify(data) }),

  storyLoadLocal: (content: string) =>
    invokeJson<{
      scenarioId: string
      sourceTalks: SourceTalk[]
      saveTitle: string
      chapterTitle: string
      indexLabel: string
    }>('mobile_story_load_local', { content }),

  voiceUrl: (scenarioId: string, voiceId: string, source: string, chara2d?: number) =>
    invokeJson<{ url: string }>('mobile_voice_url', {
      requestJson: JSON.stringify({ scenarioId, voiceId, source, chara2d }),
    }),

  resolveLive2DAsset: async (url: string) => {
    const asset = await invokeJson<{ path: string; mime: string; size: number; cacheHit: boolean }>(
      'mobile_resolve_live2d_asset',
      { requestJson: JSON.stringify({ url }) },
    )
    return { ...asset, url: convertFileSrc(asset.path) }
  },

  resolveStoryLabel: (label: string) =>
    invokeJson<{ ok: boolean; storyType: string; index: string; indexLabel: string; chapter: number }>(
      'mobile_resolve_story_label',
      { requestJson: JSON.stringify({ label }) },
    ),

  storyCatalogStatus: () =>
    invokeJson<{ ready: boolean; generation: number; updating: boolean; error?: string }>(
      'mobile_story_catalog_status',
    ),

  updateStoryCatalog: () =>
    invokeJson<{ status: string }>('mobile_update_story_catalog'),

  storyUpdateProgress: () =>
    invokeJson<{
      current: number
      total: number
      message?: string
      done: boolean
      status: 'idle' | 'running' | 'done' | 'error'
      error?: string
    }>('mobile_story_update_progress'),

  createTranslation: (data: { sourceTalks: SourceTalk[]; jp: boolean }) =>
    invokeJson<DstTalk[]>('mobile_create_translation', { requestJson: JSON.stringify(data) }),

  loadTranslation: (content: string) =>
    invokeJson<{ talks: DstTalk[]; meta: SaveMetadata | null }>('mobile_load_translation', { content }),

  serializeTranslation: (data: { talks: DstTalk[]; saveN: boolean; meta?: SaveMetadata }) =>
    invokeJson<{ content: string }>('mobile_serialize_translation', { requestJson: JSON.stringify(data) }),

  checkLines: (data: { sourceTalks: SourceTalk[]; loadedTalks: DstTalk[] }) =>
    invokeJson<DstTalk[]>('mobile_check_lines', { requestJson: JSON.stringify(data) }),

  compareText: (data: { referTalks: DstTalk[]; checkTalks: DstTalk[]; editorMode: number }) =>
    invokeJson<{ talks: DstTalk[]; dstTalks: DstTalk[] }>('mobile_compare_text', {
      requestJson: JSON.stringify(data),
    }),

  changeText: (data: {
    row: number
    text: string
    editorMode: number
    talks: DstTalk[]
    dstTalks: DstTalk[]
    referTalks: DstTalk[]
  }) => invokeJson<{ talks: DstTalk[]; dstTalks: DstTalk[] }>('mobile_change_text', {
    requestJson: JSON.stringify(data),
  }),

  addLine: (data: {
    row: number
    talks: DstTalk[]
    dstTalks: DstTalk[]
    isProofreading: boolean
  }) => invokeJson<{ talks: DstTalk[]; dstTalks: DstTalk[] }>('mobile_add_line', {
    requestJson: JSON.stringify(data),
  }),

  removeLine: (data: { row: number; talks: DstTalk[]; dstTalks: DstTalk[] }) =>
    invokeJson<{ talks: DstTalk[]; dstTalks: DstTalk[] }>('mobile_remove_line', {
      requestJson: JSON.stringify(data),
    }),

  replaceBrackets: (data: { row: number; brackets: string; talks: DstTalk[]; dstTalks: DstTalk[] }) =>
    invokeJson<{ talks: DstTalk[]; dstTalks: DstTalk[] }>('mobile_replace_brackets', {
      requestJson: JSON.stringify(data),
    }),

  speakerCount: (data: { talks: DstTalk[]; sourceTalks: SourceTalk[] }) =>
    invokeJson<{ speakers: { japanese: string; chinese: string; count: number }[] }>('mobile_speaker_count', {
      requestJson: JSON.stringify(data),
    }),

  checkText: (data: { speaker: string; text: string }) =>
    invokeJson<{ text: string; checked: boolean; message?: string }>('mobile_check_text', {
      requestJson: JSON.stringify(data),
    }),

  glossarySearch: (q: string, category = '', limit = 50) =>
    invokeJson<GlossaryEntry[]>('mobile_glossary_search', {
      requestJson: JSON.stringify({ q, category, limit }),
    }),

  glossaryCategories: () =>
    invokeJson<CategoryCount[]>('mobile_glossary_categories'),

  glossaryEntries: (category = '', offset = 0, limit = 200) =>
    invokeJson<{ items: GlossaryEntry[]; total: number }>('mobile_glossary_entries', {
      requestJson: JSON.stringify({ category, offset, limit }),
    }),

  glossaryAddEntry: (entry: Partial<GlossaryEntry>) =>
    invokeJson<GlossaryEntry>('mobile_glossary_add_entry', {
      requestJson: JSON.stringify(entry),
    }),

  glossaryUpdateEntry: (id: string, entry: Partial<GlossaryEntry>) =>
    invokeJson<GlossaryEntry>('mobile_glossary_update_entry', {
      requestJson: JSON.stringify({ id, entry }),
    }),

  glossaryDeleteEntry: (id: string) =>
    invokeJson<{ status: string }>('mobile_glossary_delete_entry', {
      requestJson: JSON.stringify({ id }),
    }),

  glossaryAppellationSpeakers: () =>
    invokeJson<string[]>('mobile_glossary_appellation_speakers'),

  glossaryAppellationTargets: (speaker: string) =>
    invokeJson<string[]>('mobile_glossary_appellation_targets', {
      requestJson: JSON.stringify({ speaker }),
    }),

  glossaryAppellationLookup: (speaker: string, target: string) =>
    invokeJson<AppellationResult>('mobile_glossary_appellation_lookup', {
      requestJson: JSON.stringify({ speaker, target }),
    }),

  glossaryAppellationUpsert: (appellation: Appellation) =>
    invokeJson<Appellation>('mobile_glossary_appellation_upsert', {
      requestJson: JSON.stringify(appellation),
    }),

  glossaryGrammar: (q = '', limit = 0) =>
    invokeJson<GrammarUsage[]>('mobile_glossary_grammar', {
      requestJson: JSON.stringify({ q, limit }),
    }),

  glossaryExport: () =>
    invokeJson<GlossaryData>('mobile_glossary_export'),

  teamStatus: () =>
    invokeRequestJson<TeamStatus>('mobile_team_status'),

  teamLogin: (serverUrl: string, username: string, password: string) =>
    invokeRequestJson<{ loggedIn: boolean; user: TeamUser }>('mobile_team_login', {
      serverUrl,
      username,
      password,
    }),

  teamLogout: () =>
    invokeRequestJson<{ status: string }>('mobile_team_logout'),

  teamConnect: (serverUrl: string) =>
    invokeRequestJson<{ connected: boolean; readonly: boolean }>('mobile_team_connect', { serverUrl }),

  teamDisconnect: () =>
    invokeRequestJson<{ status: string }>('mobile_team_disconnect'),

  teamSync: (force = false) =>
    invokeRequestJson<{
      status: 'up-to-date' | 'synced'
      version: number
      changed: boolean
      entries?: number
      appellations?: number
      grammar?: number
      removed?: number
    }>('mobile_team_sync', { force }),

  teamCreateProposal: (proposal: {
    kind: string
    targetType?: string
    targetId?: string
    category: string
    payload: unknown
    baseVersion?: number
  }) => invokeRequestJson<Proposal>('mobile_team_create_proposal', proposal),

  teamMyProposals: () =>
    invokeRequestJson<Proposal[]>('mobile_team_my_proposals'),

  teamWithdrawProposal: (id: string) =>
    invokeRequestJson<{ status: string }>('mobile_team_withdraw_proposal', { id }),

  teamPendingProposals: (category = '') =>
    invokeRequestJson<Proposal[]>('mobile_team_pending_proposals', { category }),

  teamApproveProposal: (id: string, note = '') =>
    invokeRequestJson<{ status: string }>('mobile_team_approve_proposal', { id, note }),

  teamRejectProposal: (id: string, note: string) =>
    invokeRequestJson<{ status: string }>('mobile_team_reject_proposal', { id, note }),

  teamSetReviewer: (userId: string, categories: string[]) =>
    invokeRequestJson<{ userId: string; categories: string[] }>('mobile_team_set_reviewer', {
      userId,
      categories,
    }),

  teamListUsers: () =>
    invokeRequestJson<TeamUser[]>('mobile_team_list_users'),

  teamChangePassword: (oldPassword: string, newPassword: string) =>
    invokeRequestJson<{ status: string }>('mobile_team_change_password', { oldPassword, newPassword }),

  teamUpdateProfile: (displayName: string, avatarColor?: string) =>
    invokeRequestJson<TeamUser>(
      'mobile_team_update_profile',
      avatarColor === undefined ? { displayName } : { displayName, avatarColor },
    ),

  teamAccountUsers: () =>
    invokeRequestJson<TeamUser[]>('mobile_team_account_users'),

  teamCreateUser: (username: string, password: string, role: string, displayName: string) =>
    invokeRequestJson<TeamUser>('mobile_team_create_user', { username, password, role, displayName }),

  teamSetUserRole: (id: string, role: string) =>
    invokeRequestJson<{ id: string; role: string }>('mobile_team_set_user_role', { id, role }),

  teamSetUserStatus: (id: string, status: string) =>
    invokeRequestJson<{ id: string; status: string }>('mobile_team_set_user_status', { id, status }),

  teamResetUserPassword: (id: string, newPassword: string) =>
    invokeRequestJson<{ status: string }>('mobile_team_reset_user_password', { id, newPassword }),

  teamDeleteUser: (id: string) =>
    invokeRequestJson<{ status: string }>('mobile_team_delete_user', { id }),

  teamGlossaryReplace: (data: GlossaryData) =>
    invokeRequestJson<{
      deleted: number
      written: number
      entries: number
      appellations: number
      grammar: number
      version: number
    }>('mobile_team_glossary_replace', {
      entries: data.entries,
      appellations: data.appellations ?? [],
      grammar: data.grammar ?? [],
    }),

  teamBulkImport: (data: GlossaryData) =>
    invokeRequestJson<{ upserted: number; version: number }>('mobile_team_bulk_import', {
      entries: data.entries,
      appellations: data.appellations ?? [],
      grammar: data.grammar ?? [],
    }),
}
