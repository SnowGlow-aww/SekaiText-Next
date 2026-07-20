import type { Pinia } from 'pinia'
import { api, BASE_URL } from '../api/client'
import { usePluginRegistry } from './registry'
import { scopeHostForPlugin, type ScopedPluginHost } from './bridge'
import type { PluginModule, SekaiHost } from './types'
import type { InstalledPlugin, PluginProvenance } from './autoload'
import { fetchPluginJson, fetchPluginText } from './fetch'

const loadedModules = new Map<string, PluginModule>()
const loadedScopes = new Map<string, ScopedPluginHost>()
const generations = new Map<string, number>()
const loadControllers = new Map<string, AbortController>()
const loadPromises = new Map<string, Promise<void>>()
const PLUGIN_SETUP_TIMEOUT_MS = 15_000

function generation(pluginId: string): number {
  return generations.get(pluginId) ?? 0
}

function cancellationError(pluginId: string): DOMException {
  return new DOMException(`插件 ${pluginId} 加载已取消`, 'AbortError')
}

export function isPluginLoadCancelled(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError'
}

function assertCurrent(pluginId: string, expectedGeneration: number, signal?: AbortSignal): void {
  if (signal?.aborted || generation(pluginId) !== expectedGeneration) {
    throw signal?.reason ?? cancellationError(pluginId)
  }
}

// Invalidating is synchronous: disable, uninstall, and replacement operations
// can fence a fetch/import/setup already in progress before awaiting any backend
// mutation.
export function cancelPluginLoad(pluginId: string): void {
  generations.set(pluginId, generation(pluginId) + 1)
  loadControllers.get(pluginId)?.abort(cancellationError(pluginId))
  loadControllers.delete(pluginId)
}

function sameProvenance(left?: PluginProvenance, right?: PluginProvenance): boolean {
  if (!left || !right) return !left && !right
  return left.source === right.source &&
    left.publisher === right.publisher &&
    left.keyId === right.keyId &&
    left.sha256 === right.sha256 &&
    left.indexVersion === right.indexVersion &&
    (left.sequence ?? 0) === (right.sequence ?? 0)
}

export function samePluginLoadIdentity(current: InstalledPlugin, expected: InstalledPlugin): boolean {
  return current.id === expected.id &&
    current.enabled &&
    current.version === expected.version &&
    current.loadToken === expected.loadToken &&
    sameProvenance(current.provenance, expected.provenance)
}

// Re-read authoritative backend state after the executable module has imported
// and immediately before setup. The load token binds version and provenance.
export async function verifyPluginLoadIdentity(
  expected: InstalledPlugin,
  signal?: AbortSignal,
): Promise<void> {
  const { response, data } = await fetchPluginJson<InstalledPlugin[]>(
    `${BASE_URL}/plugins/list`,
    { cache: 'no-store', signal },
  )
  if (!response.ok) throw new Error(`插件列表复核失败: HTTP ${response.status}`)
  const current = data?.find((plugin) => plugin.id === expected.id)
  if (!current || !samePluginLoadIdentity(current, expected)) {
    throw new Error(`插件 ${expected.id} 在加载期间已被禁用、替换或失去可信来源`)
  }
}

function removePluginStyles(pluginId: string): void {
  if (typeof document === 'undefined') return
  document.querySelectorAll(`[data-sekaitext-plugin="${pluginId}"]`).forEach((node) => node.remove())
  // Compatibility cleanup for bundles produced before the data attribute was added.
  document.getElementById(`sekai-plugin-${pluginId}-css`)?.remove()
  document.getElementById(`sekai-plugin-${pluginId.replaceAll('-', '')}-css`)?.remove()
}

async function rollbackContributions(
  pluginId: string,
  mod: PluginModule,
  scope: ScopedPluginHost,
  pinia: Pinia,
): Promise<void> {
  // Revoke and remove host-owned contributions before awaiting plugin code. A
  // setup() that resolves after cancellation can no longer register stale UI.
  scope.revoke()
  const registry = usePluginRegistry(pinia)
  registry.disposeRoutes(pluginId)
  registry.forget(pluginId)
  removePluginStyles(pluginId)
  try {
    if (mod.teardown) {
      await waitForSetup(
        Promise.resolve().then(() => mod.teardown!()),
        `${pluginId} teardown`,
        undefined,
        5_000,
      )
    }
  } catch (error) {
    console.error(`[plugin] ${pluginId} teardown 失败`, error)
  }
}

function waitForSetup(
  setup: Promise<void>,
  pluginId: string,
  signal?: AbortSignal,
  timeoutMs = PLUGIN_SETUP_TIMEOUT_MS,
  onInterrupt?: () => void,
): Promise<void> {
  return new Promise((resolve, reject) => {
    let settled = false
    const finish = (callback: () => void) => {
      if (settled) return
      settled = true
      if (timeout) clearTimeout(timeout)
      signal?.removeEventListener('abort', onAbort)
      callback()
    }
    const onAbort = () => finish(() => {
      onInterrupt?.()
      reject(signal?.reason ?? cancellationError(pluginId))
    })
    const timeout = timeoutMs > 0
      ? setTimeout(() => finish(() => {
        onInterrupt?.()
        reject(new DOMException(
          `插件 ${pluginId} setup() 超时（${timeoutMs}ms）`,
          'TimeoutError',
        ))
      }), timeoutMs)
      : undefined

    if (signal?.aborted) {
      onAbort()
      return
    }
    signal?.addEventListener('abort', onAbort, { once: true })
    setup.then(
      () => finish(resolve),
      error => finish(() => reject(error)),
    )
  })
}

