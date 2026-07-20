import type { DstTalk, EditorMode, SourceTalk } from '../types/translation'
import type { DocMeta, EditorModeState } from '../stores/editor'

export interface RecoveryModeSave {
  // `talks` is the file-serializable destination list consumed by the backend.
  talks: DstTalk[]
  // The browser sidecar retains the full editor graph. In compare modes this is
  // not derivable from the serialized file: deleted rows, baselines and diffs
  // live in editorTalks/referTalks rather than the destination list.
  editorTalks: DstTalk[]
  referTalks: DstTalk[]
  filePath: string
  editorMode: EditorMode
  titleOverride: string
  hasUnsavedChanges: boolean
  sourceTalks: SourceTalk[]
  docMeta: DocMeta | null
}

export interface RecoverySaveRequestV2 {
  version: 2
  activeMode: EditorMode
  modes: RecoveryModeSave[]
  // Active-mode mirror keeps V1 readers functional.
  talks: DstTalk[]
  saveN: boolean
  filePath: string
  editorMode: EditorMode
  storyType?: string
  storySort?: string
  storyIndex?: string
  storyChapter?: number
  storySource?: string
}

export interface RecoveryStoredMode {
  content: string
  talks?: DstTalk[]
  dstTalks?: DstTalk[]
  referTalks?: DstTalk[]
  filePath: string
  editorMode: EditorMode
  titleOverride?: string
  hasUnsavedChanges?: boolean
  sourceTalks?: SourceTalk[]
  docMeta?: DocMeta | null
  storyType?: string
  storySort?: string
  storyIndex?: string
  storyChapter?: number
  storySource?: string
}

export interface RecoveryLoadResult {
  exists: boolean
  version?: number
  activeMode?: EditorMode
  modes?: RecoveryStoredMode[]
  content?: string
  filePath?: string
  editorMode?: number
  savedAt?: string
  storyType?: string
  storySort?: string
  storyIndex?: string
  storyChapter?: number
  storySource?: string
}

interface RecoveryRawMode extends RecoveryModeSave {
  content: string
  association: string
}

interface RecoveryRawSidecar {
  version: 3 | 4
  associationId: string
  committed: boolean
  activeMode: EditorMode
  modes: RecoveryRawMode[]
}

const RECOVERY_RAW_KEY = 'sekaitext:recovery-v2-raw'
const stagedPreviousSidecars = new Map<string, string | null>()

function cloneTalks(talks: DstTalk[]): DstTalk[] {
  return JSON.parse(JSON.stringify(talks)) as DstTalk[]
}

// Mirrors the backend's recovery serializer. The text is used only as a
// fingerprint tying the browser-side raw rows to the exact backend snapshot;
// the backend recovery file remains the source of truth for whether recovery
// exists. This preserves rows that the plain-text format cannot represent.
export function serializeRecoveryTalks(talks: DstTalk[], saveN: boolean): string {
  let content = ''
  for (const talk of talks) {
    if (['场景', '左上场景', '选项', ''].includes(talk.speaker)) {
      let text = talk.text
      if ((talk.speaker === '场景' || talk.speaker === '左上场景') && text === '') {
        text = talk.speaker
      } else if (talk.speaker === '选项' && !text.includes('/')) {
        text += '/'
      }
      content += text + '\n'
      continue
    }

    if (talk.start) content += talk.speaker + '：'
    content += talk.text.split('\n')[0]
    if (!talk.end && saveN) content += '\\N'
    else if (talk.end) content += '\n'
  }
  return content.replace(/\n+$/, '').replace(/\r\n/g, '\n').replace(/\n/g, '\r\n')
}

function modeAssociation(mode: {
  content: string
  filePath: string
  editorMode: EditorMode
  titleOverride?: string
  hasUnsavedChanges?: boolean
  sourceTalks?: SourceTalk[]
  docMeta?: DocMeta | null
}): string {
  return stableStringify({
    content: mode.content,
    filePath: mode.filePath,
    editorMode: mode.editorMode,
    titleOverride: mode.titleOverride || '',
    hasUnsavedChanges: mode.hasUnsavedChanges ?? true,
    sourceTalks: mode.sourceTalks ?? [],
    docMeta: mode.docMeta ?? null,
  })
}

function stableStringify(value: unknown): string {
  const canonicalize = (item: unknown): unknown => {
    if (Array.isArray(item)) return item.map(canonicalize)
    if (!item || typeof item !== 'object') return item
    return Object.fromEntries(
      Object.entries(item as Record<string, unknown>)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, child]) => [key, canonicalize(child)]),
    )
  }
  return JSON.stringify(canonicalize(value))
}

