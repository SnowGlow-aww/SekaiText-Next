import { describe, expect, it, vi } from 'vitest'
import type { DocMeta } from '../stores/editor'
import type { DstTalk, SourceTalk } from '../types/translation'
import {
  runOpenDocumentTransaction,
  type OpenDocumentDependencies,
  type OpenStoryRequest,
  type OpenStoryResult,
  type OpenTranslationResult,
} from './openDocument'

function talk(text: string, idx = 1): DstTalk {
  return {
    idx,
    speaker: '愛莉',
    text,
    start: true,
    end: true,
    checked: true,
    save: true,
    dstidx: 0,
  }
}

function source(text: string, charIndex = 0): SourceTalk {
  return { speaker: '愛莉', text, charIndex }
}

function story(overrides: Partial<OpenStoryResult> = {}): OpenStoryResult {
  return {
    scenarioId: '007054_airi01',
    sourceTalks: [source('原文')],
    saveTitle: 'event211-airi',
    chapterTitle: '前篇',
    indexLabel: '211 卡面活动',
    ...overrides,
  }
}

function result(fileName = '【翻译】event211-airi 前篇.txt', overrides: Partial<OpenTranslationResult> = {}): OpenTranslationResult {
  return {
    talks: [talk('译文')],
    meta: null,
    fileName,
    ...overrides,
  }
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

function dependencies(overrides: Partial<OpenDocumentDependencies> = {}): OpenDocumentDependencies {
  return {
    resolveLabel: vi.fn(async () => ({
      ok: true,
      storyType: '活动卡面',
      index: '211',
      indexLabel: '211 卡面活动',
      chapter: 3,
    })),
    loadStory: vi.fn(async () => story()),
    checkLines: vi.fn(async (data: { sourceTalks: SourceTalk[]; loadedTalks: DstTalk[] }) => data.loadedTalks.map(talkItem => ({ ...talkItem, baseline: talkItem.text }))),
    compareText: vi.fn(async (data: { referTalks: DstTalk[]; checkTalks: DstTalk[]; editorMode: number }) => ({
      talks: data.checkTalks.map(talkItem => ({ ...talkItem, baseline: talkItem.text })),
      dstTalks: data.checkTalks.map(talkItem => ({ ...talkItem, baseline: talkItem.text })),
    })),
    isCurrent: () => true,
    ...overrides,
  }
}

function transactionOptions(
  deps: OpenDocumentDependencies,
  input: OpenTranslationResult = result(),
  editorMode: 0 | 1 | 2 = 0,
) {
  return {
    result: input,
    editorMode,
    isAndroid: false,
    deps,
  }
}

interface StoryFixture {
  name: string
  identity: string
  saveTitle: string
  chapterTitle: string
  storyType: string
  sort: string
  index: string
  indexLabel: string
  chapter: number
  source: string
  scenarioId: string
}

const supportedCurrentStories: StoryFixture[] = [
  {
    name: 'ordinary activity', identity: '1-01 Ordinary Chapter With Spaces', saveTitle: '1-01', chapterTitle: 'Ordinary Chapter With Spaces',
    storyType: '活动剧情', sort: '', index: '10', indexLabel: '10 Ordinary Activity', chapter: 0, source: 'moesekai-jp', scenarioId: 'event_501_01',
  },
  {
    name: 'World Link activity', identity: '3rd-group3-01 World Link Chapter', saveTitle: '3rd-group3-01', chapterTitle: 'World Link Chapter',
    storyType: '活动剧情', sort: '', index: '205', indexLabel: '205 World Link', chapter: 0, source: 'haruki', scenarioId: 'wl_3rd_group3_01',
  },
  {
    name: 'main story', identity: 'main-leo-01 Main Chapter With Spaces', saveTitle: 'main-leo-01', chapterTitle: 'Main Chapter With Spaces',
    storyType: '主线剧情', sort: '', index: '0', indexLabel: 'Leo/need', chapter: 0, source: 'unipjsk', scenarioId: 'main_leo_01',
  },
  {
    name: 'activity card', identity: 'event211-airi 后篇', saveTitle: 'event211-airi', chapterTitle: '后篇',
    storyType: '活动卡面', sort: '', index: '211', indexLabel: '211 Card Event', chapter: 4, source: 'haruki', scenarioId: 'event211_airi_02',
  },
  {
    name: 'collaboration card', identity: 'collabo001-saki 前篇', saveTitle: 'collabo001-saki', chapterTitle: '前篇',
    storyType: '特殊卡面', sort: '', index: '3', indexLabel: 'Collaboration', chapter: 0, source: 'sekai.best', scenarioId: 'collabo001_saki_01',
  },
  {
    name: 'birthday card', identity: 'birth2022-honami 后篇', saveTitle: 'birth2022-honami', chapterTitle: '后篇',
    storyType: '特殊卡面', sort: '', index: '2', indexLabel: 'Birthday 2022', chapter: 1, source: 'haruki', scenarioId: 'birth2022_honami_02',
  },
  {
    name: 'festival card', identity: 'fes202204-shiho 特殊篇', saveTitle: 'fes202204-shiho', chapterTitle: '特殊篇',
    storyType: '特殊卡面', sort: '', index: '1', indexLabel: 'Festival 2022 04', chapter: 2, source: 'haruki', scenarioId: 'fes202204_shiho_03',
  },
  {
    name: 'initial card', identity: 'release-airi-03 特殊篇', saveTitle: 'release-airi-03', chapterTitle: '特殊篇',
    storyType: '初始卡面', sort: '', index: '6', indexLabel: '愛莉', chapter: 8, source: 'haruki', scenarioId: 'release_airi_03_03',
  },
  {
    name: 'upgrade card', identity: 'lvelup2023-airi 后篇', saveTitle: 'lvelup2023-airi', chapterTitle: '后篇',
    storyType: '升级卡面', sort: '', index: '6', indexLabel: '愛莉', chapter: 1, source: 'haruki', scenarioId: 'levelup_airi_02',
  },
  {
    name: 'initial area talk', identity: 'areatalk-0001', saveTitle: 'areatalk-0001', chapterTitle: '',
    storyType: '初始地图对话', sort: 'character', index: '6', indexLabel: '愛莉', chapter: 0, source: 'haruki', scenarioId: 'areatalk_init_01',
  },
  {
    name: 'upgrade area talk', identity: 'areatalk-0002', saveTitle: 'areatalk-0002', chapterTitle: '',
    storyType: '升级地图对话', sort: 'character', index: '6', indexLabel: '愛莉', chapter: 0, source: 'haruki', scenarioId: 'areatalk_upgrade_01',
  },
  {
    name: 'extra area talk', identity: 'areatalk-S0001', saveTitle: 'areatalk-S0001', chapterTitle: '',
    storyType: '追加地图对话', sort: 'character', index: '8', indexLabel: 'こはね', chapter: 0, source: 'haruki', scenarioId: 'areatalk_extra_01',
  },
  {
    name: 'special story SaveTitle containing spaces', identity: 'Special Story SaveTitle With Spaces', saveTitle: 'Special Story SaveTitle With Spaces', chapterTitle: '',
    storyType: '特殊剧情', sort: '', index: '0', indexLabel: 'Special Story SaveTitle With Spaces', chapter: 0, source: 'haruki', scenarioId: 'special_01',
  },
]

const currentMismatchCases: Array<[string, { story?: OpenStoryResult; docMeta?: DocMeta }]> = [
  ['docMeta identity', { docMeta: meta({ saveTitle: 'event211-minori' }) }],
  ['docMeta chapter identity', { docMeta: meta({ chapterTitle: '后篇' }) }],
  ['scenario', { docMeta: meta({ scenarioId: 'different-scenario' }) }],
  ['source coordinate', { docMeta: meta({ source: '' }) }],
  ['story index coordinate', { docMeta: meta({ index: '' }) }],
  ['canonical index label', { docMeta: meta({ indexLabel: '' }) }],
  ['source rows', { story: story({ sourceTalks: [] }) }],
]

describe('open document transaction', () => {
  it.each(supportedCurrentStories)('reuses the exact current $name identity and preserves all coordinates', async (fixture) => {
    const resolveLabel = vi.fn(async () => { throw new Error('resolver must not run') })
    const loadStory = vi.fn(async () => { throw new Error('source must be reused') })
    const deps = dependencies({ resolveLabel, loadStory })
    const current = story({
      scenarioId: fixture.scenarioId,
      saveTitle: fixture.saveTitle,
      chapterTitle: fixture.chapterTitle,
      indexLabel: fixture.indexLabel,
      sourceTalks: [source(`${fixture.name} source`)],
    })
    const currentMeta = meta({
      saveTitle: fixture.saveTitle,
      chapterTitle: fixture.chapterTitle,
      type: fixture.storyType,
      sort: fixture.sort,
      index: fixture.index,
      indexLabel: fixture.indexLabel,
      chapter: fixture.chapter,
      source: fixture.source,
      scenarioId: fixture.scenarioId,
    })
    const commit = vi.fn()

    await expect(runOpenDocumentTransaction({
      ...transactionOptions(deps, result(`【翻译】${fixture.identity}.txt`)),
      currentStory: { story: current, docMeta: currentMeta },
    }, commit)).resolves.toBe('committed')

    expect(resolveLabel).not.toHaveBeenCalled()
    expect(loadStory).not.toHaveBeenCalled()
    expect(commit).toHaveBeenCalledWith(expect.objectContaining({
      sourceTalks: [source(`${fixture.name} source`)],
      titleOverride: fixture.chapterTitle,
      docMeta: currentMeta,
    }))
  })

  it('keeps the canonical document index label when the navigator label is stale', async () => {
    const current = story({ indexLabel: 'stale navigator label' })
    const currentMeta = meta({ indexLabel: 'canonical saved folder' })
    const commit = vi.fn()

    await expect(runOpenDocumentTransaction({
      ...transactionOptions(dependencies()),
      currentStory: { story: current, docMeta: currentMeta },
    }, commit)).resolves.toBe('committed')

    expect(commit).toHaveBeenCalledWith(expect.objectContaining({
      docMeta: expect.objectContaining({ indexLabel: 'canonical saved folder' }),
    }))
  })

  it.each([
    ['SaveTitle-only card file', story(), meta()],
    ['chapterless card document state', story({ chapterTitle: '' }), meta({ chapterTitle: '' })],
  ])('does not reuse a card with incomplete identity: %s', async (_name, current, currentMeta) => {
    const resolveLabel = vi.fn(async () => ({ ok: false, storyType: '', index: '', indexLabel: '', chapter: 0 }))
    const deps = dependencies({ resolveLabel })
    const commit = vi.fn()

    await expect(runOpenDocumentTransaction({
      ...transactionOptions(deps, result('【翻译】event211-airi.txt')),
      currentStory: { story: current, docMeta: currentMeta },
    }, commit)).rejects.toMatchObject({ code: 'resolve-failed' })

    expect(resolveLabel).toHaveBeenCalledWith('event211-airi')
    expect(deps.loadStory).not.toHaveBeenCalled()
    expect(commit).not.toHaveBeenCalled()
  })

  it('reuses an exact loaded Festival collision but fails closed on cold open and preserves the canonical index label', async () => {
    const identity = 'fes202310-meiko 前篇'
    const canonicalIndexLabel = 'Festival 2023 10'
    const current = story({
      saveTitle: 'fes202310-meiko',
      chapterTitle: '前篇',
      scenarioId: '025030_meiko01',
      // Simulate navigator state changing after the document was loaded.
      indexLabel: 'mutable navigator label',
    })
    const currentMeta = meta({
      saveTitle: 'fes202310-meiko',
      chapterTitle: '前篇',
      type: '特殊卡面',
      index: '0',
      indexLabel: canonicalIndexLabel,
      chapter: 0,
      source: 'haruki',
      scenarioId: '025030_meiko01',
    })
    const resolveLabel = vi.fn(async () => ({ ok: false, storyType: '', index: '', indexLabel: '', chapter: 0 }))
    const commit = vi.fn()

    await expect(runOpenDocumentTransaction({
      ...transactionOptions(dependencies({ resolveLabel }), result(`【翻译】${identity}.txt`)),
      currentStory: { story: current, docMeta: currentMeta },
    }, commit)).resolves.toBe('committed')
    expect(resolveLabel).not.toHaveBeenCalled()
    expect(commit).toHaveBeenCalledWith(expect.objectContaining({
      story: expect.objectContaining({ indexLabel: canonicalIndexLabel }),
      docMeta: expect.objectContaining({ indexLabel: canonicalIndexLabel }),
    }))

    await expect(runOpenDocumentTransaction(
      transactionOptions(dependencies({ resolveLabel }), result(`【翻译】${identity}.txt`)),
      vi.fn(),
    )).rejects.toMatchObject({ code: 'resolve-failed' })
    expect(resolveLabel).toHaveBeenCalledWith(identity)
  })

  it('cold-resolves a canonical card identity while preserving a translated title suffix', async () => {
    const resolveLabel = vi.fn(async (identity: string) => {
      expect(identity).toBe('event211-airi 前篇')
      return { ok: true, storyType: '活动卡面', index: '211', indexLabel: '211 卡面活动', chapter: 3, matchKind: 'exact' as const }
    })
    const commit = vi.fn()

    await expect(runOpenDocumentTransaction(
      transactionOptions(
        dependencies({ resolveLabel }),
        result('【翻译】event211-airi 前篇【标题】爱莉前篇译文.txt'),
      ),
      commit,
    )).resolves.toBe('committed')

    expect(commit).toHaveBeenCalledWith(expect.objectContaining({
      titleOverride: '爱莉前篇译文',
      docMeta: expect.objectContaining({ saveTitle: 'event211-airi', chapterTitle: '前篇' }),
    }))
  })

  it('reuses the exact current story for an arbitrary legacy translated title after catalog classification fails closed', async () => {
    const resolveLabel = vi.fn(async () => ({
      ok: false,
      storyType: '',
      index: '',
      indexLabel: '',
      chapter: 0,
      reason: 'legacy-ambiguous' as const,
    }))
    const loadStory = vi.fn(async () => { throw new Error('current story must be reused') })
    const commit = vi.fn()
    const current = story({ saveTitle: 'event211-airi', chapterTitle: '前篇', scenarioId: '007054_airi01' })
    const currentMeta = meta({ saveTitle: 'event211-airi', chapterTitle: '前篇', scenarioId: '007054_airi01' })

    await expect(runOpenDocumentTransaction({
      ...transactionOptions(dependencies({ resolveLabel, loadStory }), result('【翻译】event211-airi 爱莉前篇译文.txt')),
      currentStory: { story: current, docMeta: currentMeta },
    }, commit)).resolves.toBe('committed')

    expect(resolveLabel).toHaveBeenCalledWith('event211-airi 爱莉前篇译文')
    expect(loadStory).not.toHaveBeenCalled()
    expect(commit).toHaveBeenCalledWith(expect.objectContaining({ titleOverride: '爱莉前篇译文' }))
  })

  it.each([
    ['front', '前篇', 3],
    ['back', '后篇', 4],
    ['special', '特殊篇', 5],
  ])('cold-resolves the exact activity-card %s chapter', async (_name, chapterTitle, chapter) => {
    const identity = `event211-airi ${chapterTitle}`
    const resolveLabel = vi.fn(async (received: string) => {
      expect(received).toBe(identity)
      return { ok: true, storyType: '活动卡面', index: '211', indexLabel: '211 卡面活动', chapter }
    })
    const loadStory = vi.fn(async (request: OpenStoryRequest) => {
      expect(request).toEqual({ storyType: '活动卡面', sort: '', index: '211', chapter, source: 'haruki' })
      return story({ chapterTitle, scenarioId: `event211_airi_0${chapter - 2}` })
    })
    const commit = vi.fn()

    await expect(runOpenDocumentTransaction(
      transactionOptions(dependencies({ resolveLabel, loadStory }), result(`【翻译】${identity}.txt`)),
      commit,
    )).resolves.toBe('committed')

    expect(commit).toHaveBeenCalledWith(expect.objectContaining({
      titleOverride: chapterTitle,
      docMeta: expect.objectContaining({ chapter }),
    }))
  })

  it.each([
    { name: 'initial AddEventID=1', identity: 'areatalk-0001', storyType: '初始地图对话', index: '16', indexLabel: '奏', chapter: 10, scenarioId: 'areatalk02_129' },
    { name: 'upgrade AddEventID>1', identity: 'areatalk-0806', storyType: '升级地图对话', index: '2', indexLabel: '穂波', chapter: 34, scenarioId: 'areatalk_ev_night_01_002' },
    { name: 'extra limited', identity: 'areatalk-S0001', storyType: '追加地图对话', index: '21', indexLabel: 'リン', chapter: 45, scenarioId: 'areatalk_ev_akuno_001' },
  ])('cold-resolves the exact $name area-talk identity with deterministic coordinates', async (fixture) => {
    const resolveLabel = vi.fn(async (identity: string) => {
      expect(identity).toBe(fixture.identity)
      return {
        ok: true,
        storyType: fixture.storyType,
        index: fixture.index,
        indexLabel: fixture.indexLabel,
        chapter: fixture.chapter,
      }
    })
    const loadStory = vi.fn(async (request: OpenStoryRequest) => {
      expect(request).toEqual({
        storyType: fixture.storyType,
        sort: 'character',
        index: fixture.index,
        chapter: fixture.chapter,
        source: 'haruki',
      })
      return story({
        scenarioId: fixture.scenarioId,
        saveTitle: fixture.identity,
        chapterTitle: '',
        indexLabel: fixture.indexLabel,
      })
    })
    const commit = vi.fn()

    await expect(runOpenDocumentTransaction(
      transactionOptions(dependencies({ resolveLabel, loadStory }), result(`【翻译】${fixture.identity}.txt`)),
      commit,
    )).resolves.toBe('committed')

    expect(commit).toHaveBeenCalledWith(expect.objectContaining({
      titleOverride: '',
      docMeta: expect.objectContaining({
        type: fixture.storyType,
        sort: 'character',
        index: fixture.index,
        indexLabel: fixture.indexLabel,
        chapter: fixture.chapter,
        scenarioId: fixture.scenarioId,
      }),
    }))
  })

  it('passes and validates a complete special-story SaveTitle containing spaces', async () => {
    const identity = 'Special Story SaveTitle With Spaces'
    const resolveLabel = vi.fn(async (received: string) => {
      expect(received).toBe(identity)
      return { ok: true, storyType: '特殊剧情', index: '0', indexLabel: identity, chapter: 0 }
    })
    const loadStory = vi.fn(async () => story({ scenarioId: 'special_01', saveTitle: identity, chapterTitle: '', indexLabel: identity }))
    const commit = vi.fn()

    await expect(runOpenDocumentTransaction(
      transactionOptions(dependencies({ resolveLabel, loadStory }), result(`【翻译】${identity}.txt`)),
      commit,
    )).resolves.toBe('committed')

    expect(commit).toHaveBeenCalledWith(expect.objectContaining({
      titleOverride: '',
      docMeta: expect.objectContaining({ saveTitle: identity, chapterTitle: '' }),
    }))
  })

  it('validates the complete loaded SaveTitle plus ChapterTitle, not only the first token', async () => {
    const resolveLabel = vi.fn(async () => ({
      ok: true,
      storyType: '活动剧情',
      index: '10',
      indexLabel: '10 Activity',
      chapter: 0,
    }))
    const loadStory = vi.fn(async () => story({
      scenarioId: 'event_501_01',
      saveTitle: '1-01',
      chapterTitle: 'Wrong Chapter With Spaces',
      indexLabel: '10 Activity',
    }))
    const commit = vi.fn()

    await expect(runOpenDocumentTransaction(
      transactionOptions(dependencies({ resolveLabel, loadStory }), result('【翻译】1-01 Correct Chapter With Spaces.txt')),
      commit,
    )).rejects.toMatchObject({ code: 'source-mismatch' })

    expect(commit).not.toHaveBeenCalled()
  })

  it('uses valid embedded metadata directly and verifies its scenario before commit', async () => {
    const resolveLabel = vi.fn(async () => { throw new Error('embedded metadata must win') })
    const loadStory = vi.fn(async (request: OpenStoryRequest) => {
      expect(request).toEqual({ storyType: '活动卡面', sort: '', index: '211', chapter: 3, source: 'haruki' })
      return story()
    })
    const deps = dependencies({ resolveLabel, loadStory })
    const commit = vi.fn()
    const input = result('【翻译】.txt', {
      meta: {
        type: '活动卡面',
        sort: '',
        index: '211',
        chapter: 3,
        source: 'haruki',
        scenarioId: '007054_airi01',
        mode: 0,
      },
    })

    await expect(runOpenDocumentTransaction(transactionOptions(deps, input), commit)).resolves.toBe('committed')
    expect(resolveLabel).not.toHaveBeenCalled()
    expect(loadStory).toHaveBeenCalledOnce()
    expect(commit).toHaveBeenCalledOnce()
  })

  it('keeps greet unsupported even when an old file carries embedded metadata', async () => {
    const commit = vi.fn()
    const input = result('【翻译】.txt', {
      meta: { type: '主界面语音', sort: 'character', index: '0', chapter: 0, source: 'haruki', scenarioId: 'greet-0', mode: 0 },
    })

    await expect(runOpenDocumentTransaction(transactionOptions(dependencies(), input), commit)).rejects.toMatchObject({ code: 'invalid-metadata' })
    expect(commit).not.toHaveBeenCalled()
  })

  it('opens an old custom-title filename when valid embedded metadata and the loaded scenario match exactly', async () => {
    const resolveLabel = vi.fn(async () => ({
      ok: false,
      storyType: '',
      index: '',
      indexLabel: '',
      chapter: 0,
      reason: 'legacy-ambiguous' as const,
    }))
    const commit = vi.fn()
    const input = result('【翻译】event211-airi 爱莉前篇译文.txt', {
      meta: {
        type: '活动卡面', sort: '', index: '211', chapter: 3, source: 'haruki', scenarioId: '007054_airi01', mode: 0,
      },
    })

    await expect(runOpenDocumentTransaction(
      transactionOptions(dependencies({ resolveLabel }), input),
      commit,
    )).resolves.toBe('committed')
    expect(resolveLabel).toHaveBeenCalledWith('event211-airi 爱莉前篇译文')
    expect(commit).toHaveBeenCalledWith(expect.objectContaining({ titleOverride: '爱莉前篇译文' }))
  })

  it('rejects embedded metadata when the complete filename identity names a different chapter', async () => {
    const resolveLabel = vi.fn(async () => ({
      ok: true,
      storyType: '活动卡面',
      index: '211',
      indexLabel: '211 卡面活动',
      chapter: 4,
      matchKind: 'exact' as const,
    }))
    const commit = vi.fn()
    const input = result('【翻译】event211-airi 后篇.txt', {
      meta: {
        type: '活动卡面', sort: '', index: '211', chapter: 3, source: 'haruki', scenarioId: '007054_airi01', mode: 0,
      },
    })

    await expect(runOpenDocumentTransaction(
      transactionOptions(dependencies({ resolveLabel }), input),
      commit,
    )).rejects.toMatchObject({ code: 'source-mismatch' })
    expect(commit).not.toHaveBeenCalled()
  })

  it.each(currentMismatchCases)('does not reuse a current story with mismatching %s', async (_name, override) => {
    const resolveLabel = vi.fn(async () => ({ ok: true, storyType: '活动卡面', index: '211', indexLabel: '211 卡面活动', chapter: 3 }))
    const loadStory = vi.fn(async () => story())
    const deps = dependencies({ resolveLabel, loadStory })
    const current = override.story || story()
    const currentMeta = override.docMeta || meta()

    await expect(runOpenDocumentTransaction({
      ...transactionOptions(deps),
      currentStory: { story: current, docMeta: currentMeta },
    }, vi.fn())).resolves.toBe('committed')

    expect(resolveLabel).toHaveBeenCalledOnce()
    expect(loadStory).toHaveBeenCalledOnce()
  })

  it('fails closed when cold resolution reports an ambiguous identity', async () => {
    const deps = dependencies({
      resolveLabel: vi.fn(async () => ({ ok: false, storyType: '', index: '', indexLabel: '', chapter: 0 })),
    })
    const previous = { talks: [talk('旧文')], story: story({ scenarioId: 'old-story' }) }
    const before = JSON.stringify(previous)
    const commit = vi.fn()

    await expect(runOpenDocumentTransaction(
      transactionOptions(deps, result('【翻译】areatalk-ambiguous.txt')),
      commit,
    )).rejects.toMatchObject({ code: 'resolve-failed' })

    expect(JSON.stringify(previous)).toBe(before)
    expect(deps.loadStory).not.toHaveBeenCalled()
    expect(commit).not.toHaveBeenCalled()
  })

  it('rejects a zero-talk translation file without touching the document', async () => {
    const deps = dependencies()
    const commit = vi.fn()

    await expect(runOpenDocumentTransaction(
      transactionOptions(deps, result('【翻译】event211-airi 前篇.txt', { talks: [] })),
      commit,
    )).rejects.toMatchObject({ code: 'zero-talks' })

    expect(deps.resolveLabel).not.toHaveBeenCalled()
    expect(deps.loadStory).not.toHaveBeenCalled()
    expect(commit).not.toHaveBeenCalled()
  })

  it('rejects a zero-source-row story without touching the document', async () => {
    const deps = dependencies({ loadStory: vi.fn(async () => story({ sourceTalks: [] })) })
    const commit = vi.fn()

    await expect(runOpenDocumentTransaction(transactionOptions(deps), commit)).rejects.toMatchObject({ code: 'source-load-failed' })
    expect(commit).not.toHaveBeenCalled()
  })

  it.each([
    ['resolver failure', (deps: OpenDocumentDependencies) => ({
      ...deps,
      resolveLabel: vi.fn(async () => { throw new Error('resolver down') }),
    }), 'resolve-failed'],
    ['source-load failure', (deps: OpenDocumentDependencies) => ({
      ...deps,
      loadStory: vi.fn(async () => { throw new Error('source down') }),
    }), 'source-load-failed'],
    ['comparison failure', (deps: OpenDocumentDependencies) => ({
      ...deps,
      loadStory: vi.fn(async () => story({ saveTitle: '3rd-group3-01', chapterTitle: '标题' })),
      compareText: vi.fn(async () => { throw new Error('compare down') }),
    }), 'comparison-failed'],
  ])('preserves the previous document on %s', async (_name, makeDeps, code) => {
    const deps = makeDeps(dependencies())
    const previous = { talks: [talk('旧文')], story: story({ scenarioId: 'old-story' }) }
    const before = JSON.stringify(previous)
    const commit = vi.fn(() => { previous.talks = [talk('新文')] })

    await expect(runOpenDocumentTransaction(
      transactionOptions(deps, result('【翻译】3rd-group3-01 标题.txt'), 1),
      commit,
    )).rejects.toMatchObject({ code })

    expect(JSON.stringify(previous)).toBe(before)
    expect(commit).not.toHaveBeenCalled()
  })

  it('keeps destination-only files explicit instead of claiming an opened start page', async () => {
    const deps = dependencies()
    const commit = vi.fn()

    await expect(runOpenDocumentTransaction(
      transactionOptions(deps, result('【翻译】.txt')),
      commit,
    )).rejects.toMatchObject({ code: 'destination-only' })

    expect(commit).not.toHaveBeenCalled()
  })

  it('does not commit when pre-commit recovery cleanup fails', async () => {
    const beforeCommit = vi.fn(async () => { throw new Error('recovery offline') })
    const commit = vi.fn()

    await expect(runOpenDocumentTransaction(
      {
        ...transactionOptions(dependencies()),
        beforeCommit,
      },
      commit,
    )).rejects.toMatchObject({ code: 'commit-preparation-failed' })

    expect(beforeCommit).toHaveBeenCalledOnce()
    expect(commit).not.toHaveBeenCalled()
  })

  it('does not commit when pre-commit recovery cleanup becomes stale', async () => {
    let active = true
    let release!: () => void
    const gate = new Promise<void>(resolve => { release = resolve })
    const beforeCommit = vi.fn(async () => {
      await gate
      active = false
    })
    const commit = vi.fn()

    const pending = runOpenDocumentTransaction(
      {
        ...transactionOptions(dependencies({ isCurrent: () => active })),
        beforeCommit,
      },
      commit,
    )
    release()

    await expect(pending).resolves.toBe('stale')
    expect(commit).not.toHaveBeenCalled()
  })

  it('does not commit a stale overlapping completion after the newer open wins', async () => {
    let active = 1
    let releaseFirst!: () => void
    const firstGate = new Promise<void>(resolve => { releaseFirst = resolve })
    const firstDeps = dependencies({
      resolveLabel: vi.fn(async () => {
        await firstGate
        return { ok: true, storyType: '活动剧情', index: '1', indexLabel: '1 旧活动', chapter: 0 }
      }),
      loadStory: vi.fn(async () => story({ saveTitle: '3rd-group3-01', chapterTitle: '标题' })),
      isCurrent: () => active === 1,
    })
    const secondDeps = dependencies({
      resolveLabel: vi.fn(async () => ({ ok: true, storyType: '活动剧情', index: '2', indexLabel: '2 新活动', chapter: 0 })),
      loadStory: vi.fn(async () => story({ scenarioId: 'new-story', saveTitle: 'new-label', chapterTitle: '标题', indexLabel: '2 新活动' })),
      isCurrent: () => active === 2,
    })
    const committed: string[] = []

    const first = runOpenDocumentTransaction(
      transactionOptions(firstDeps, result('【翻译】3rd-group3-01 标题.txt')),
      candidate => { committed.push(candidate.story.scenarioId) },
    )
    active = 2
    const second = runOpenDocumentTransaction(
      transactionOptions(secondDeps, result('【翻译】new-label 标题.txt')),
      candidate => { committed.push(candidate.story.scenarioId) },
    )

    await expect(second).resolves.toBe('committed')
    releaseFirst()
    await expect(first).resolves.toBe('stale')
    expect(committed).toEqual(['new-story'])
  })
})
