import type { DocMeta } from '../stores/editor'
import type { DstTalk, SourceTalk } from '../types/translation'
import { isValidEmbeddedMetadata, type OpenTranslationResult } from './openDocument'
import {
  canonicalStoryIdentity,
  parseDocumentFileName,
  titlePartForParsedFile,
} from './documentFileName'

export interface BaselineImportCandidate {
  talks: DstTalk[]
  dstTalks: DstTalk[]
  referTalks: DstTalk[]
  titleOverride: string
  importedName: string
}

export interface BaselineImportDependencies {
  resolveLabel: (label: string) => Promise<{
    ok: boolean
    storyType: string
    index: string
    indexLabel: string
    chapter: number
    matchKind?: 'exact' | 'legacy'
    reason?: 'not-found' | 'exact-ambiguous' | 'legacy-ambiguous'
  }>
  checkLines: (data: { sourceTalks: SourceTalk[]; loadedTalks: DstTalk[] }) => Promise<DstTalk[]>
  compareText: (data: {
    referTalks: DstTalk[]
    checkTalks: DstTalk[]
    editorMode: number
  }) => Promise<{ talks: DstTalk[]; dstTalks: DstTalk[] }>
  isCurrent: () => boolean
}

export type BaselineImportErrorCode =
  | 'stale'
  | 'missing-current-document'
  | 'zero-talks'
  | 'invalid-identity'
  | 'identity-mismatch'
  | 'alignment-failed'
  | 'comparison-failed'

export class BaselineImportError extends Error {
  readonly code: BaselineImportErrorCode

  constructor(code: BaselineImportErrorCode, message: string, options?: ErrorOptions) {
    super(message, options)
    this.name = 'BaselineImportError'
    this.code = code
  }
}

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

function assertCurrent(deps: BaselineImportDependencies): void {
  if (!deps.isCurrent()) throw new BaselineImportError('stale', 'baseline import is no longer current')
}

function currentDocumentIsUsable(docMeta: DocMeta | null, currentTalks: DstTalk[], sourceTalks: SourceTalk[]): docMeta is DocMeta {
  return !!docMeta
    && docMeta.type.trim() !== ''
    && docMeta.index.trim() !== ''
    && Number.isInteger(docMeta.chapter)
    && docMeta.chapter >= 0
    && docMeta.source.trim() !== ''
    && docMeta.scenarioId.trim() !== ''
    && docMeta.saveTitle.trim() !== ''
    && currentTalks.length > 0
    && sourceTalks.length > 0
}

function exactIdentityMatches(identity: string, docMeta: DocMeta): boolean {
  const canonical = identity.trim()
  const exact = canonicalStoryIdentity(docMeta.saveTitle, docMeta.chapterTitle)
  return canonical === exact
    || (docMeta.chapterTitle.trim() === '特殊篇' && canonical === `${docMeta.saveTitle.trim()} 其他`)
}

function legacyIdentityMatches(identity: string, docMeta: DocMeta): boolean {
  const saveTitle = docMeta.saveTitle.trim()
  const canonical = identity.trim()
  return canonical.startsWith(`${saveTitle} `)
    && canonical.slice(saveTitle.length + 1).trim() !== ''
}

function resolvedIdentityMatchesCurrent(
  resolved: { storyType: string; index: string; chapter: number },
  docMeta: DocMeta,
): boolean {
  return resolved.storyType === docMeta.type
    && resolved.index === docMeta.index
    && resolved.chapter === docMeta.chapter
}

function embeddedMetadataMatchesCurrent(
  embedded: NonNullable<OpenTranslationResult['meta']>,
  docMeta: DocMeta,
): boolean {
  const embeddedSort = (embedded.sort || '').trim()
  return embedded.type === docMeta.type
    && embedded.index === docMeta.index
    && embedded.chapter === docMeta.chapter
    && embedded.source === docMeta.source
    && embedded.scenarioId === docMeta.scenarioId
    // Older headers may omit sort. A present sort must still agree; scenarioId
    // remains the final exact story guard for area-talk coordinate variants.
    && (embeddedSort === '' || embeddedSort === docMeta.sort.trim())
}

