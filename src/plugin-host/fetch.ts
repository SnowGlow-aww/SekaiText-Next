export const PLUGIN_FETCH_TIMEOUT_MS = 15_000

async function fetchAndRead<T>(
  input: RequestInfo | URL,
  init: RequestInit,
  timeoutMs: number,
  read: (response: Response) => Promise<T> | T,
): Promise<T> {
  const callerSignal = init.signal
  const controller = new AbortController()
  const forwardAbort = () => controller.abort(callerSignal?.reason)
  let timeout: ReturnType<typeof setTimeout> | undefined

  if (callerSignal) {
    if (callerSignal.aborted) forwardAbort()
    else callerSignal.addEventListener('abort', forwardAbort, { once: true })
  }
  if (timeoutMs > 0) {
    timeout = setTimeout(() => {
      controller.abort(new DOMException(`Plugin request timed out after ${timeoutMs}ms`, 'TimeoutError'))
    }, timeoutMs)
  }

  try {
    if (controller.signal.aborted) throw controller.signal.reason
    const response = await fetch(input, { ...init, signal: controller.signal })
    return await read(response)
  } finally {
    if (timeout) clearTimeout(timeout)
    callerSignal?.removeEventListener('abort', forwardAbort)
  }
}

export function fetchPluginResource(
  input: RequestInfo | URL,
  init: RequestInit = {},
  timeoutMs = PLUGIN_FETCH_TIMEOUT_MS,
): Promise<Response> {
  return fetchAndRead(input, init, timeoutMs, response => response)
}

export function fetchPluginText(
  input: RequestInfo | URL,
  init: RequestInit = {},
  timeoutMs = PLUGIN_FETCH_TIMEOUT_MS,
): Promise<{ response: Response; data: string }> {
  return fetchAndRead(input, init, timeoutMs, async response => ({
    response,
    data: response.ok ? await response.text() : '',
  }))
}

export function fetchPluginJson<T>(
  input: RequestInfo | URL,
  init: RequestInit = {},
  timeoutMs = PLUGIN_FETCH_TIMEOUT_MS,
): Promise<{ response: Response; data?: T }> {
  return fetchAndRead(input, init, timeoutMs, async response => ({
    response,
    data: response.ok ? await response.json() as T : undefined,
  }))
}
