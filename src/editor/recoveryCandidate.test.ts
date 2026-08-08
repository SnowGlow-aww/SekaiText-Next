import { describe, expect, it, vi } from 'vitest'
import type { DstTalk, SourceTalk } from '../types/translation'
import type { RecoveryLoadResult } from './recovery'
import {
  prepareRecoveryDocumentCandidate,
  type RecoveryCandidateDependencies,
} from './recoveryCandidate'

function talk(text: string): DstTalk {
  return {
    idx: 1,
    speaker: '愛莉',
    text,
    start: true,
    end: true,
    checked: true,
    save: true,
    dstidx: 0,
  }
}

function source(text = '原文'): SourceTalk {
  return { speaker: '愛莉', text, charIndex: 0 }
}

function recovery(overrides: Partial<RecoveryLoadResult> = {}): RecoveryLoadResult {
  return {
    exists: true,
    version: 2,
    activeMode: 0,
    modes: [{
      content: '愛莉：译文',
      talks: [talk('译文')],
      dstTalks: [talk('译文')],
      referTalks: [],
      filePath: '/drafts/【翻译】event211-airi 前篇【标题】爱莉前篇译文.txt',
      editorMode: 0,
      titleOverride: '',
      hasUnsavedChanges: true,
      sourceTalks: [source()],
      docMeta: {
        saveTitle: 'event211-airi',
        chapterTitle: '前篇',
        type: '活动卡面',
        sort: '',
        index: '211',
        indexLabel: '211 卡面活动',
        chapter: 3,
        source: 'haruki',
        scenarioId: '007054_airi01',
      },
    }],
    ...overrides,
  }
}

function dependencies(overrides: Partial<RecoveryCandidateDependencies> = {}): RecoveryCandidateDependencies {
  return {
    loadTranslationContent: vi.fn(async () => ({ talks: [talk('译文')] })),
    loadStory: vi.fn(async () => ({
      scenarioId: '007054_airi01',
      sourceTalks: [source()],
      saveTitle: 'event211-airi',
      chapterTitle: '前篇',
      indexLabel: '211 卡面活动',
    })),
    checkLines: vi.fn(async ({ loadedTalks }: { sourceTalks: SourceTalk[]; loadedTalks: DstTalk[] }) => loadedTalks.map(item => ({ ...item }))),
    compareText: vi.fn(async ({ checkTalks }: { referTalks: DstTalk[]; checkTalks: DstTalk[]; editorMode: number }) => ({
      talks: checkTalks.map(item => ({ ...item })),
      dstTalks: checkTalks.map(item => ({ ...item })),
    })),
    loadSorts: vi.fn(async () => []),
    loadIndices: vi.fn(async () => [{ label: '211 卡面活动', value: '211' }]),
    loadChapters: vi.fn(async () => [{ number: 3, label: '前篇' }]),
    isCurrent: () => true,
    ...overrides,
  }
}

describe('recovery candidate transaction', () => {
  it('prepares every document field locally and keeps the canonical story identity', async () => {
    const candidate = await prepareRecoveryDocumentCandidate(recovery(), dependencies())

    expect(candidate.activeMode).toBe(0)
    expect(candidate.states).toHaveLength(1)
    expect(candidate.states[0]).toEqual(expect.objectContaining({
      talks: [expect.objectContaining({ text: '译文' })],
      sourceTalks: [source()],
      titleOverride: '爱莉前篇译文',
      currentFilePath: '/drafts/【翻译】event211-airi 前篇【标题】爱莉前篇译文.txt',
      recoveryPending: true,
      docMeta: expect.objectContaining({
        saveTitle: 'event211-airi',
        chapterTitle: '前篇',
        scenarioId: '007054_airi01',
      }),
    }))
    expect(candidate.activeStory.scenarioId).toBe('007054_airi01')
    expect(candidate.navigator.meta.indexLabel).toBe('211 卡面活动')
  })

  it('rejects an empty recovered translation before loading or committing story state', async () => {
    const deps = dependencies()
    const result = recovery({
      modes: [{
        ...recovery().modes![0],
        talks: [],
        dstTalks: [],
      }],
    })

    await expect(prepareRecoveryDocumentCandidate(result, deps)).rejects.toMatchObject({ code: 'zero-talks' })
    expect(deps.loadStory).not.toHaveBeenCalled()
  })

  it('rejects missing source rows or scenarioId', async () => {
    const deps = dependencies({
      loadStory: vi.fn(async () => ({
        scenarioId: '',
        sourceTalks: [],
        saveTitle: 'event211-airi',
        chapterTitle: '前篇',
        indexLabel: '211 卡面活动',
      })),
    })

    await expect(prepareRecoveryDocumentCandidate(recovery(), deps)).rejects.toMatchObject({ code: 'source-load-failed' })
    expect(deps.checkLines).not.toHaveBeenCalled()
  })

  it('rejects a recovery whose persisted scenario does not match the loaded source', async () => {
    const deps = dependencies({
      loadStory: vi.fn(async () => ({
        scenarioId: 'different-scenario',
        sourceTalks: [source()],
        saveTitle: 'event211-airi',
        chapterTitle: '前篇',
        indexLabel: '211 卡面活动',
      })),
    })

    await expect(prepareRecoveryDocumentCandidate(recovery(), deps)).rejects.toMatchObject({ code: 'source-mismatch' })
    expect(deps.checkLines).not.toHaveBeenCalled()
  })

  it('rejects empty alignment and empty comparison results', async () => {
    await expect(prepareRecoveryDocumentCandidate(
      recovery(),
      dependencies({ checkLines: vi.fn(async () => []) }),
    )).rejects.toMatchObject({ code: 'alignment-failed' })

    const proofread = recovery({
      activeMode: 1,
      modes: [{
        ...recovery().modes![0],
        editorMode: 1,
        referTalks: [talk('基准')],
      }],
    })
    await expect(prepareRecoveryDocumentCandidate(
      proofread,
      dependencies({ compareText: vi.fn(async () => ({ talks: [], dstTalks: [] })) }),
    )).rejects.toMatchObject({ code: 'comparison-failed' })
  })

  it('rejects a stale asynchronous recovery candidate', async () => {
    let current = true
    const deps = dependencies({
      loadStory: vi.fn(async () => {
        current = false
        return {
          scenarioId: '007054_airi01',
          sourceTalks: [source()],
          saveTitle: 'event211-airi',
          chapterTitle: '前篇',
          indexLabel: '211 卡面活动',
        }
      }),
      isCurrent: () => current,
    })

    await expect(prepareRecoveryDocumentCandidate(recovery(), deps)).rejects.toMatchObject({ code: 'stale' })
    expect(deps.checkLines).not.toHaveBeenCalled()
  })
})