// Activate an already-imported module. The optional generation predicate lets a
// disable/uninstall that occurs during async setup roll back partial effects.
export async function activatePluginModule(
  pluginId: string,
  mod: PluginModule,
  host: SekaiHost,
  isCurrent: () => boolean = () => true,
  signal?: AbortSignal,
  setupTimeoutMs = PLUGIN_SETUP_TIMEOUT_MS,
): Promise<void> {
  const registry = usePluginRegistry(host.pinia)
  const scope = scopeHostForPlugin(host, pluginId)
  try {
    if (!isCurrent()) throw cancellationError(pluginId)
    const setup = Promise.resolve().then(() => scope.runSetup(() => mod.setup(scope.host)))
    await waitForSetup(setup, pluginId, signal, setupTimeoutMs, scope.revoke)
    if (!isCurrent()) throw cancellationError(pluginId)
  } catch (error) {
    console.error(`[plugin] ${pluginId} setup() 失败，正在回滚`, error)
    await rollbackContributions(pluginId, mod, scope, host.pinia)
    throw error
  }
  loadedModules.set(pluginId, mod)
  loadedScopes.set(pluginId, scope)
  registry.markLoaded(pluginId)
}

async function performPluginLoad(
  expected: InstalledPlugin,
  entryUrl: string,
  host: SekaiHost,
): Promise<void> {
  const pluginId = expected.id
  const expectedGeneration = generation(pluginId)
  const controller = new AbortController()
  loadControllers.set(pluginId, controller)
  let activated = false
  try {
    const { response, data: code } = await fetchPluginText(
      entryUrl,
      { cache: 'no-store', signal: controller.signal },
    )
    if (!response.ok) throw new Error(`插件 ${pluginId} 入口获取失败: HTTP ${response.status}`)
    assertCurrent(pluginId, expectedGeneration, controller.signal)

    // Check once before module evaluation (top-level plugin code is executable),
    // then again below immediately before setup to cover an async import race.
    await verifyPluginLoadIdentity(expected, controller.signal)
    assertCurrent(pluginId, expectedGeneration, controller.signal)

    const blobUrl = URL.createObjectURL(new Blob([code], { type: 'text/javascript' }))
    let mod: PluginModule
    try {
      mod = (await import(/* @vite-ignore */ blobUrl)) as PluginModule
    } finally {
      URL.revokeObjectURL(blobUrl)
    }
    assertCurrent(pluginId, expectedGeneration, controller.signal)
    if (typeof mod.setup !== 'function') {
      removePluginStyles(pluginId)
      throw new Error(`插件 ${pluginId} 缺少 setup() 导出`)
    }

    await verifyPluginLoadIdentity(expected, controller.signal)
    assertCurrent(pluginId, expectedGeneration, controller.signal)
    await activatePluginModule(
      pluginId,
      mod,
      host,
      () => !controller.signal.aborted && generation(pluginId) === expectedGeneration,
      controller.signal,
    )
    activated = true
    await api.pluginMarkGood(pluginId, expected.version, expected.loadToken, expected.provenance, controller.signal)
    assertCurrent(pluginId, expectedGeneration, controller.signal)
  } catch (error) {
    if (activated && usePluginRegistry(host.pinia).isLoaded(pluginId)) {
      const mod = loadedModules.get(pluginId)
      const scope = loadedScopes.get(pluginId)
      if (mod && scope) await rollbackContributions(pluginId, mod, scope, host.pinia)
      loadedModules.delete(pluginId)
      loadedScopes.delete(pluginId)
    } else if (!activated) {
      removePluginStyles(pluginId)
    }
    throw error
  } finally {
    if (loadControllers.get(pluginId) === controller) loadControllers.delete(pluginId)
  }
}

export function loadPlugin(
  expected: InstalledPlugin,
  entryUrl: string,
  host: SekaiHost,
): Promise<void> {
  const registry = usePluginRegistry(host.pinia)
  if (registry.isLoaded(expected.id)) return Promise.resolve()
  const pending = loadPromises.get(expected.id)
  if (pending) return pending
  const task = performPluginLoad(expected, entryUrl, host)
  loadPromises.set(expected.id, task)
  void task.finally(() => {
    if (loadPromises.get(expected.id) === task) loadPromises.delete(expected.id)
  }).catch(() => {})
  return task
}

export async function unloadPlugin(pluginId: string, pinia: Pinia): Promise<void> {
  const pending = loadPromises.get(pluginId)
  cancelPluginLoad(pluginId)
  if (pending) {
    try { await pending } catch { /* cancellation/load failure cleanup runs in the task */ }
  }
  const registry = usePluginRegistry(pinia)
  const mod = loadedModules.get(pluginId)
  const scope = loadedScopes.get(pluginId)
  if (mod && scope) {
    await rollbackContributions(pluginId, mod, scope, pinia)
  } else {
    registry.disposeRoutes(pluginId)
    registry.forget(pluginId)
    removePluginStyles(pluginId)
  }
  loadedModules.delete(pluginId)
  loadedScopes.delete(pluginId)
}
