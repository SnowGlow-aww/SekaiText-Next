// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import StoryNavigator from './StoryNavigator.vue'
import { useEditorStore } from '../../stores/editor'
import { useStoryStore } from '../../stores/story'

const apiMock = vi.hoisted(() => ({
  storyTypes: vi.fn(async () => ['活动剧情']),
  storySorts: vi.fn(async () => []),
  storyIndex: vi.fn(async () => []),
  storyChapter: vi.fn(async () => []),
  storyLoad: vi.fn(),
  translationCreate: vi.fn(),
  update: vi.fn(),
  updateProgress: vi.fn(),
  openSaveDir: vi.fn(),
}))
const confirmMock = vi.hoisted(() => ({ confirm: vi.fn(async () => true) }))
const downloadMock = vi.hoisted(() => ({
  add: vi.fn(() => 'story-load-task'),
  start: vi.fn(),
  done: vi.fn(),
  fail: vi.fn(),
}))
const toastMock = vi.hoisted(() => ({ show: vi.fn() }))
const recoveryMock = vi.hoisted(() => ({ clear: vi.fn(async () => {}) }))
const undoMock = vi.hoisted(() => ({ clear: vi.fn() }))

vi.mock('../../api/client', () => ({ api: apiMock }))
vi.mock('../../composables/useConfirm', () => ({ useConfirm: () => confirmMock }))
vi.mock('../../composables/useDownloadFloat', () => ({ useDownloadFloat: () => downloadMock }))
vi.mock('../../composables/useToast', () => ({ useToast: () => toastMock }))
vi.mock('../../composables/useDebugLog', () => ({ useDebugLog: () => ({ log: vi.fn() }) }))
vi.mock('../../editor/recoveryCoordinator', () => ({ clearRecovery: recoveryMock.clear }))
vi.mock('../../composables/useUndo', () => ({ clearUndoHistory: undoMock.clear }))
vi.mock('../ui/SkSelect.vue', () => ({
  default: {
    props: ['modelValue', 'options', 'placeholder', 'disabled'],
    emits: ['update:model-value'],
    template: '<select :disabled="disabled"><option>{{ placeholder }}</option></select>',
  },
}))

function talk(text: string) {
  return {
    idx: 1,
    speaker: '瑞希',
    text,
    start: true,
    end: true,
    checked: true,
    save: true,
    dstidx: 0,
  }
}

function loadedStory() {
  return {
    scenarioId: 'new-scenario',
    sourceTalks: [{ speaker: '瑞希', text: '新原文', charIndex: 0 }],
    saveTitle: 'new-save',
    chapterTitle: '前篇',
    indexLabel: '1 新活动',
  }
}

async function settle() {
  for (let i = 0; i < 4; i++) {
    await Promise.resolve()
    await new Promise(resolve => setTimeout(resolve, 0))
    await nextTick()
  }
}

async function mountWithSelection() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const editor = useEditorStore()
  editor.setTalks([talk('旧文档')], [talk('旧文档')], [])
  editor.setSourceTalks([{ speaker: '瑞希', text: '旧原文', charIndex: 0 }])
  editor.markUnsaved()

  const story = useStoryStore()
  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp(StoryNavigator, { autoPull: false })
  app.use(pinia)
  app.mount(host)
  await nextTick()

  story.selectedType = '活动剧情'
  await settle()
  story.selectedChapter = 0
  await nextTick()

  const load = Array.from(host.querySelectorAll('button'))
    .find(button => button.textContent?.includes('载入')) as HTMLButtonElement | undefined
  if (!load || load.disabled) throw new Error('load button is unavailable')
  return { app, editor, story, load }
}

describe('StoryNavigator story-load candidate transaction', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    apiMock.storyLoad.mockResolvedValue(loadedStory())
    apiMock.translationCreate.mockResolvedValue([talk('')])
    confirmMock.confirm.mockResolvedValue(true)
    recoveryMock.clear.mockResolvedValue(undefined)
  })

  afterEach(() => { document.body.innerHTML = '' })

  it('preserves the current document when source rows or scenarioId are empty', async () => {
    apiMock.storyLoad.mockResolvedValue({ ...loadedStory(), scenarioId: '', sourceTalks: [] })
    const mounted = await mountWithSelection()

    mounted.load.click()
    await settle()

    expect(mounted.editor.talks[0].text).toBe('旧文档')
    expect(mounted.editor.sourceTalks[0].text).toBe('旧原文')
    expect(downloadMock.done).not.toHaveBeenCalled()
    expect(downloadMock.fail).toHaveBeenCalledWith('story-load-task', expect.stringContaining('未载入'))
    expect(undoMock.clear).not.toHaveBeenCalled()
    mounted.app.unmount()
  })

  it('preserves the current document when template creation returns zero rows', async () => {
    apiMock.translationCreate.mockResolvedValue([])
    const mounted = await mountWithSelection()

    mounted.load.click()
    await settle()

    expect(mounted.editor.talks[0].text).toBe('旧文档')
    expect(mounted.editor.sourceTalks[0].text).toBe('旧原文')
    expect(mounted.story.scenarioId).toBe('')
    expect(downloadMock.done).not.toHaveBeenCalled()
    expect(downloadMock.fail).toHaveBeenCalledWith('story-load-task', expect.stringContaining('未载入'))
    expect(undoMock.clear).not.toHaveBeenCalled()
    mounted.app.unmount()
  })

  it('preserves the current document and undo stack when recovery cleanup fails', async () => {
    recoveryMock.clear.mockRejectedValueOnce(new Error('recovery offline'))
    const mounted = await mountWithSelection()

    mounted.load.click()
    await settle()

    expect(mounted.editor.talks[0].text).toBe('旧文档')
    expect(mounted.editor.sourceTalks[0].text).toBe('旧原文')
    expect(mounted.story.scenarioId).toBe('')
    expect(undoMock.clear).not.toHaveBeenCalled()
    expect(downloadMock.done).not.toHaveBeenCalled()
    expect(downloadMock.fail).toHaveBeenCalledWith('story-load-task', expect.stringContaining('未载入'))
    mounted.app.unmount()
  })

  it('commits translation, source, identity, metadata and undo only after the candidate is usable', async () => {
    const mounted = await mountWithSelection()

    mounted.load.click()
    await settle()

    expect(mounted.editor.talks).toHaveLength(1)
    expect(mounted.editor.sourceTalks).toEqual(loadedStory().sourceTalks)
    expect(mounted.editor.docMeta?.scenarioId).toBe('new-scenario')
    expect(mounted.story.scenarioId).toBe('new-scenario')
    expect(undoMock.clear).toHaveBeenCalledOnce()
    expect(recoveryMock.clear).toHaveBeenCalledOnce()
    expect(downloadMock.done).toHaveBeenCalledWith('story-load-task', '已载入 1 行')
    expect(downloadMock.fail).not.toHaveBeenCalled()
    mounted.app.unmount()
  })
})
