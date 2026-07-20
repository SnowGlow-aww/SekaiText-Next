import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchPluginResource, fetchPluginText } from './fetch'

describe('plugin fetch bounds', () => {
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('aborts a plugin request when its timeout expires', async () => {
    vi.useFakeTimers()
    vi.stubGlobal('fetch', vi.fn((_input, init?: RequestInit) => new Promise((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => reject(init.signal?.reason), { once: true })
    })))

    const request = fetchPluginResource('/entry.js', {}, 25)
    const rejection = expect(request).rejects.toMatchObject({ name: 'TimeoutError' })
    await vi.advanceTimersByTimeAsync(25)

    await rejection
  })

  it('forwards caller cancellation to the fetch', async () => {
    const controller = new AbortController()
    vi.stubGlobal('fetch', vi.fn((_input, init?: RequestInit) => new Promise((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => reject(init.signal?.reason), { once: true })
    })))

    const request = fetchPluginResource('/plugins/list', { signal: controller.signal }, 1000)
    controller.abort(new DOMException('uninstalled', 'AbortError'))

    await expect(request).rejects.toMatchObject({ name: 'AbortError' })
  })

  it('keeps the timeout active while reading an entry body', async () => {
    vi.useFakeTimers()
    vi.stubGlobal('fetch', vi.fn(async (_input, init?: RequestInit) => ({
      ok: true,
      text: () => new Promise((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () => reject(init.signal?.reason), { once: true })
      }),
    })))

    const request = fetchPluginText('/entry.js', {}, 25)
    const rejection = expect(request).rejects.toMatchObject({ name: 'TimeoutError' })
    await vi.advanceTimersByTimeAsync(25)
    await rejection
  })
})