export async function prepareBaselineImportCandidate(options: {
  result: OpenTranslationResult
  currentTalks: DstTalk[]
  currentReferTalks: DstTalk[]
  sourceTalks: SourceTalk[]
  docMeta: DocMeta | null
  deps: BaselineImportDependencies
}): Promise<BaselineImportCandidate> {
  const { result, docMeta, deps } = options
  const currentTalks = clone(options.currentTalks || [])
  const sourceTalks = clone(options.sourceTalks || [])
  if (!currentDocumentIsUsable(docMeta, currentTalks, sourceTalks)) {
    throw new BaselineImportError('missing-current-document', '当前文档缺少译文、原文或 scenarioId')
  }

  const importedTalks = clone(result.talks || [])
  if (importedTalks.length === 0) {
    throw new BaselineImportError('zero-talks', '导入文件没有译文')
  }
  assertCurrent(deps)

  const hasEmbeddedMetadata = result.meta != null
  if (hasEmbeddedMetadata) {
    if (!isValidEmbeddedMetadata(result.meta)) {
      throw new BaselineImportError('invalid-identity', '导入文件内嵌剧情信息不完整')
    }
    if (!embeddedMetadataMatchesCurrent(result.meta, docMeta)) {
      throw new BaselineImportError('identity-mismatch', '导入文件内嵌剧情信息与当前剧情不一致')
    }
  }

  const parsed = parseDocumentFileName(result.filePath || result.fileName || '')
  if (!parsed.canonical && !hasEmbeddedMetadata) {
    throw new BaselineImportError('invalid-identity', '导入文件没有可用剧情身份')
  }

  // Exact matching embedded metadata can identify a renamed legacy file on its
  // own. When a filename identity is present, still validate it so a forged
  // current-looking name or a canonical different chapter cannot contradict the
  // embedded scenario.
  let identityMatches = !parsed.canonical && hasEmbeddedMetadata
  if (parsed.canonical) identityMatches = exactIdentityMatches(parsed.canonical, docMeta)
  if (parsed.canonical && !identityMatches) {
    let resolved: Awaited<ReturnType<BaselineImportDependencies['resolveLabel']>>
    try {
      resolved = await deps.resolveLabel(parsed.canonical)
    } catch (error) {
      assertCurrent(deps)
      throw new BaselineImportError('invalid-identity', '导入文件剧情身份无法反解', { cause: error })
    }
    assertCurrent(deps)
    if (resolved.ok) {
      identityMatches = resolvedIdentityMatchesCurrent(resolved, docMeta)
    } else if ((resolved.reason === 'not-found' || resolved.reason === 'legacy-ambiguous')
      && legacyIdentityMatches(parsed.canonical, docMeta)) {
      // The complete current document identity, source rows and scenarioId are
      // already known. This is the same bounded legacy-title disambiguation as
      // the generic file-open transaction; exact catalog ambiguity never enters.
      identityMatches = true
    }
  }
  if (!identityMatches) {
    throw new BaselineImportError('identity-mismatch', '导入文件与当前剧情不是同一章节')
  }

  let aligned: DstTalk[]
  try {
    aligned = clone(await deps.checkLines({
      sourceTalks: clone(sourceTalks),
      loadedTalks: importedTalks,
    }))
  } catch (error) {
    assertCurrent(deps)
    throw new BaselineImportError('alignment-failed', '导入文件行对齐失败', { cause: error })
  }
  assertCurrent(deps)
  if (aligned.length === 0) {
    throw new BaselineImportError('alignment-failed', '导入文件行对齐结果为空')
  }

  let compared: { talks: DstTalk[]; dstTalks: DstTalk[] }
  try {
    compared = await deps.compareText({
      referTalks: clone(currentTalks),
      checkTalks: clone(aligned),
      editorMode: 2,
    })
  } catch (error) {
    assertCurrent(deps)
    throw new BaselineImportError('comparison-failed', '导入文件对比失败', { cause: error })
  }
  assertCurrent(deps)
  if (!Array.isArray(compared.talks) || compared.talks.length === 0
    || !Array.isArray(compared.dstTalks) || compared.dstTalks.length === 0) {
    throw new BaselineImportError('comparison-failed', '导入文件对比结果为空')
  }

  return {
    talks: clone(compared.talks),
    dstTalks: clone(compared.dstTalks),
    referTalks: clone(options.currentReferTalks?.length ? options.currentReferTalks : currentTalks),
    titleOverride: titlePartForParsedFile(parsed, docMeta),
    importedName: parsed.rawName,
  }
}

export async function runBaselineImportTransaction(
  options: Parameters<typeof prepareBaselineImportCandidate>[0],
  commit: (candidate: BaselineImportCandidate) => boolean | void,
): Promise<'committed' | 'stale'> {
  try {
    const candidate = await prepareBaselineImportCandidate(options)
    if (!options.deps.isCurrent()) return 'stale'
    return commit(candidate) === false ? 'stale' : 'committed'
  } catch (error) {
    if (error instanceof BaselineImportError && error.code === 'stale') return 'stale'
    throw error
  }
}
