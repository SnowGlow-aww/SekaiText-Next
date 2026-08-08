// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest'

const apiMock = vi.hoisted(() => ({
  translationLoadContent: vi.fn(),
}))

vi.mock('../api/client', () => ({ api: apiMock }))

import { useFileDialog } from './useFileDialog'

function dispatchPickerEvent(
  event: 'change' | 'cancel',
  file?: { name: string; text: () => Promise<string> },
) {
  return vi.spyOn(HTMLInputElement.prototype, 'click').mockImplementation(function (this: HTMLInputElement) {
    if (file) {
      Object.defineProperty(this, 'files', {
        configurable: true,
        value: [file],
      })
    }
    this.dispatchEvent(new Event(event))
  })
}

describe('browser translation picker', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    apiMock.translationLoadContent.mockReset()
  })

  it('returns null only when the picker is cancelled', async () => {
    dispatchPickerEvent('cancel')

    await expect(useFileDialog().openTranslation()).resolves.toBeNull()
    expect(apiMock.translationLoadContent).not.toHaveBeenCalled()
  })

  it('rejects when a selected file cannot be parsed instead of disguising it as cancellation', async () => {
    const parseError = new Error('translation parse failed')
    apiMock.translationLoadContent.mockRejectedValueOnce(parseError)
    dispatchPickerEvent('change', {
      name: 'broken.txt',
      text: async () => 'invalid translation',
    })

    await expect(useFileDialog().openTranslation()).rejects.toBe(parseError)
  })

  it('returns the selected filename and parsed payload on success', async () => {
    const parsed = { talks: [{ idx: 1, text: '译文' }], meta: null }
    apiMock.translationLoadContent.mockResolvedValueOnce(parsed)
    dispatchPickerEvent('change', {
      name: '【翻译】story-01 前篇.txt',
      text: async () => '译文',
    })

    await expect(useFileDialog().openTranslation()).resolves.toEqual({
      ...parsed,
      fileName: '【翻译】story-01 前篇.txt',
    })
  })
})