function createAssociationId(): string {
  return globalThis.crypto?.randomUUID?.()
    ?? `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
}

function recoveryRawSidecar(
  request: RecoverySaveRequestV2,
  associationId: string,
  committed: boolean,
): RecoveryRawSidecar {
  return {
    version: 4,
    associationId,
    committed,
    activeMode: request.activeMode,
    modes: request.modes.map(mode => {
      const raw = {
        ...mode,
        talks: cloneTalks(mode.talks),
        editorTalks: cloneTalks(mode.editorTalks),
        referTalks: cloneTalks(mode.referTalks),
        sourceTalks: JSON.parse(JSON.stringify(mode.sourceTalks)) as SourceTalk[],
        docMeta: mode.docMeta ? { ...mode.docMeta } : null,
        content: serializeRecoveryTalks(mode.talks, request.saveN),
      }
      return { ...raw, association: modeAssociation(raw) }
    }),
  }
}

function readRecoveryRawUnchecked(): RecoveryRawSidecar | null {
  if (typeof localStorage === 'undefined') return null
  try {
    const parsed = JSON.parse(localStorage.getItem(RECOVERY_RAW_KEY) || 'null') as RecoveryRawSidecar | null
    return (parsed?.version === 3 || parsed?.version === 4) && Array.isArray(parsed.modes) ? parsed : null
  } catch {
    return null
  }
}

// Stage the lossless rows first. Until commitRecoveryRaw runs after the backend
// write, recoveryModes ignores this payload, so a crash cannot pair it with the
// previous backend snapshot even when their serialized text is identical.
export function stageRecoveryRaw(request: RecoverySaveRequestV2): string | null {
  if (typeof localStorage === 'undefined') return null
  const associationId = createAssociationId()
  let previous: string | null = null
  try {
    previous = localStorage.getItem(RECOVERY_RAW_KEY)
    localStorage.setItem(RECOVERY_RAW_KEY, JSON.stringify(recoveryRawSidecar(request, associationId, false)))
    stagedPreviousSidecars.set(associationId, previous)
    return associationId
  } catch {
    // setItem is atomic: retain the previous committed sidecar until the backend
    // save succeeds. The coordinator removes it after success so it cannot be
    // paired with a newer backend snapshot.
    return null
  }
}

export function commitRecoveryRaw(associationId: string): void {
  if (typeof localStorage === 'undefined') return
  const sidecar = readRecoveryRawUnchecked()
  if (!sidecar || sidecar.associationId !== associationId) {
    stagedPreviousSidecars.delete(associationId)
    return
  }
  try {
    localStorage.setItem(RECOVERY_RAW_KEY, JSON.stringify({ ...sidecar, committed: true }))
  } catch {
    try { localStorage.removeItem(RECOVERY_RAW_KEY) } catch { /* best effort */ }
  } finally {
    stagedPreviousSidecars.delete(associationId)
  }
}

export function discardStagedRecoveryRaw(associationId: string): void {
  const sidecar = readRecoveryRawUnchecked()
  const hadPrevious = stagedPreviousSidecars.has(associationId)
  const previous = stagedPreviousSidecars.get(associationId) ?? null
  stagedPreviousSidecars.delete(associationId)
  if (sidecar?.associationId !== associationId) return
  try {
    if (hadPrevious && previous !== null) localStorage.setItem(RECOVERY_RAW_KEY, previous)
    else localStorage.removeItem(RECOVERY_RAW_KEY)
  } catch { /* best effort; the uncommitted sidecar is ignored on restore */ }
}

export function rememberRecoveryRaw(request: RecoverySaveRequestV2): void {
  const associationId = stageRecoveryRaw(request)
  if (associationId) commitRecoveryRaw(associationId)
}

export function forgetRecoveryRaw(): void {
  if (typeof localStorage === 'undefined') return
  try { localStorage.removeItem(RECOVERY_RAW_KEY) } catch { /* best effort */ }
}

function readRecoveryRaw(): RecoveryRawSidecar | null {
  const sidecar = readRecoveryRawUnchecked()
  return sidecar?.committed ? sidecar : null
}

export function buildRecoverySaveRequest(
  states: EditorModeState[],
  activeMode: EditorMode,
  saveN: boolean,
): RecoverySaveRequestV2 {
  const modes = states
    // Recovery contains only unsaved slots. Including a clean mode can restore
    // an older snapshot over a newer on-disk file after another mode crashes.
    .filter(state => state.hasUnsavedChanges)
    .map(state => ({
      talks: state.dstTalks,
      editorTalks: state.talks,
      referTalks: state.referTalks,
      filePath: state.currentFilePath,
      editorMode: state.mode,
      titleOverride: state.titleOverride,
      hasUnsavedChanges: state.hasUnsavedChanges,
      sourceTalks: state.sourceTalks,
      docMeta: state.docMeta,
    }))
  const active = modes.find(mode => mode.editorMode === activeMode) ?? modes[0]
  const meta = active?.docMeta
  return {
    version: 2,
    activeMode,
    modes,
    talks: active?.talks ?? [],
    saveN,
    filePath: active?.filePath ?? '',
    editorMode: active?.editorMode ?? activeMode,
    storyType: meta?.type || undefined,
    storySort: meta?.sort || undefined,
    storyIndex: meta?.index || undefined,
    storyChapter: meta && meta.chapter >= 0 ? meta.chapter : undefined,
    storySource: meta?.source || undefined,
  }
}

export function recoveryModes(result: RecoveryLoadResult): RecoveryStoredMode[] {
  if (result.modes?.length) {
    const raw = readRecoveryRaw()
    return result.modes.map(mode => {
      const match = raw?.modes.find(candidate =>
        raw.activeMode === (result.activeMode ?? 0)
        && candidate.association === modeAssociation(mode),
      )
      if (!match) return mode
      return {
        ...mode,
        talks: cloneTalks(match.editorTalks ?? match.talks),
        dstTalks: cloneTalks(match.talks),
        referTalks: cloneTalks(match.referTalks ?? []),
      }
    })
  }
  if (!result.content) return []
  return [{
    content: result.content,
    filePath: result.filePath || '',
    editorMode: (result.editorMode ?? 0) as EditorMode,
    hasUnsavedChanges: true,
    storyType: result.storyType,
    storySort: result.storySort,
    storyIndex: result.storyIndex,
    storyChapter: result.storyChapter,
    storySource: result.storySource,
  }]
}

export function hasRecovery(result: RecoveryLoadResult): boolean {
  return result.exists && ((result.modes?.length ?? 0) > 0 || !!result.content)
}
