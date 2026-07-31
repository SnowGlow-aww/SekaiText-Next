// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useEditorStore } from '../stores/editor'
import { clearRecovery } from '../editor/recoveryCoordinator'
import { useAutoSave } from './useAutoSave'
import type { DstTalk } from '../types/translation'

const apiMock = vi.hoisted(() => ({
  recoverySave: vi.fn(),
  recoveryClear: vi.fn(),
}))

vi.mock('../api/client', () => ({ api: apiMock }))

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

describe('recovery autosave', () => {
  beforeEach(async () => {
    setActivePinia(createPinia())
    apiMock.recoverySave.mockReset().mockResolvedValue(undefined)
    apiMock.recoveryClear.mockReset().mockResolvedValue(undefined)
    await clearRecovery()
    vi.clearAllMocks()
    vi.useFakeTimers()
  })

  afterEach(() => vi.useRealTimers())

  it('flushes focused editor text before capturing recovery mode state', async () => {
    const editor = useEditorStore()
    editor.setTalks([talk('old')], [talk('old')], [])
    editor.markUnsaved()
    const autoSave = useAutoSave(1000, async () => {
      editor.talks[0].text = 'focused text'
      editor.dstTalks[0].text = 'focused text'
    })

    await autoSave.syncNow()

    expect(apiMock.recoverySave).toHaveBeenCalledWith(expect.objectContaining({
      modes: [expect.objectContaining({
        talks: [expect.objectContaining({ text: 'focused text' })],
        editorTalks: [expect.objectContaining({ text: 'focused text' })],
      })],
    }))
  })

  it('retries a failed clear on the interval even after the document is clean', async () => {
    apiMock.recoveryClear
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(undefined)
    const autoSave = useAutoSave(1000)

    await expect(autoSave.syncNow()).rejects.toThrow('offline')
    expect(useEditorStore().hasAnyUnsaved()).toBe(false)

    autoSave.start()
    await vi.advanceTimersByTimeAsync(1000)
    autoSave.stop()

    expect(apiMock.recoveryClear).toHaveBeenCalledTimes(2)
  })

  it('can persist the first mutation immediately and coalesce a trailing burst', async () => {
    const editor = useEditorStore()
    editor.setTalks([talk('first')], [talk('first')], [])
    const autoSave = useAutoSave(30_000)
    autoSave.start()

    editor.markUnsaved()
    autoSave.schedule(1_000)
    await vi.advanceTimersByTimeAsync(0)
    expect(apiMock.recoverySave).toHaveBeenCalledTimes(1)

    editor.talks[0].text = 'second'
    editor.dstTalks[0].text = 'second'
    editor.markUnsaved()
    autoSave.schedule(1_000)
    editor.talks[0].text = 'third'
    editor.dstTalks[0].text = 'third'
    editor.markUnsaved()
    autoSave.schedule(1_000)

    await vi.advanceTimersByTimeAsync(999)
    expect(apiMock.recoverySave).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(apiMock.recoverySave).toHaveBeenCalledTimes(2)
    expect(apiMock.recoverySave).toHaveBeenLastCalledWith(expect.objectContaining({
      modes: [expect.objectContaining({
        talks: [expect.objectContaining({ text: 'third' })],
      })],
    }))
    autoSave.stop()
  })

  it('reports background persistence failures without repeating the same error toast', async () => {
    const editor = useEditorStore()
    editor.setTalks([talk('draft')], [talk('draft')], [])
    editor.markUnsaved()
    apiMock.recoverySave
      .mockRejectedValueOnce(new DOMException('quota', 'QuotaExceededError'))
      .mockRejectedValueOnce(new DOMException('quota', 'QuotaExceededError'))
      .mockResolvedValueOnce(undefined)
    const onError = vi.fn()
    const autoSave = useAutoSave(30_000, undefined, onError)
    autoSave.start()

    autoSave.schedule(100)
    await vi.advanceTimersByTimeAsync(0)
    expect(onError).toHaveBeenCalledOnce()
    expect(autoSave.lastError.value?.name).toBe('QuotaExceededError')

    await vi.advanceTimersByTimeAsync(100)
    autoSave.schedule(100)
    await vi.advanceTimersByTimeAsync(0)
    expect(onError).toHaveBeenCalledOnce()

    await vi.advanceTimersByTimeAsync(100)
    autoSave.schedule(100)
    await vi.advanceTimersByTimeAsync(0)
    expect(autoSave.lastError.value).toBeNull()
    autoSave.stop()
  })

  it('writes a final dirty snapshot when stopped before the first interval', async () => {
    const editor = useEditorStore()
    editor.setTalks([talk('draft')], [talk('draft')], [])
    editor.markUnsaved()
    const materialize = vi.fn(async () => {
      editor.talks[0].text = 'focused edit'
      editor.dstTalks[0].text = 'focused edit'
    })
    const autoSave = useAutoSave(30_000)
    autoSave.start()

    await autoSave.stopAndSync(materialize)
    await vi.advanceTimersByTimeAsync(30_000)

    expect(materialize).toHaveBeenCalledOnce()
    expect(apiMock.recoverySave).toHaveBeenCalledOnce()
    expect(apiMock.recoverySave).toHaveBeenCalledWith(expect.objectContaining({
      modes: [expect.objectContaining({
        talks: [expect.objectContaining({ text: 'focused edit' })],
      })],
    }))
  })
})
