import { describe, expect, it, vi } from 'vitest'
import type { DocMeta } from '../stores/editor'
import type { DstTalk, SourceTalk } from '../types/translation'
import {
  prepareBaselineImportCandidate,
  runBaselineImportTransaction,
  type BaselineImportDependencies,
} from './baselineImport'
import type { OpenTranslationResult } from './openDocument'

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

function source(): SourceTalk {
  return { speaker: '愛莉', text: '原文', charIndex: 0 }
}

function meta(overrides: Partial<DocMeta> = {}): DocMeta {
  return {
    saveTitle: 'event211-airi',
    chapterTitle: '前篇',
    type: '活动卡面',
    sort: '',
    index: '211',
    indexLabel: '211 卡面活动',
    chapter: 3,
    source: 'haruki',
    scenarioId: '007054_airi01',
    ...overrides,
  }
}

function result(fileName = '【校对】event211-airi 前篇【标题】爱莉校对稿.txt', talks = [talk('校对稿')]): OpenTranslationResult {
  return { talks, meta: null, fileName }
}

function dependencies(overrides: Partial<BaselineImportDependencies> = {}): BaselineImportDependencies {
  return {
    resolveLabel: vi.fn(async () => ({
      ok: true,
      storyType: '活动卡面',
      index: '211',
      indexLabel: '211 卡面活动',
      chapter: 3,
      matchKind: 'exact' as const,
    })),
    checkLines: vi.fn(async ({ loadedTalks }: { sourceTalks: SourceTalk[]; loadedTalks: DstTalk[] }) => loadedTalks.map(item => ({ ...item }))),
    compareText: vi.fn(async ({ checkTalks }: { referTalks: DstTalk[]; checkTalks: DstTalk[]; editorMode: number }) => ({
      talks: checkTalks.map(item => ({ ...item })),
      dstTalks: checkTalks.map(item => ({ ...item })),
    })),
    isCurrent: () => true,
    ...overrides,
  }
}

function options(deps = dependencies(), input = result()) {
  return {
    result: input,
    currentTalks: [talk('翻译稿')],
    currentReferTalks: [talk('翻译基准')],
    sourceTalks: [source()],
    docMeta: meta(),
    deps,
  }
}

describe('agreement baseline import transaction', () => {
  it('aligns and compares a canonical same-story import before returning a complete candidate', async () => {
    const deps = dependencies()
    const candidate = await prepareBaselineImportCandidate(options(deps))

    expect(deps.resolveLabel).not.toHaveBeenCalled()
    expect(deps.checkLines).toHaveBeenCalledOnce()
    expect(deps.compareText).toHaveBeenCalledWith(expect.objectContaining({ editorMode: 2 }))
    expect(candidate).toEqual(expect.objectContaining({
      talks: [expect.objectContaining({ text: '校对稿' })],
      dstTalks: [expect.objectContaining({ text: '校对稿' })],
      referTalks: [expect.objectContaining({ text: '翻译基准' })],
      titleOverride: '爱莉校对稿',
    }))
  })

  it('allows an arbitrary legacy title only through the exact current document identity', async () => {
    const resolveLabel = vi.fn(async () => ({
      ok: false,
      storyType: '',
      index: '',
      indexLabel: '',
      chapter: 0,
      reason: 'legacy-ambiguous' as const,
    }))
    const candidate = await prepareBaselineImportCandidate(options(
      dependencies({ resolveLabel }),
      result('【校对】event211-airi 爱莉校对稿.txt'),
    ))

    expect(resolveLabel).toHaveBeenCalledWith('event211-airi 爱莉校对稿')
    expect(candidate.titleOverride).toBe('爱莉校对稿')
  })

  it('rejects mismatching embedded metadata before resolver, alignment, comparison, or commit', async () => {
    const deps = dependencies()
    const commit = vi.fn()
    const input = result('【校对】event211-airi 前篇.txt')
    input.meta = {
      type: '活动卡面',
      sort: '',
      index: '211',
      chapter: 4,
      source: 'haruki',
      scenarioId: 'other-scenario',
      mode: 1,
    }

    await expect(runBaselineImportTransaction(options(deps, input), commit))
      .rejects.toMatchObject({ code: 'identity-mismatch' })
    expect(deps.resolveLabel).not.toHaveBeenCalled()
    expect(deps.checkLines).not.toHaveBeenCalled()
    expect(deps.compareText).not.toHaveBeenCalled()
    expect(commit).not.toHaveBeenCalled()
  })

  it('accepts an exactly matching embedded identity even when the legacy filename has no usable identity', async () => {
    const deps = dependencies()
    const input = result('【校对】.txt')
    input.meta = {
      type: '活动卡面',
      sort: '',
      index: '211',
      chapter: 3,
      source: 'haruki',
      scenarioId: '007054_airi01',
      mode: 1,
    }

    await expect(prepareBaselineImportCandidate(options(deps, input)))
      .resolves.toEqual(expect.objectContaining({ importedName: '【校对】.txt' }))
    expect(deps.resolveLabel).not.toHaveBeenCalled()
    expect(deps.checkLines).toHaveBeenCalledOnce()
  })

  it('rejects an empty file or a current document without source/scenario before alignment', async () => {
    const emptyDeps = dependencies()
    await expect(prepareBaselineImportCandidate(options(emptyDeps, result('【校对】event211-airi 前篇.txt', []))))
      .rejects.toMatchObject({ code: 'zero-talks' })
    expect(emptyDeps.checkLines).not.toHaveBeenCalled()

    const missingDeps = dependencies()
    await expect(prepareBaselineImportCandidate({
      ...options(missingDeps),
      sourceTalks: [],
      docMeta: meta({ scenarioId: '' }),
    })).rejects.toMatchObject({ code: 'missing-current-document' })
    expect(missingDeps.resolveLabel).not.toHaveBeenCalled()
  })

  it('rejects a different canonical chapter even when it resolves successfully', async () => {
    const deps = dependencies({
      resolveLabel: vi.fn(async () => ({
        ok: true,
        storyType: '活动卡面',
        index: '211',
        indexLabel: '211 卡面活动',
        chapter: 4,
        matchKind: 'exact' as const,
      })),
    })

    await expect(prepareBaselineImportCandidate(options(
      deps,
      result('【校对】event211-airi 后篇.txt'),
    ))).rejects.toMatchObject({ code: 'identity-mismatch' })
    expect(deps.checkLines).not.toHaveBeenCalled()
  })

  it('rejects empty alignment and comparison output', async () => {
    await expect(prepareBaselineImportCandidate(options(
      dependencies({ checkLines: vi.fn(async () => []) }),
    ))).rejects.toMatchObject({ code: 'alignment-failed' })

    await expect(prepareBaselineImportCandidate(options(
      dependencies({ compareText: vi.fn(async () => ({ talks: [], dstTalks: [] })) }),
    ))).rejects.toMatchObject({ code: 'comparison-failed' })
  })

  it('never invokes commit for a stale asynchronous import', async () => {
    let current = true
    const commit = vi.fn()
    const deps = dependencies({
      checkLines: vi.fn(async ({ loadedTalks }: { sourceTalks: SourceTalk[]; loadedTalks: DstTalk[] }) => {
        current = false
        return loadedTalks
      }),
      isCurrent: () => current,
    })

    await expect(runBaselineImportTransaction(options(deps), commit)).resolves.toBe('stale')
    expect(commit).not.toHaveBeenCalled()
  })
})
