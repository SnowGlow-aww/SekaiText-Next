import type { SaveMetadata } from '../types/api'
import type { DocMeta } from '../stores/editor'
import type { DstTalk, EditorMode, SourceTalk } from '../types/translation'
import {
  canonicalStoryIdentity,
  parseDocumentFileName,
  titlePartForParsedFile,
  type ParsedDocumentFileName,
} from './documentFileName'

export { canonicalStoryIdentity } from './documentFileName'

export interface OpenTranslationResult {
  talks: DstTalk[]
  meta: SaveMetadata | null
  filePath?: string
  fileName?: string
}

export interface OpenStoryResult {
  scenarioId: string
  sourceTalks: SourceTalk[]
  saveTitle: string
  chapterTitle: string
  indexLabel: string
}

export interface OpenStoryRequest {
  storyType: string
  sort: string
  index: string
  chapter: number
  source: string
}

export interface CurrentOpenStory {
  story: OpenStoryResult
  docMeta: DocMeta | null
}

export interface OpenDocumentCandidate {
  talks: DstTalk[]
  dstTalks: DstTalk[]
  referTalks: DstTalk[]
  sourceTalks: SourceTalk[]
  story: OpenStoryResult
  docMeta: DocMeta
  currentFilePath: string
  titleOverride: string
  fileMode: EditorMode
  deriving: boolean
}

export interface OpenDocumentDependencies {
  resolveLabel: (label: string) => Promise<{
    ok: boolean
    storyType: string
    index: string
    indexLabel: string
    chapter: number
    matchKind?: 'exact' | 'legacy'
    reason?: 'not-found' | 'exact-ambiguous' | 'legacy-ambiguous'
  }>
  loadStory: (request: OpenStoryRequest) => Promise<OpenStoryResult>
  checkLines: (data: { sourceTalks: SourceTalk[]; loadedTalks: DstTalk[] }) => Promise<DstTalk[]>
  compareText: (data: {
    referTalks: DstTalk[]
    checkTalks: DstTalk[]
    editorMode: number
  }) => Promise<{ talks: DstTalk[]; dstTalks: DstTalk[] }>
  isCurrent: () => boolean
}

export type OpenDocumentErrorCode =
  | 'stale'
  | 'zero-talks'
  | 'destination-only'
  | 'invalid-metadata'
  | 'resolve-failed'
  | 'source-load-failed'
  | 'source-mismatch'
  | 'comparison-failed'
  | 'commit-preparation-failed'

export class OpenDocumentError extends Error {
  readonly code: OpenDocumentErrorCode

  constructor(code: OpenDocumentErrorCode, message: string, options?: ErrorOptions) {
    super(message, options)
    this.name = 'OpenDocumentError'
    this.code = code
  }
}

export function cloneOpenDocument<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

