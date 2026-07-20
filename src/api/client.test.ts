import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, request } from './client'

describe('API transport', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('preserves default and caller-provided headers together', async () => {
    const fetchMock = vi.fn(async (_url: string, init?: RequestInit) => {
      const headers = new Headers(init?.headers)
      expect(headers.get('Content-Type')).toBe('application/json')
      expect(headers.get('X-Request-ID')).toBe('test-request')
      expect(init?.signal).toBeInstanceOf(AbortSignal)
      return new Response('{"ok":true}', {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    })
    vi.stubGlobal('fetch', fetchMock)

    await expect(request<{ ok: boolean }>('/test', {
      method: 'POST',
      headers: { 'X-Request-ID': 'test-request' },
      body: '{}',
    })).resolves.toEqual({ ok: true })
  })

  it('accepts a successful response with an empty body', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status: 204 })))

    await expect(request<void>('/empty', { method: 'DELETE' })).resolves.toBeUndefined()
  })

  it('exports typed HTTP errors without wrapping them as network failures', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{"error":"invalid"}', {
      status: 422,
      statusText: 'Unprocessable Content',
    })))

    const error = await request('/invalid').catch((reason) => reason)
    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ status: 422 })
    expect((error as ApiError).message).toContain('invalid')
  })

  it('honors a caller AbortSignal without disguising cancellation as a network error', async () => {
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init?: RequestInit) => new Promise((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => reject(init.signal?.reason), { once: true })
    })))
    const controller = new AbortController()
    const reason = new DOMException('cancelled', 'AbortError')
    const pending = request('/cancelled', { signal: controller.signal })

    controller.abort(reason)

    await expect(pending).rejects.toBe(reason)
  })

  it('aborts requests that exceed the transport timeout', async () => {
    vi.useFakeTimers()
    let signal: AbortSignal | null = null
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init?: RequestInit) => new Promise((_resolve, reject) => {
      signal = init?.signal ?? null
      signal?.addEventListener('abort', () => reject(signal?.reason), { once: true })
    })))
    const pending = request('/slow', { timeoutMs: 25 } as RequestInit & { timeoutMs: number })
    const rejection = pending.catch((reason) => reason)

    await vi.advanceTimersByTimeAsync(25)

    expect((signal as AbortSignal | null)?.aborted).toBe(true)
    await expect(rejection).resolves.toMatchObject({ name: 'TimeoutError' })
  })
})
