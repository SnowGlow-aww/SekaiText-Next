// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import EditorWorkspace from './EditorWorkspace.vue'
import { useEditorStore } from '../../stores/editor'
import { useStoryStore } from '../../stores/story'
import { clearUndoHistory } from '../../composables/useUndo'
import type { DstTalk } from '../../types/translation'

const apiMock = vi.hoisted(() => ({
  changeText: vi.fn(),
  characterIconUrl: vi.fn(() => ''),
  glossaryEntries: vi.fn(async () => ({ items: [], total: 0 })),
}))

vi.mock('../../api/client', () => ({ api: apiMock }))

function talk(text: string): DstTalk {
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

describe('EditorWorkspace focused edit materialization', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    clearUndoHistory()
    apiMock.changeText.mockReset().mockImplementation(async data => ({
      talks: JSON.parse(JSON.stringify(data.talks)),
      dstTalks: JSON.parse(JSON.stringify(data.dstTalks)),
    }))
  })

  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('waits for IME composition to end before flushing the focused row', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    const editor = useEditorStore()
    const story = useStoryStore()
    editor.setTalks([talk('old')], [talk('old')], [])
    story.sourceTalks = [{ speaker: '场景', text: 'source', charIndex: 0 }]
    const container = document.createElement('div')
    document.body.appendChild(container)
    const app = createApp(EditorWorkspace)
    app.use(pinia)
    const workspace = app.mount(container) as unknown as { flushPendingEdit: () => Promise<void> }
    await nextTick()
    const editable = container.querySelector<HTMLElement>('[contenteditable="true"][data-gidx="0"]')!
    Object.defineProperty(editable, 'isContentEditable', { configurable: true, value: true })
    editable.focus()
    editable.dispatchEvent(new Event('compositionstart', { bubbles: true }))
    editable.textContent = 'partial'
    let flushed = false

    const flushing = workspace.flushPendingEdit().then(() => { flushed = true })
    await Promise.resolve()
    expect(flushed).toBe(false)
    expect(apiMock.changeText).not.toHaveBeenCalled()

    editable.textContent = '完成'
    editable.dispatchEvent(new Event('compositionend', { bubbles: true }))
    await flushing

    expect(editor.talks[0].text).toBe('完成')
    expect(editor.dstTalks[0].text).toBe('完成')
    expect(apiMock.changeText).toHaveBeenCalledWith(expect.objectContaining({ text: '完成' }))
    app.unmount()
  })

  it('materializes active IME text without waiting when the route deactivates', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    const editor = useEditorStore()
    const story = useStoryStore()
    editor.setTalks([talk('old')], [talk('old')], [])
    story.sourceTalks = [{ speaker: '场景', text: 'source', charIndex: 0 }]
    const container = document.createElement('div')
    document.body.appendChild(container)
    const app = createApp(EditorWorkspace)
    app.use(pinia)
    const workspace = app.mount(container) as unknown as {
      flushPendingEditForDeactivation: () => Promise<void>
    }
    await nextTick()
    const editable = container.querySelector<HTMLElement>('[contenteditable="true"][data-gidx="0"]')!
    Object.defineProperty(editable, 'isContentEditable', { configurable: true, value: true })
    editable.focus()
    editable.dispatchEvent(new Event('compositionstart', { bubbles: true }))
    editable.textContent = '离开前输入'

    await workspace.flushPendingEditForDeactivation()

    expect(editor.talks[0].text).toBe('离开前输入')
    expect(editor.dstTalks[0].text).toBe('离开前输入')
    expect(apiMock.changeText).toHaveBeenCalledWith(expect.objectContaining({ text: '离开前输入' }))
    app.unmount()
  })
})