export function isValidEmbeddedMetadata(meta: SaveMetadata | null | undefined): meta is SaveMetadata {
  return !!meta
    && typeof meta.type === 'string' && meta.type.trim() !== ''
    && typeof meta.index === 'string' && meta.index.trim() !== ''
    && Number.isInteger(meta.chapter) && meta.chapter >= 0
    && typeof meta.source === 'string' && meta.source.trim() !== ''
    && typeof meta.scenarioId === 'string' && meta.scenarioId.trim() !== ''
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

const areaTalkStoryTypes = new Set([
  '初始地图对话',
  '升级地图对话',
  '追加地图对话',
])

const chapteredStoryTypes = new Set([
  '活动剧情',
  '主线剧情',
  '活动卡面',
  '特殊卡面',
  '初始卡面',
  '升级卡面',
])

export function parseOpenFileIdentity(result: OpenTranslationResult): ParsedDocumentFileName & {
  /** @deprecated The complete canonical identity; retained for focused callers. */
  label: string
} {
  const parsed = parseDocumentFileName(result.filePath || result.fileName || '')
  return { ...parsed, label: parsed.canonical }
}

function storyIdentityMatches(
  identity: string,
  story: Pick<OpenStoryResult, 'saveTitle' | 'chapterTitle'>,
  storyType: string,
): boolean {
  const canonical = identity.trim()
  const saveTitle = story.saveTitle.trim()
  const chapterTitle = story.chapterTitle.trim()
  if (!canonical || !saveTitle) return false
  // A chaptered story is never identified by SaveTitle alone. This is the
  // critical cold-open and current-story guard for card/Festival collisions.
  if (chapteredStoryTypes.has(storyType) && !chapterTitle) return false
  if (chapterTitle) {
    if (canonical === canonicalStoryIdentity(saveTitle, chapterTitle)) return true
    // Preserve the old third-slot display spelling only at this identity
    // boundary; it still resolves to the exact canonical special chapter.
    return chapterTitle === '特殊篇' && canonical === `${saveTitle} 其他`
  }
  return canonical === saveTitle
}

function legacyIdentityMatchesStory(
  identity: string,
  story: Pick<OpenStoryResult, 'saveTitle' | 'chapterTitle'>,
): boolean {
  const canonical = identity.trim()
  const saveTitle = story.saveTitle.trim()
  return canonical.startsWith(`${saveTitle} `)
    && canonical.slice(saveTitle.length + 1).trim() !== ''
}

function storyIdentityPartsMatch(
  story: Pick<OpenStoryResult, 'saveTitle' | 'chapterTitle'>,
  docMeta: Pick<DocMeta, 'saveTitle' | 'chapterTitle'>,
): boolean {
  return story.saveTitle.trim() !== ''
    && story.saveTitle.trim() === docMeta.saveTitle.trim()
    && story.chapterTitle.trim() === docMeta.chapterTitle.trim()
}

function fileModeFor(result: OpenTranslationResult, rawName: string): EditorMode {
  const prefix = rawName.match(/^【([^】]*)】/)?.[1] ?? ''
  const prefixMode = ({ 翻译: 0, 校对: 1, 合意: 2 } as Record<string, EditorMode>)[prefix]
  const embeddedMode = result.meta?.mode
  return embeddedMode === 0 || embeddedMode === 1 || embeddedMode === 2
    ? embeddedMode
    : prefixMode ?? 0
}

function assertCurrent(deps: OpenDocumentDependencies): void {
  if (!deps.isCurrent()) throw new OpenDocumentError('stale', 'document open operation is no longer current')
}

function currentStoryHasUsableIdentity(current: CurrentOpenStory | undefined): current is CurrentOpenStory & { docMeta: DocMeta } {
  const docMeta = current?.docMeta
  if (!current || !docMeta || !supportedStoryTypes.has(docMeta.type)) return false
  if (!validRequest({
    storyType: docMeta.type,
    sort: docMeta.sort,
    index: docMeta.index,
    chapter: docMeta.chapter,
    source: docMeta.source,
  })) return false
  if (docMeta.saveTitle.trim() === '' || docMeta.indexLabel.trim() === '' || docMeta.scenarioId.trim() === '') return false
  if (!storyIdentityPartsMatch(current.story, docMeta)) return false
  if (current.story.scenarioId.trim() === '' || current.story.scenarioId !== docMeta.scenarioId) return false
  if (!Array.isArray(current.story.sourceTalks) || current.story.sourceTalks.length === 0) return false
  return true
}

function currentStoryMatchesIdentity(
  identity: { canonical: string },
  current: CurrentOpenStory | undefined,
): boolean {
  if (!currentStoryHasUsableIdentity(current)) return false
  return storyIdentityMatches(identity.canonical, current.story, current.docMeta.type)
    && storyIdentityMatches(identity.canonical, current.docMeta, current.docMeta.type)
}

function currentStoryMatchesLegacyIdentity(
  identity: { canonical: string },
  current: CurrentOpenStory | undefined,
): boolean {
  if (!currentStoryHasUsableIdentity(current)) return false
  return legacyIdentityMatchesStory(identity.canonical, current.story)
    && legacyIdentityMatchesStory(identity.canonical, current.docMeta)
}

function usableStory(story: OpenStoryResult): boolean {
  return typeof story.scenarioId === 'string'
    && story.scenarioId.trim() !== ''
    && Array.isArray(story.sourceTalks)
    && story.sourceTalks.length > 0
}

function validRequest(request: OpenStoryRequest): boolean {
  return request.storyType.trim() !== ''
    && request.index.trim() !== ''
    && Number.isInteger(request.chapter)
    && request.chapter >= 0
    && request.source.trim() !== ''
}

function requestFromMeta(meta: SaveMetadata): OpenStoryRequest {
  return {
    storyType: meta.type,
    sort: meta.sort || '',
    index: meta.index,
    chapter: meta.chapter,
    source: meta.source,
  }
}

function resolvedIdentityMatchesRequest(
  resolved: { storyType: string; index: string; chapter: number },
  request: OpenStoryRequest,
): boolean {
  return resolved.storyType === request.storyType
    && resolved.index === request.index
    && resolved.chapter === request.chapter
}

function requestFromResolved(result: {
  storyType: string
  index: string
  chapter: number
  sort?: string
  source?: string
}): OpenStoryRequest {
  return {
    storyType: result.storyType,
    // ResolveLabel's stable cold area-talk coordinate is character-based. The
    // backend response predates an explicit sort field, so retain that
    // production contract while accepting a future field when present.
    sort: result.sort || (areaTalkStoryTypes.has(result.storyType) ? 'character' : ''),
    index: result.index,
    chapter: result.chapter,
    source: result.source || 'haruki',
  }
}

async function loadUsableStory(
  request: OpenStoryRequest,
  deps: OpenDocumentDependencies,
): Promise<OpenStoryResult> {
  if (!validRequest(request)) {
    throw new OpenDocumentError('invalid-metadata', 'embedded story metadata is incomplete')
  }
  let story: OpenStoryResult
  try {
    story = await deps.loadStory(request)
  } catch (error) {
    assertCurrent(deps)
    throw new OpenDocumentError('source-load-failed', 'source story could not be loaded', { cause: error })
  }
  assertCurrent(deps)
  if (!usableStory(story)) {
    throw new OpenDocumentError('source-load-failed', 'source story contains zero talks')
  }
  return cloneOpenDocument(story)
}

function makeDocMeta(
  request: OpenStoryRequest,
  story: OpenStoryResult,
  resolvedIndexLabel?: string,
): DocMeta {
  return {
    saveTitle: story.saveTitle.trim(),
    chapterTitle: story.chapterTitle.trim(),
    type: request.storyType,
    sort: request.sort,
    index: request.index,
    // The resolver/current document snapshot is the canonical file coordinate.
    // A mutable navigator label carried by the story store must not replace it.
    indexLabel: resolvedIndexLabel || story.indexLabel || request.index,
    chapter: request.chapter,
    source: request.source,
    scenarioId: story.scenarioId,
  }
}

export async function prepareOpenDocumentCandidate(options: {
  result: OpenTranslationResult
  editorMode: EditorMode
  isAndroid: boolean
  currentStory?: CurrentOpenStory
  deps: OpenDocumentDependencies
}): Promise<OpenDocumentCandidate> {
  const { result, editorMode, isAndroid, currentStory, deps } = options
  const inputTalks = cloneOpenDocument(result.talks || [])
  if (inputTalks.length === 0) {
    throw new OpenDocumentError('zero-talks', 'translation file contains zero talks')
  }
  assertCurrent(deps)

  const identity = parseOpenFileIdentity(result)
  const hasEmbeddedMetadata = isValidEmbeddedMetadata(result.meta)
  if (!identity.canonical && !hasEmbeddedMetadata) {
    throw new OpenDocumentError('destination-only', 'translation file has no usable story identity')
  }

  const fileMode = fileModeFor(result, identity.rawName)
  const deriving = fileMode !== editorMode
  const currentFilePath = deriving || isAndroid ? '' : (result.filePath || result.fileName || '')
  let request: OpenStoryRequest
  let loadedStory: OpenStoryResult
  let resolvedIndexLabel = ''

  if (isValidEmbeddedMetadata(result.meta)) {
    if (!supportedStoryTypes.has(result.meta.type)) {
      throw new OpenDocumentError('invalid-metadata', 'embedded metadata names an unsupported story type')
    }
    request = requestFromMeta(result.meta)
    loadedStory = await loadUsableStory(request, deps)
    if (loadedStory.scenarioId !== result.meta.scenarioId) {
      throw new OpenDocumentError('source-mismatch', 'embedded metadata does not match the loaded source story')
    }
    if (identity.canonical && !storyIdentityMatches(identity.canonical, loadedStory, request.storyType)) {
      // A valid legacy header plus the loaded scenario is enough to recover an
      // old custom-title filename, but first classify the suffix so a canonical
      // different chapter (for example 后篇 while metadata says 前篇) cannot be
      // disguised as an arbitrary title.
      if (!legacyIdentityMatchesStory(identity.canonical, loadedStory)) {
        throw new OpenDocumentError('source-mismatch', 'loaded source story does not match the complete file identity')
      }
      let resolved: Awaited<ReturnType<OpenDocumentDependencies['resolveLabel']>>
      try {
        resolved = await deps.resolveLabel(identity.canonical)
      } catch (error) {
        assertCurrent(deps)
        throw new OpenDocumentError('resolve-failed', 'legacy filename identity could not be classified', { cause: error })
      }
      assertCurrent(deps)
      const safelyBoundedLegacy = resolved.ok
        ? resolvedIdentityMatchesRequest(resolved, request)
        : resolved.reason === 'not-found' || resolved.reason === 'legacy-ambiguous'
      if (!safelyBoundedLegacy) {
        throw new OpenDocumentError('source-mismatch', 'loaded source story does not match the complete file identity')
      }
    }
  } else if (currentStoryMatchesIdentity(identity, currentStory)) {
    assertCurrent(deps)
    request = {
      storyType: currentStory!.docMeta!.type,
      sort: currentStory!.docMeta!.sort,
      index: currentStory!.docMeta!.index,
      chapter: currentStory!.docMeta!.chapter,
      source: currentStory!.docMeta!.source,
    }
    if (!validRequest(request)) {
      throw new OpenDocumentError('invalid-metadata', 'the matching current story has no usable coordinates')
    }
    loadedStory = cloneOpenDocument(currentStory!.story)
    resolvedIndexLabel = currentStory!.docMeta!.indexLabel.trim()
  } else {
    let resolved: Awaited<ReturnType<OpenDocumentDependencies['resolveLabel']>>
    try {
      // Pass the complete canonical identity. ResolveLabel is catalog-bounded
      // and must not be given a first-token approximation of a SaveTitle that
      // itself contains spaces. Legacy arbitrary title suffixes are classified
      // by the backend so an exact-but-ambiguous chapter can never be guessed.
      resolved = await deps.resolveLabel(identity.canonical)
    } catch (error) {
      assertCurrent(deps)
      throw new OpenDocumentError('resolve-failed', `could not resolve story identity ${identity.canonical}`, { cause: error })
    }
    assertCurrent(deps)
    if (!resolved.ok || !resolved.storyType || !resolved.index || !Number.isInteger(resolved.chapter) || resolved.chapter < 0) {
      const canReuseLegacyCurrent = (resolved.reason === 'not-found' || resolved.reason === 'legacy-ambiguous')
        && currentStoryMatchesLegacyIdentity(identity, currentStory)
      if (canReuseLegacyCurrent) {
        assertCurrent(deps)
        request = {
          storyType: currentStory!.docMeta!.type,
          sort: currentStory!.docMeta!.sort,
          index: currentStory!.docMeta!.index,
          chapter: currentStory!.docMeta!.chapter,
          source: currentStory!.docMeta!.source,
        }
        loadedStory = cloneOpenDocument(currentStory!.story)
        resolvedIndexLabel = currentStory!.docMeta!.indexLabel.trim()
      } else {
        throw new OpenDocumentError('resolve-failed', `could not resolve story identity ${identity.canonical}`)
      }
    } else {
      request = requestFromResolved(resolved)
      resolvedIndexLabel = resolved.indexLabel || ''
      loadedStory = await loadUsableStory(request, deps)
      const identityMatches = storyIdentityMatches(identity.canonical, loadedStory, request.storyType)
        || (resolved.matchKind === 'legacy' && legacyIdentityMatchesStory(identity.canonical, loadedStory))
      if (!identityMatches) {
        throw new OpenDocumentError('source-mismatch', 'resolved source story does not match the complete file identity')
      }
    }
  }

  assertCurrent(deps)
  let aligned: DstTalk[]
  try {
    aligned = await deps.checkLines({
      sourceTalks: cloneOpenDocument(loadedStory.sourceTalks),
      loadedTalks: inputTalks,
    })
  } catch (error) {
    assertCurrent(deps)
    throw new OpenDocumentError('source-load-failed', 'source alignment failed', { cause: error })
  }
  assertCurrent(deps)
  if (!Array.isArray(aligned) || aligned.length === 0) {
    throw new OpenDocumentError('source-load-failed', 'source alignment contains zero talks')
  }

  let talks: DstTalk[]
  let dstTalks: DstTalk[]
  let referTalks: DstTalk[]
  if (editorMode >= 1) {
    let compared: { talks: DstTalk[]; dstTalks: DstTalk[] }
    try {
      compared = await deps.compareText({
        referTalks: cloneOpenDocument(aligned),
        checkTalks: cloneOpenDocument(aligned),
        editorMode,
      })
    } catch (error) {
      assertCurrent(deps)
      throw new OpenDocumentError('comparison-failed', 'document comparison failed', { cause: error })
    }
    assertCurrent(deps)
    if (!Array.isArray(compared.talks) || compared.talks.length === 0 || !Array.isArray(compared.dstTalks) || compared.dstTalks.length === 0) {
      throw new OpenDocumentError('comparison-failed', 'document comparison produced no usable talks')
    }
    talks = cloneOpenDocument(compared.talks)
    dstTalks = cloneOpenDocument(compared.dstTalks)
    referTalks = cloneOpenDocument(aligned)
  } else {
    talks = cloneOpenDocument(aligned)
    dstTalks = cloneOpenDocument(aligned)
    referTalks = []
  }

  assertCurrent(deps)
  const docMeta = makeDocMeta(request, loadedStory, resolvedIndexLabel)
  const storyForCommit = cloneOpenDocument(loadedStory)
  // Publish one canonical label in both document and story state so the commit
  // cannot reintroduce a mutable navigator label through applyStory().
  storyForCommit.indexLabel = docMeta.indexLabel
  return {
    talks,
    dstTalks,
    referTalks,
    sourceTalks: cloneOpenDocument(loadedStory.sourceTalks),
    story: storyForCommit,
    docMeta,
    currentFilePath,
    titleOverride: titlePartForParsedFile(identity, loadedStory),
    fileMode,
    deriving,
  }
}

export type OpenDocumentTransactionOptions = Parameters<typeof prepareOpenDocumentCandidate>[0] & {
  // Runs after the entire candidate is prepared but before the in-memory commit.
  // Recovery cleanup belongs here: a failed cleanup must not replace the current
  // document, and a stale operation must never publish its candidate.
  beforeCommit?: (candidate: Readonly<OpenDocumentCandidate>) => void | Promise<void>
}

export async function runOpenDocumentTransaction(
  options: OpenDocumentTransactionOptions,
  commit: (candidate: OpenDocumentCandidate) => boolean | void,
): Promise<'committed' | 'stale'> {
  let candidate: OpenDocumentCandidate
  try {
    candidate = await prepareOpenDocumentCandidate(options)
    if (!options.deps.isCurrent()) return 'stale'
    if (options.beforeCommit) {
      try {
        await options.beforeCommit(candidate)
      } catch (error) {
        assertCurrent(options.deps)
        throw new OpenDocumentError('commit-preparation-failed', 'document commit preparation failed', { cause: error })
      }
    }
    if (!options.deps.isCurrent()) return 'stale'
  } catch (error) {
    if (error instanceof OpenDocumentError && error.code === 'stale') return 'stale'
    throw error
  }
  return commit(candidate) === false ? 'stale' : 'committed'
}
