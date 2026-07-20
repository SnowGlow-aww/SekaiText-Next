import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  buildRecoverySaveRequest,
  hasRecovery,
  recoveryModes,
  rememberRecoveryRaw,
  serializeRecoveryTalks,
} from './recovery'
import type { EditorModeState } from '../stores/editor'
import type { RecoveryLoadResult } from './recovery'

function modeState(mode: 0 | 1 | 2, title: string): EditorModeState {
  const talk = {
    idx: 1,
    speaker: '瑞希',
    text: `${title}译文`,
    start: true,
    end: true,
    checked: true,
    save: true,
    dstidx: 0,
  }
  return {
    mode,
    talks: [talk],
    dstTalks: [talk],
    referTalks: [],
    sourceTalks: [{ speaker: '瑞希', text: `${title}原文`, charIndex: 0 }],
    currentFilePath: `/${title}.txt`,
    titleOverride: title,
    hasUnsavedChanges: true,
    recoveryPending: false,
    majorClue: null,
    mutationSeq: mode + 1,
    docMeta: {
      saveTitle: `${title}-01`,
      chapterTitle: `${title}章节`,
      type: 'event',
      sort: 'unit',
      index: String(mode),
      indexLabel: `${mode} ${title}`,
      chapter: mode,
      source: 'haruki',
      scenarioId: `scenario-${mode}`,
    },
  }
}

describe('Recovery V2', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('saves every mode with title and complete source context', () => {
    const request = buildRecoverySaveRequest(
      [modeState(0, '翻译'), modeState(1, '校对')],
      1,
      true,
    )

    expect(request.version).toBe(2)
    expect(request.activeMode).toBe(1)
    expect(request.modes).toHaveLength(2)
    expect(request.modes[0]).toEqual(expect.objectContaining({
      editorMode: 0,
      editorTalks: [expect.objectContaining({ text: '翻译译文' })],
      referTalks: [],
      titleOverride: '翻译',
      sourceTalks: [{ speaker: '瑞希', text: '翻译原文', charIndex: 0 }],
      docMeta: expect.objectContaining({ scenarioId: 'scenario-0', indexLabel: '0 翻译' }),
    }))
    // Legacy top-level fields mirror the active mode for older readers.
    expect(request.talks[0].text).toBe('校对译文')
    expect(request.editorMode).toBe(1)
  })

  it('normalizes an old single-mode recovery JSON as one restorable mode', () => {
    const modes = recoveryModes({
      exists: true,
      content: '瑞希：旧译文',
      filePath: '/old.txt',
      editorMode: 2,
      storyType: 'event',
      storyIndex: '17',
      storyChapter: 3,
      storySource: 'haruki',
    })

    expect(modes).toEqual([expect.objectContaining({
      content: '瑞希：旧译文',
      filePath: '/old.txt',
      editorMode: 2,
    })])
  })

  it('keeps a dirty mode even when its raw document has no serializable rows', () => {
    const state = modeState(0, '仅标题')
    state.talks = []
    state.dstTalks = []

    const request = buildRecoverySaveRequest([state], 0, true)

    expect(request.modes).toHaveLength(1)
    expect(request.modes[0].talks).toEqual([])
    expect(hasRecovery({ exists: true, modes: [{
      content: '', filePath: '', editorMode: 0,
    }] })).toBe(true)
  })

  it('drops a mode from the next snapshot as soon as it becomes clean', () => {
    const dirty = modeState(0, '未保存')
    const clean = modeState(1, '已保存')
    clean.hasUnsavedChanges = false

    const request = buildRecoverySaveRequest([dirty, clean], 1, true)

    expect(request.modes.map(mode => mode.editorMode)).toEqual([0])
    expect(request.editorMode).toBe(0)
  })

  it('restores exact raw rows only when their serialized backend snapshot matches', () => {
    const values = new Map<string, string>()
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
    })
    const state = modeState(0, '特殊行')
    state.talks = state.dstTalks = [
      { ...state.talks[0], speaker: '', text: '', idx: 1, dstidx: 0 },
      { ...state.talks[0], speaker: '', text: '', idx: 2, dstidx: 1 },
      { ...state.talks[0], speaker: '场景', text: '', idx: 3, dstidx: 2 },
    ]
    const request = buildRecoverySaveRequest([state], 0, true)
    rememberRecoveryRaw(request)
    const stored = request.modes[0]
    const result: RecoveryLoadResult = {
      exists: true,
      version: 2,
      activeMode: 0,
      modes: [{
        content: serializeRecoveryTalks(stored.talks, true),
        filePath: stored.filePath,
        editorMode: stored.editorMode,
        titleOverride: stored.titleOverride,
        hasUnsavedChanges: stored.hasUnsavedChanges,
        sourceTalks: stored.sourceTalks,
        docMeta: stored.docMeta,
      }],
    }

    expect(recoveryModes(result)[0].talks).toEqual(state.dstTalks)
    result.modes![0].content += 'stale'
    expect(recoveryModes(result)[0].talks).toBeUndefined()
  })

  it('does not attach raw rows to matching text with different metadata', () => {
    const values = new Map<string, string>()
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
    })
    const state = modeState(0, '关联')
    const request = buildRecoverySaveRequest([state], 0, true)
    rememberRecoveryRaw(request)
    const stored = request.modes[0]

    const restored = recoveryModes({
      exists: true,
      version: 2,
      activeMode: 0,
      modes: [{
        content: serializeRecoveryTalks(stored.talks, true),
        filePath: stored.filePath,
        editorMode: stored.editorMode,
        titleOverride: stored.titleOverride,
        hasUnsavedChanges: true,
        sourceTalks: stored.sourceTalks,
        docMeta: { ...stored.docMeta!, scenarioId: 'different-scenario' },
      }],
    })

    expect(restored[0].talks).toBeUndefined()
  })

  it('restores agreement baselines, deletion rows and diffs without deriving them from text', () => {
    const values = new Map<string, string>()
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
    })
    const state = modeState(2, '合意')
    const destination = {
      ...state.dstTalks[0],
      text: '合意稿',
      baseline: '校对稿',
      diff: [
        { text: '校对', type: 'remove' as const },
        { text: '合意', type: 'add' as const },
        { text: '稿', type: 'same' as const },
      ],
    }
    const deleted = {
      ...destination,
      idx: 2,
      dstidx: -1,
      text: '',
      save: false,
      baseline: '已删除行',
      diff: [{ text: '已删除行', type: 'remove' as const }],
    }
    const reference = { ...destination, text: '翻译稿', baseline: undefined, diff: undefined }
    state.talks = [destination, deleted]
    state.dstTalks = [destination]
    state.referTalks = [reference]
    const request = buildRecoverySaveRequest([state], 2, true)
    rememberRecoveryRaw(request)
    const stored = request.modes[0]

    const [restored] = recoveryModes({
      exists: true,
      version: 2,
      activeMode: 2,
      modes: [{
        content: serializeRecoveryTalks(stored.talks, true),
        filePath: stored.filePath,
        editorMode: stored.editorMode,
        titleOverride: stored.titleOverride,
        hasUnsavedChanges: stored.hasUnsavedChanges,
        sourceTalks: stored.sourceTalks,
        docMeta: stored.docMeta,
      }],
    })

    expect(restored.talks).toEqual(state.talks)
    expect(restored.dstTalks).toEqual(state.dstTalks)
    expect(restored.referTalks).toEqual(state.referTalks)
    expect(restored.talks?.[1]).toEqual(expect.objectContaining({
      save: false,
      baseline: '已删除行',
      diff: [{ text: '已删除行', type: 'remove' }],
    }))
  })
})
