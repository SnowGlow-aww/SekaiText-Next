import type { DocMeta, EditorModeState } from '../stores/editor'
import type { DstTalk, EditorMode, SourceTalk } from '../types/translation'
import {
  hasRecovery,
  recoveryModes,
  type RecoveryLoadResult,
  type RecoveryStoredMode,
} from './recovery'
import { parseDocumentFileName, titlePartForParsedFile } from './documentFileName'

interface RecoveryStoryRequest {
  storyType: string
  sort: string
  index: string
  chapter: number
  source: string
}

export interface RecoveryStoryResult {
  scenarioId: string
  sourceTalks: SourceTalk[]
  saveTitle: string
  chapterTitle: string
  indexLabel: string
}

export interface RecoveryNavigatorCandidate {
  sorts: { label: string; value: string }[]
  indices: { label: string; value: string; chapters?: number[] }[]
  chapters: { number: number; label: string }[]
  meta: DocMeta
}

export interface RecoveryDocumentCandidate {
  states: EditorModeState[]
  activeMode: EditorMode
  activeStory: RecoveryStoryResult
  navigator: RecoveryNavigatorCandidate
}

export interface RecoveryCandidateDependencies {
  loadTranslationContent: (content: string) => Promise<{ talks: DstTalk[] }>
  loadStory: (request: RecoveryStoryRequest) => Promise<RecoveryStoryResult>
  checkLines: (data: { sourceTalks: SourceTalk[]; loadedTalks: DstTalk[] }) => Promise<DstTalk[]>
  compareText: (data: {
    referTalks: DstTalk[]
    checkTalks: DstTalk[]
    editorMode: number
  }) => Promise<{ talks: DstTalk[]; dstTalks: DstTalk[] }>
  loadSorts: (type: string) => Promise<{ label: string; value: string }[]>
  loadIndices: (type: string, sort: string) => Promise<{ label: string; value: string; chapters?: number[] }[]>
  loadChapters: (type: string, sort: string, index: string) => Promise<{ number: number; label: string }[]>
  isCurrent: () => boolean
}

export type RecoveryCandidateErrorCode =
  | 'stale'
  | 'missing-recovery'
  | 'invalid-mode'
  | 'zero-talks'
  | 'invalid-metadata'
  | 'source-load-failed'
  | 'source-mismatch'
  | 'alignment-failed'
  | 'comparison-failed'
  | 'navigator-failed'

export class RecoveryCandidateError extends Error {
  readonly code: RecoveryCandidateErrorCode

  constructor(code: RecoveryCandidateErrorCode, message: string, options?: ErrorOptions) {
    super(message, options)
    this.name = 'RecoveryCandidateError'
    this.code = code
  }
}

const supportedStoryTypes = new Set([
  '活动剧情',
  '主线剧情',
  '活动卡面',
  '特殊卡面',
  '初始卡面',
  '升级卡面',
  '初始地图对话',
  '升级地图对话',
  '追加地图对话',
  '特殊剧情',
])

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

function assertCurrent(deps: RecoveryCandidateDependencies): void {
  if (!deps.isCurrent()) throw new RecoveryCandidateError('stale', 'recovery operation is no longer current')
}

function validRequest(request: RecoveryStoryRequest): boolean {
  return supportedStoryTypes.has(request.storyType)
    && request.index.trim() !== ''
    && Number.isInteger(request.chapter)
    && request.chapter >= 0
    && request.source.trim() !== ''
}

function requestForMode(mode: RecoveryStoredMode): RecoveryStoryRequest {
  const meta = mode.docMeta
  const request = meta
    ? {
      storyType: meta.type,
      sort: meta.sort || '',
      index: meta.index,
      chapter: meta.chapter,
      source: meta.source,
    }
    : {
      storyType: mode.storyType || '',
      sort: mode.storySort || '',
      index: mode.storyIndex || '',
      chapter: mode.storyChapter ?? -1,
      source: mode.storySource || 'haruki',
    }
  if (!validRequest(request)) {
    throw new RecoveryCandidateError('invalid-metadata', `恢复槽位 ${mode.editorMode} 缺少可用剧情坐标`)
  }
  return request
}

function usableStory(story: RecoveryStoryResult): boolean {
  return typeof story.scenarioId === 'string'
    && story.scenarioId.trim() !== ''
    && typeof story.saveTitle === 'string'
    && story.saveTitle.trim() !== ''
    && Array.isArray(story.sourceTalks)
    && story.sourceTalks.length > 0
}

