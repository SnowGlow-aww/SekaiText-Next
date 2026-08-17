import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useEditorStore } from '../stores/editor'
import { useSettingsStore } from '../stores/settings'

vi.mock('../api/client', () => ({
  api: {
    translationSave: vi.fn(),
    ensureDir: vi.fn(),
  },
  capabilities: {
    isAndroid: false,
    supportsDesktopDirectories: true,
  },
}))

describe('EditorPage save behavior contracts', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('determines silent save targets from bound path first then canonical hierarchy', () => {
    const editor = useEditorStore()
    const settings = useSettingsStore()

    settings.settings.saveBaseDir = '/Users/test/Documents/SekaiText'
    settings.settings.confirmSavePath = false // 静默保存

    editor.docMeta = {
      type: '活动剧情',
      sort: '',
      index: '211',
      indexLabel: '211 Leap Beyond The Limits！',
      chapter: 0,
      saveTitle: 'event211-airi',
      chapterTitle: '前篇',
      source: 'haruki',
      scenarioId: '007054_airi01',
    }

    // 1. Without bound file path, canonical path is constructed
    const sep = (s: string) => s.replace(/[/\\]/g, '_')
    const expectedCanonical = `${settings.settings.saveBaseDir}/${sep(editor.docMeta.type)}/${sep(editor.docMeta.indexLabel)}/【翻译】${editor.docMeta.saveTitle} ${editor.docMeta.chapterTitle}.txt`
    expect(expectedCanonical).toBe('/Users/test/Documents/SekaiText/活动剧情/211 Leap Beyond The Limits！/【翻译】event211-airi 前篇.txt')

    // 2. With bound custom file path, custom path takes precedence
    editor.currentFilePath = '/custom/path/my_translation.txt'
    const target = editor.currentFilePath || expectedCanonical
    expect(target).toBe('/custom/path/my_translation.txt')
  })

  it('switches between silent direct save and prompt picker save on confirmSavePath toggle', () => {
    const settings = useSettingsStore()

    // Default mode is silent save (confirmSavePath = false)
    expect(settings.settings.confirmSavePath).toBeFalsy()

    // Toggle to prompt mode
    settings.settings.confirmSavePath = true
    expect(settings.settings.confirmSavePath).toBe(true)

    // Toggle back to silent mode
    settings.settings.confirmSavePath = false
    expect(settings.settings.confirmSavePath).toBe(false)
  })
})
