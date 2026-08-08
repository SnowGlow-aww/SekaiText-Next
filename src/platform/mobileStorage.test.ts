// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { RecoverySaveRequestV2 } from '../editor/recovery'
import type { DstTalk } from '../types/translation'

const RAW_RECOVERY_KEY = 'sekaitext:recovery-v2-raw'
const MOBILE_RECOVERY_KEY = 'sekaitext:android-recovery-v2'

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

function recoveryRequest(text = '移动端草稿'): RecoverySaveRequestV2 {
  const draft = talk(text)
  return {
    version: 2,
    activeMode: 0,
    modes: [{
      talks: [draft],
      editorTalks: [draft],
      referTalks: [],
      filePath: '',
      editorMode: 0,
      titleOverride: '',
      hasUnsavedChanges: true,
      sourceTalks: [],
      docMeta: null,
    }],
    talks: [draft],
    saveN: true,
    filePath: '',
    editorMode: 0,
  }
}

describe('Android recovery storage', () => {
  beforeEach(() => {
    vi.resetModules()
    localStorage.clear()
    Object.defineProperty(window, '__SEKAI_PLATFORM__', {
      configurable: true,
      value: 'android',
    })
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
    localStorage.clear()
    Object.defineProperty(window, '__SEKAI_PLATFORM__', {
      configurable: true,
      value: undefined,
    })
  })

  it('stores one authoritative full graph instead of duplicating the desktop raw sidecar', async () => {
    localStorage.setItem(RAW_RECOVERY_KEY, '{"legacy":true}')
    const { saveRecovery } = await import('../editor/recoveryCoordinator')

    await saveRecovery(recoveryRequest())

    expect(localStorage.getItem(RAW_RECOVERY_KEY)).toBeNull()
    const stored = JSON.parse(localStorage.getItem(MOBILE_RECOVERY_KEY) || 'null')
    expect(stored).toEqual(expect.objectContaining({
      exists: true,
      version: 2,
      modes: [expect.objectContaining({
        talks: [expect.objectContaining({ text: '移动端草稿' })],
        dstTalks: [expect.objectContaining({ text: '移动端草稿' })],
      })],
    }))
    expect(localStorage.length).toBe(1)
  })

  it('selects the newest snapshot when cross-store cleanup was interrupted', async () => {
    const { selectLatestMobileRecovery } = await import('./mobileStorage')
    const older = {
      exists: true,
      version: 2 as const,
      activeMode: 0 as const,
      modes: [],
      savedAt: '2026-07-31T10:00:00.000Z',
    }
    const newer = {
      exists: true,
      version: 2 as const,
      activeMode: 0 as const,
      modes: [],
      savedAt: '2026-07-31T10:00:01.000Z',
    }

    expect(selectLatestMobileRecovery(older, newer)).toBe(newer)
    expect(selectLatestMobileRecovery(newer, older)).toBe(newer)
    expect(selectLatestMobileRecovery({ exists: false }, newer)).toBe(newer)
  })

  it('propagates quota failures from the authoritative mobile recovery write', async () => {
    const originalSetItem = Storage.prototype.setItem
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key, value) {
      if (key === MOBILE_RECOVERY_KEY) throw new DOMException('quota', 'QuotaExceededError')
      return originalSetItem.call(this, key, value)
    })
    const { saveRecovery } = await import('../editor/recoveryCoordinator')

    await expect(saveRecovery(recoveryRequest())).rejects.toMatchObject({ name: 'QuotaExceededError' })
    expect(localStorage.getItem(MOBILE_RECOVERY_KEY)).toBeNull()
    expect(localStorage.getItem(RAW_RECOVERY_KEY)).toBeNull()
  })

  it('propagates IndexedDB clear failures and leaves recovery cleanup pending for retry', async () => {
    const deleteError = new Error('IndexedDB delete failed')
    const database = {
      objectStoreNames: { contains: () => true },
      transaction: vi.fn(() => {
        const request: Record<string, any> = { error: deleteError }
        const transaction: Record<string, any> = {
          error: deleteError,
          objectStore: () => ({
            delete: () => {
              queueMicrotask(() => request.onerror?.())
              return request
            },
          }),
        }
        return transaction
      }),
      close: vi.fn(),
    }
    const indexedDBMock = {
      open: vi.fn(() => {
        const request: Record<string, any> = { result: database, error: null }
        queueMicrotask(() => request.onsuccess?.())
        return request
      }),
    }
    vi.stubGlobal('indexedDB', indexedDBMock)
    localStorage.setItem(MOBILE_RECOVERY_KEY, JSON.stringify({ exists: true, savedAt: new Date().toISOString() }))

    const { clearRecovery, hasPendingRecoveryClear } = await import('../editor/recoveryCoordinator')
    await expect(clearRecovery()).rejects.toBe(deleteError)

    expect(localStorage.getItem(MOBILE_RECOVERY_KEY)).toBeNull()
    expect(hasPendingRecoveryClear()).toBe(true)
    expect(database.close).toHaveBeenCalledOnce()
  })

  it('removes an older snapshot when a larger replacement exhausts quota', async () => {
    const { saveRecovery } = await import('../editor/recoveryCoordinator')
    await saveRecovery(recoveryRequest('旧草稿'))
    expect(localStorage.getItem(MOBILE_RECOVERY_KEY)).toContain('旧草稿')

    const originalSetItem = Storage.prototype.setItem
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key, value) {
      if (key === MOBILE_RECOVERY_KEY) throw new DOMException('quota', 'QuotaExceededError')
      return originalSetItem.call(this, key, value)
    })

    await expect(saveRecovery(recoveryRequest('无法写入的新草稿')))
      .rejects.toMatchObject({ name: 'QuotaExceededError' })
    expect(localStorage.getItem(MOBILE_RECOVERY_KEY)).toBeNull()

    const { loadMobileRecovery } = await import('./mobileStorage')
    await expect(loadMobileRecovery()).resolves.toEqual({ exists: false })
  })
})
