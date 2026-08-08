// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import RecoveryDialog from './RecoveryDialog.vue'
import { useEditorStore } from '../stores/editor'
import { useStoryStore } from '../stores/story'
import type { DstTalk } from '../types/translation'

const apiMock = vi.hoisted(() => ({
  recoveryLoad: vi.fn(),
  translationLoadContent: vi.fn(),
  storyLoad: vi.fn(),
  checkLines: vi.fn(),
  compareText: vi.fn(),
  storySorts: vi.fn(),
  storyIndex: vi.fn(),
  storyChapter: vi.fn(),
}))
const recoveryMock = vi.hoisted(() => ({ clear: vi.fn(async () => {}) }))
const undoMock = vi.hoisted(() => ({ clear: vi.fn() }))
const toastMock = vi.hoisted(() => ({ show: vi.fn() }))

vi.mock('../api/client', () => ({ api: apiMock }))
vi.mock('../editor/recoveryCoordinator', () => ({ clearRecovery: recoveryMock.clear }))
vi.mock('../composables/useUndo', () => ({ clearUndoHistory: undoMock.clear }))
vi.mock('../composables/useToast', () => ({ useToast: () => toastMock }))

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

function recoveryResult() {
  return {
    exists: true,
    version: 2,
    activeMode: 0,
    modes: [{
      content: '愛莉：恢复译文',
      talks: [talk('恢复译文')],
      dstTalks: [talk('恢复译文')],
      referTalks: [],
      filePath: '/drafts/【翻译】event211-airi 前篇【标题】恢复标题.txt',
      editorMode: 0,
      hasUnsavedChanges: true,
      sourceTalks: [{ speaker: '愛莉', text: '恢复原文', charIndex: 0 }],
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
  }
}

function loadedStory() {
  return {
    scenarioId: '007054_airi01',
    sourceTalks: [{ speaker: '愛莉', text: '恢复原文', charIndex: 0 }],
    saveTitle: 'event211-airi',
    chapterTitle: '前篇',
    indexLabel: '211 卡面活动',
  }
}

async function settle() {
  for (let i = 0; i < 5; i++) {
    await Promise.resolve()
    await new Promise(resolve => setTimeout(resolve, 0))
    await nextTick()
  }
}

async function mountDialog() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const editor = useEditorStore()
  editor.setTalks([talk('旧文档')], [talk('旧文档')], [])
  editor.setSourceTalks([{ speaker: '愛莉', text: '旧原文', charIndex: 0 }])
  editor.docMeta = {
    saveTitle: 'old-save', chapterTitle: '旧章', type: '活动剧情', sort: '',
    index: '1', indexLabel: '1 旧活动', chapter: 0, source: 'haruki', scenarioId: 'old-scenario',
  }
  editor.markUnsaved()
  const story = useStoryStore()
  story.scenarioId = 'old-scenario'
  story.sourceTalks = [{ speaker: '愛莉', text: '旧原文', charIndex: 0 }]
  story.saveTitle = 'old-save'
  story.chapterTitle = '旧章'

  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp(RecoveryDialog)
  app.use(pinia)
  app.mount(host)
  await nextTick()
  const restore = Array.from(host.querySelectorAll('button'))
    .find(button => button.textContent?.includes('恢复')) as HTMLButtonElement
  return { app, editor, story, restore }
}

describe('RecoveryDialog candidate transaction', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    apiMock.recoveryLoad.mockResolvedValue(recoveryResult())
    apiMock.translationLoadContent.mockResolvedValue({ talks: [talk('恢复译文')] })
    apiMock.storyLoad.mockResolvedValue(loadedStory())
    apiMock.checkLines.mockImplementation(async ({ loadedTalks }: { loadedTalks: DstTalk[] }) => loadedTalks)
    apiMock.compareText.mockImplementation(async ({ checkTalks }: { checkTalks: DstTalk[] }) => ({ talks: checkTalks, dstTalks: checkTalks }))
    apiMock.storySorts.mockResolvedValue([])
    apiMock.storyIndex.mockResolvedValue([{ label: '211 卡面活动', value: '211' }])
    apiMock.storyChapter.mockResolvedValue([{ number: 3, label: '前篇' }])
    recoveryMock.clear.mockResolvedValue(undefined)
  })

  afterEach(() => { document.body.innerHTML = '' })

  it('preserves the live document and undo stack when recovery cleanup fails', async () => {
    recoveryMock.clear.mockRejectedValueOnce(new Error('recovery offline'))
    const mounted = await mountDialog()

    mounted.restore.click()
    await settle()

    expect(mounted.editor.talks[0].text).toBe('旧文档')
    expect(mounted.editor.sourceTalks[0].text).toBe('旧原文')
    expect(mounted.editor.docMeta?.scenarioId).toBe('old-scenario')
    expect(mounted.story.scenarioId).toBe('old-scenario')
    expect(undoMock.clear).not.toHaveBeenCalled()
    expect(toastMock.show).toHaveBeenCalledWith(expect.stringContaining('恢复失败'), 'error')
    mounted.app.unmount()
  })

  it('commits all recovered editor, source, identity, title, path and undo state together', async () => {
    const mounted = await mountDialog()

    mounted.restore.click()
    await settle()

    expect(mounted.editor.talks[0].text).toBe('恢复译文')
    expect(mounted.editor.sourceTalks).toEqual(loadedStory().sourceTalks)
    expect(mounted.editor.docMeta?.scenarioId).toBe('007054_airi01')
    expect(mounted.editor.titleOverride).toBe('恢复标题')
    expect(mounted.editor.currentFilePath).toContain('event211-airi 前篇')
    expect(mounted.editor.recoveryPending).toBe(true)
    expect(mounted.story.scenarioId).toBe('007054_airi01')
    expect(undoMock.clear).toHaveBeenCalledOnce()
    expect(recoveryMock.clear).toHaveBeenCalledOnce()
    mounted.app.unmount()
  })
})
