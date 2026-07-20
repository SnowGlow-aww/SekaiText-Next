import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api/client'
import { fetchInstalledPlugins, pluginEntryUrl, type InstalledPlugin } from '../plugin-host/autoload'
import { cancelPluginLoad, loadPlugin, unloadPlugin } from '../plugin-host/loader'

declare const __APP_VERSION__: string

// Drives the settings "插件" management panel. Enable/disable toggles persist to
// the backend AND apply live via the plugin-host loader (load/unloadPlugin), so
// a plugin's routes + sidebar + settings contributions appear/disappear without
// a restart. Uninstall removes the plugin dir on the backend (the id is validated
// server-side to a single safe path segment; there is no first-party/built-in
// protection, so any installed plugin can be uninstalled).
export const usePluginsStore = defineStore('plugins', () => {
  const list = ref<InstalledPlugin[]>([])
  const loading = ref(false)
  const busyId = ref<string | null>(null)

  async function refresh() {
    loading.value = true
    try {
      list.value = await fetchInstalledPlugins()
    } finally {
      loading.value = false
    }
  }

  async function reconcileRuntime(
    id: string,
    fallback: InstalledPlugin | undefined,
    host: NonNullable<Window['__SEKAI_HOST__']>,
  ) {
    let current = fallback
    try {
      const authoritative = await fetchInstalledPlugins()
      list.value = authoritative
      current = authoritative.find(plugin => plugin.id === id)
    } catch {
      // A rejected mutation normally leaves backend state unchanged. If listing
      // is also unavailable, the previous identity is the best reload candidate.
    }
    if (current?.enabled) await loadPlugin(current, pluginEntryUrl(current), host)
    else await unloadPlugin(id, host.pinia)
  }

  async function setEnabled(id: string, enabled: boolean, approveLocal = false) {
    const host = window.__SEKAI_HOST__
    if (!host) throw new Error('host bridge unavailable')
    const previous = list.value.find(plugin => plugin.id === id)
    busyId.value = id
    try {
      // Fence any fetch/import/setup synchronously before changing backend state.
      cancelPluginLoad(id)
      if (!enabled) await unloadPlugin(id, host.pinia)
      await api.pluginSetEnabled(id, enabled, approveLocal)
      await refresh()
      const p = list.value.find((x) => x.id === id)
      try {
        if (enabled) {
          if (!p?.enabled) throw new Error('插件启用状态复核失败')
          await loadPlugin(p, pluginEntryUrl(p), host)
        }
      } catch (e) {
        if (enabled) {
          try {
            await api.pluginRollback(id)
            await refresh()
            const restored = list.value.find((plugin) => plugin.id === id)
            if (!restored?.enabled) throw new Error('恢复版本未启用')
            await loadPlugin(restored, pluginEntryUrl(restored), host)
            host.ui.toast(`插件 ${id} 加载失败，已恢复上一个可用版本`, 'warn', 6000)
            return
          } catch (rollbackError) {
            console.error(`[plugin] ${id} 恢复失败，正在禁用`, rollbackError)
            try {
              await api.pluginSetEnabled(id, false)
            } catch {
              // refresh below still attempts to reconcile with backend state
            }
          }
        }
        await refresh()
        throw e
      }
    } catch (error) {
      try {
        await reconcileRuntime(id, previous, host)
      } catch (reconcileError) {
        console.error(`[plugin] ${id} 后端操作失败后的运行时恢复失败`, reconcileError)
      }
      throw error
    } finally {
      busyId.value = null
    }
  }

  async function uninstall(id: string) {
    const host = window.__SEKAI_HOST__
    if (!host) throw new Error('host bridge unavailable')
    const previous = list.value.find(plugin => plugin.id === id)
    busyId.value = id
    try {
      // Unload from the running app first, then delete on the backend.
      await unloadPlugin(id, host.pinia)
      await api.pluginUninstall(id)
      list.value = list.value.filter((x) => x.id !== id)
    } catch (error) {
      try {
        await reconcileRuntime(id, previous, host)
      } catch (reconcileError) {
        console.error(`[plugin] ${id} 卸载失败后的运行时恢复失败`, reconcileError)
      }
      throw error
    } finally {
      busyId.value = null
    }
  }

  // Install a .sekplugin from a local file path. The backend retains the old
  // payload for rollback and always marks the local payload disabled; activation
  // requires a separate full-permission risk confirmation in Settings.
  async function installFromPath(srcPath: string): Promise<string> {
    const host = window.__SEKAI_HOST__
    if (!host) throw new Error('host bridge unavailable')
    const hostVersion = typeof __APP_VERSION__ !== 'undefined' ? __APP_VERSION__ : ''
    const installed = await api.pluginInstall(srcPath, hostVersion)
    // If a plugin with this id was already loaded, unload it so the new payload
    // takes effect on the next load.
    await unloadPlugin(installed.id, host.pinia)
    await refresh()
    const p = list.value.find((x) => x.id === installed.id)
    if (p?.enabled) await loadPlugin(p, pluginEntryUrl(p), host)
    return installed.id
  }

  return { list, loading, busyId, refresh, setEnabled, uninstall, installFromPath }
})
