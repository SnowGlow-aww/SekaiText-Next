import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const apiMock = vi.hoisted(() => ({
  recoverySave: vi.fn(),
  recoveryClear: vi.fn(),
}))

vi.mock('../api/client', () => ({ api: apiMock }))

import { clearRecovery, hasPendingRecoveryClear, saveRecovery } from './recoveryCoordinator'
import type { RecoverySaveRequestV2 } from './recovery'

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>(done => { resolve = done })
  return { promise, resolve }
}

describe('recovery write coordination', () => {
  beforeEach(() => {
    apiMock.recoverySave.mockReset()
    apiMock.recoveryClear.mockReset()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('does not let an older save recreate recovery after a queued clear', async () => {
    const releaseSave = deferred()
    const order: string[] = []
    apiMock.recoverySave.mockImplementationOnce(async () => {
      order.push('save:start')
      await releaseSave.promise
      order.push('save:end')
    })
    apiMock.recoveryClear.mockImplementationOnce(async () => { order.push('clear') })
    const request: RecoverySaveRequestV2 = {
      version: 2,
      activeMode: 0,
      modes: [],
      talks: [],
      saveN: true,
      filePath: '',
      editorMode: 0,
    }

    const save = saveRecovery(request)
    const clear = clearRecovery()
    await Promise.resolve()
    expect(order).toEqual(['save:start'])

    releaseSave.resolve()
    await Promise.all([save, clear])
    expect(order).toEqual(['save:start', 'save:end', 'clear'])
  })

  it('stages raw rows before writing backend metadata', async () => {
    const order: string[] = []
    const values = new Map<string, string>()
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => { order.push('raw'); values.set(key, value) },
      removeItem: (key: string) => values.delete(key),
    })
    apiMock.recoverySave.mockImplementationOnce(async () => { order.push('backend') })
    const request: RecoverySaveRequestV2 = {
      version: 2, activeMode: 0, modes: [], talks: [], saveN: true, filePath: '', editorMode: 0,
    }

    await saveRecovery(request)

    expect(order.slice(0, 2)).toEqual(['raw', 'backend'])
  })

  it('removes an old sidecar when staging fails before backend save', async () => {
    let stored = 'old-sidecar'
    const removeItem = vi.fn(() => { stored = '' })
    vi.stubGlobal('localStorage', {
      getItem: () => stored || null,
      setItem: () => { throw new DOMException('quota', 'QuotaExceededError') },
      removeItem,
    })
    apiMock.recoverySave.mockResolvedValueOnce(undefined)
    const request: RecoverySaveRequestV2 = {
      version: 2, activeMode: 0, modes: [], talks: [], saveN: true, filePath: '', editorMode: 0,
    }

    await saveRecovery(request)

    expect(removeItem).toHaveBeenCalled()
    expect(apiMock.recoverySave).toHaveBeenCalledOnce()
  })

  it('restores the previous committed sidecar when the backend save fails', async () => {
    const values = new Map<string, string>()
    const previous = JSON.stringify({
      version: 3,
      associationId: 'previous',
      committed: true,
      activeMode: 0,
      modes: [],
    })
    values.set('sekaitext:recovery-v2-raw', previous)
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
    })
    apiMock.recoverySave.mockRejectedValueOnce(new Error('disk full'))
    const request: RecoverySaveRequestV2 = {
      version: 2, activeMode: 0, modes: [], talks: [], saveN: true, filePath: '', editorMode: 0,
    }

    await expect(saveRecovery(request)).rejects.toThrow('disk full')

    expect(values.get('sekaitext:recovery-v2-raw')).toBe(previous)
  })

  it('keeps a failed clear pending until a later clear succeeds', async () => {
    apiMock.recoveryClear.mockRejectedValueOnce(new Error('offline'))

    await expect(clearRecovery()).rejects.toThrow('offline')
    expect(hasPendingRecoveryClear()).toBe(true)

    apiMock.recoveryClear.mockResolvedValueOnce(undefined)
    await clearRecovery()
    expect(hasPendingRecoveryClear()).toBe(false)
  })
})