function makeDocMeta(request: RecoveryStoryRequest, story: RecoveryStoryResult): DocMeta {
  return {
    saveTitle: story.saveTitle.trim(),
    chapterTitle: story.chapterTitle.trim(),
    type: request.storyType,
    sort: request.sort,
    index: request.index,
    indexLabel: story.indexLabel.trim() || request.index,
    chapter: request.chapter,
    source: request.source,
    scenarioId: story.scenarioId.trim(),
  }
}

function verifyStoredMeta(mode: RecoveryStoredMode, story: RecoveryStoryResult): void {
  const meta = mode.docMeta
  if (!meta) return
  if (meta.scenarioId.trim() === ''
    || meta.scenarioId.trim() !== story.scenarioId.trim()
    || meta.saveTitle.trim() !== story.saveTitle.trim()
    || meta.chapterTitle.trim() !== story.chapterTitle.trim()) {
    throw new RecoveryCandidateError('source-mismatch', `恢复槽位 ${mode.editorMode} 的剧情身份与原文不一致`)
  }
}

function seedBaseline(talks: DstTalk[]): DstTalk[] {
  const seeded = clone(talks)
  for (const talk of seeded) {
    if (talk.baseline === undefined || talk.baseline === '') talk.baseline = talk.text
  }
  return seeded
}

async function prepareMode(
  mode: RecoveryStoredMode,
  deps: RecoveryCandidateDependencies,
): Promise<{ state: EditorModeState; story: RecoveryStoryResult }> {
  if (mode.editorMode !== 0 && mode.editorMode !== 1 && mode.editorMode !== 2) {
    throw new RecoveryCandidateError('invalid-mode', `恢复槽位模式无效: ${mode.editorMode}`)
  }

  const hasLosslessState = mode.talks !== undefined
  let editorTalks: DstTalk[]
  if (mode.talks !== undefined) {
    editorTalks = clone(mode.talks)
  } else {
    let parsed: { talks: DstTalk[] }
    try {
      parsed = await deps.loadTranslationContent(mode.content || '')
    } catch (error) {
      assertCurrent(deps)
      throw new RecoveryCandidateError('zero-talks', `恢复槽位 ${mode.editorMode} 无法解析`, { cause: error })
    }
    assertCurrent(deps)
    editorTalks = clone(parsed.talks || [])
  }
  if (editorTalks.length === 0) {
    throw new RecoveryCandidateError('zero-talks', `恢复槽位 ${mode.editorMode} 没有译文`)
  }

  const request = requestForMode(mode)
  let loadedStory: RecoveryStoryResult
  try {
    loadedStory = clone(await deps.loadStory(request))
  } catch (error) {
    assertCurrent(deps)
    throw new RecoveryCandidateError('source-load-failed', `恢复槽位 ${mode.editorMode} 的原文加载失败`, { cause: error })
  }
  assertCurrent(deps)
  if (!usableStory(loadedStory)) {
    throw new RecoveryCandidateError('source-load-failed', `恢复槽位 ${mode.editorMode} 的原文或 scenarioId 为空`)
  }
  verifyStoredMeta(mode, loadedStory)

  const storedDstTalks = clone(mode.dstTalks ?? editorTalks)
  if (storedDstTalks.length === 0) {
    throw new RecoveryCandidateError('zero-talks', `恢复槽位 ${mode.editorMode} 没有可保存译文`)
  }

  let alignedDstTalks: DstTalk[]
  try {
    alignedDstTalks = clone(await deps.checkLines({
      sourceTalks: clone(loadedStory.sourceTalks),
      loadedTalks: storedDstTalks,
    }))
  } catch (error) {
    assertCurrent(deps)
    throw new RecoveryCandidateError('alignment-failed', `恢复槽位 ${mode.editorMode} 行对齐失败`, { cause: error })
  }
  assertCurrent(deps)
  if (alignedDstTalks.length === 0) {
    throw new RecoveryCandidateError('alignment-failed', `恢复槽位 ${mode.editorMode} 行对齐结果为空`)
  }

  let talks: DstTalk[]
  let dstTalks: DstTalk[]
  let referTalks: DstTalk[]
  if (mode.editorMode >= 1) {
    const comparisonRefer = mode.referTalks?.length ? clone(mode.referTalks) : seedBaseline(alignedDstTalks)
    const comparisonCheck = hasLosslessState ? clone(editorTalks) : seedBaseline(alignedDstTalks)
    let compared: { talks: DstTalk[]; dstTalks: DstTalk[] }
    try {
      compared = await deps.compareText({
        referTalks: clone(comparisonRefer),
        checkTalks: clone(comparisonCheck),
        editorMode: mode.editorMode,
      })
    } catch (error) {
      assertCurrent(deps)
      throw new RecoveryCandidateError('comparison-failed', `恢复槽位 ${mode.editorMode} 对比重建失败`, { cause: error })
    }
    assertCurrent(deps)
    if (!Array.isArray(compared.talks) || compared.talks.length === 0
      || !Array.isArray(compared.dstTalks) || compared.dstTalks.length === 0) {
      throw new RecoveryCandidateError('comparison-failed', `恢复槽位 ${mode.editorMode} 对比结果为空`)
    }
    if (hasLosslessState) {
      talks = clone(editorTalks)
      dstTalks = clone(alignedDstTalks)
      referTalks = clone(comparisonRefer)
    } else {
      talks = clone(compared.talks)
      dstTalks = clone(compared.dstTalks)
      referTalks = clone(comparisonRefer)
    }
  } else {
    talks = hasLosslessState ? clone(editorTalks) : clone(alignedDstTalks)
    dstTalks = clone(alignedDstTalks)
    referTalks = []
  }

  const docMeta = makeDocMeta(request, loadedStory)
  const parsedName = parseDocumentFileName(mode.filePath || '')
  const titleOverride = (mode.titleOverride || '').trim()
    || titlePartForParsedFile(parsedName, loadedStory)

  return {
    state: {
      mode: mode.editorMode,
      talks,
      dstTalks,
      referTalks,
      sourceTalks: clone(loadedStory.sourceTalks),
      currentFilePath: mode.filePath || '',
      titleOverride,
      hasUnsavedChanges: mode.hasUnsavedChanges ?? true,
      recoveryPending: true,
      majorClue: null,
      docMeta,
      mutationSeq: 0,
    },
    story: loadedStory,
  }
}

