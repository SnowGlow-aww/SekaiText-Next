import type { Router } from 'vue-router'
import type { Pinia } from 'pinia'
import { usePluginRegistry } from './registry'
import type { PluginModule, SekaiHost } from './types'

// Keep references to loaded modules so teardown() can be called on unload.
const loadedModules = new Map<string, PluginModule>()

// Ids whose loadPlugin() is mid-flight (awaiting fetch/import/setup). Reserved
// synchronously so a second concurrent load of the same id short-circuits instead
// of running setup() twice (which would duplicate timers/listeners and orphan the
// first module's teardown reference).
const loading = new Set<string>()

// Remove every route a plugin registered. registerRoute() (bridge.ts) lets a
// plugin supply its own route.name and only falls back to the namespaced
// `plugin:<id>:<path>` when none is given; the registry tracks paths, not names —
// so reconstructing the namespaced name would miss any plugin-named route. Resolve
// the live route record for each tracked path instead and remove it by its real name.
function removePluginRoutes(router: Router, paths: string[]): void {
  if (paths.length === 0) return
  const wanted = new Set(paths)
  for (const record of router.getRoutes()) {
    if (record.name != null && wanted.has(record.path)) {
      router.removeRoute(record.name)
    }
  }
}

// Load a plugin from a URL (served by the Go sidecar at
// http://localhost:9800/plugins/<id>/entry.js, or any dev URL). The cache-bust
// query lets a reinstall pick up a new build. Vite must NOT pre-bundle this
// import — see the /* @vite-ignore */ below.
export async function loadPlugin(
  pluginId: string,
  entryUrl: string,
  host: SekaiHost,
): Promise<void> {
  const registry = usePluginRegistry(host.pinia)
  // Reserve the id synchronously (before the first await) so a second concurrent
  // load of the same id short-circuits here instead of running setup() twice —
  // which would duplicate side effects (timers/listeners) and overwrite
  // loadedModules, orphaning the first module's teardown().
  if (registry.isLoaded(pluginId) || loading.has(pluginId)) return
  loading.add(pluginId)
  try {
    // Fetch the entry as text and import it as a blob URL. This bypasses Vite's
    // dev-server module transform (which 500s on public/ files imported with a
    // ?import query) and works identically in the packaged app. First-party
    // plugins are self-contained ESM that take all host deps via the bridge
    // global, so they have no bare imports to resolve.
    const res = await fetch(entryUrl, { cache: 'no-store' })
    if (!res.ok) throw new Error(`插件 ${pluginId} 入口获取失败: HTTP ${res.status}`)
    const code = await res.text()
    const blobUrl = URL.createObjectURL(new Blob([code], { type: 'text/javascript' }))
    let mod: PluginModule
    try {
      mod = (await import(/* @vite-ignore */ blobUrl)) as PluginModule
    } finally {
      URL.revokeObjectURL(blobUrl)
    }
    if (typeof mod.setup !== 'function') {
      throw new Error(`插件 ${pluginId} 缺少 setup() 导出`)
    }
    // A throwing setup() must not abort the host's load chain or leave a
    // half-registered plugin. Roll back anything it managed to contribute.
    try {
      await mod.setup(host)
    } catch (e) {
      console.error(`[plugin] ${pluginId} setup() 失败，已回滚`, e)
      removePluginRoutes(host.router, registry.routePaths(pluginId))
      registry.forget(pluginId)
      throw e
    }
    loadedModules.set(pluginId, mod)
    registry.markLoaded(pluginId)
  } finally {
    loading.delete(pluginId)
  }
}

// Reverse a plugin: call its teardown(), remove its routes, drop registry state.
export async function unloadPlugin(
  pluginId: string,
  router: Router,
  pinia: Pinia,
): Promise<void> {
  const registry = usePluginRegistry(pinia)
  if (!registry.isLoaded(pluginId)) return

  const mod = loadedModules.get(pluginId)
  try {
    await mod?.teardown?.()
  } catch (e) {
    console.error(`[plugin] ${pluginId} teardown 失败`, e)
  }

  // Remove routes the plugin added (resolved by tracked path, removed by real name).
  removePluginRoutes(router, registry.routePaths(pluginId))

  loadedModules.delete(pluginId)
  registry.forget(pluginId)
}