async function prepareNavigator(
  meta: DocMeta,
  deps: RecoveryCandidateDependencies,
): Promise<RecoveryNavigatorCandidate> {
  try {
    const sorts = clone(await deps.loadSorts(meta.type))
    assertCurrent(deps)
    const indices = clone(await deps.loadIndices(meta.type, meta.sort))
    assertCurrent(deps)
    if (!indices.some(option => option.value === meta.index)) {
      throw new RecoveryCandidateError('navigator-failed', '恢复剧情索引已不在当前目录中')
    }
    const chapters = clone(await deps.loadChapters(meta.type, meta.sort, meta.index))
    assertCurrent(deps)
    if (!chapters.some(option => option.number === meta.chapter)) {
      throw new RecoveryCandidateError('navigator-failed', '恢复剧情章节已不在当前目录中')
    }
    return { sorts, indices, chapters, meta: { ...meta } }
  } catch (error) {
    if (error instanceof RecoveryCandidateError) throw error
    assertCurrent(deps)
    throw new RecoveryCandidateError('navigator-failed', '恢复剧情导航数据加载失败', { cause: error })
  }
}

export async function prepareRecoveryDocumentCandidate(
  result: RecoveryLoadResult,
  deps: RecoveryCandidateDependencies,
): Promise<RecoveryDocumentCandidate> {
  if (!hasRecovery(result)) {
    throw new RecoveryCandidateError('missing-recovery', '恢复内容已不存在')
  }
  assertCurrent(deps)

  const modes = recoveryModes(result)
  if (modes.length === 0) {
    throw new RecoveryCandidateError('missing-recovery', '恢复内容没有可用槽位')
  }
  const seenModes = new Set<EditorMode>()
  const prepared: Array<{ state: EditorModeState; story: RecoveryStoryResult }> = []
  for (const mode of modes) {
    if (seenModes.has(mode.editorMode)) {
      throw new RecoveryCandidateError('invalid-mode', `恢复内容包含重复模式 ${mode.editorMode}`)
    }
    seenModes.add(mode.editorMode)
    prepared.push(await prepareMode(mode, deps))
    assertCurrent(deps)
  }

  const requestedActiveMode = result.activeMode ?? prepared[0].state.mode
  const active = prepared.find(item => item.state.mode === requestedActiveMode) ?? prepared[0]
  const navigator = await prepareNavigator(active.state.docMeta!, deps)
  assertCurrent(deps)

  return {
    states: prepared.map(item => item.state),
    activeMode: active.state.mode,
    activeStory: clone(active.story),
    navigator,
  }
}
