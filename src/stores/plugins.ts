import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api/client'
import { fetchInstalledPlugins, pluginEntryUrl, type InstalledPlugin } from '../plugin-host/autoload'
import { loadPlugin, unloadPlugin } from '../plugin-host/loader'

declare const __APP_VERSION__: string

// Drives the settings "插件" management panel. Enable/disable toggles persist to
// the backend AND apply live via the plugin-host loader (load/unloadPlugin), so
// a plugin's routes + sidebar + settings contributions appear/disappear without
// a restart. Uninstall removes the plugin dir on the backend (first-party
// plugins are protected server-side).
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

  async function setEnabled(id: string, enabled: boolean) {
    const host = window.__SEKAI_HOST__
    if (!host) throw new Error('host bridge unavailable')
    busyId.value = id
    try {
      await api.pluginSetEnabled(id, enabled)
      const p = list.value.find((x) => x.id === id)
      if (enabled) {
        if (p) await loadPlugin(id, pluginEntryUrl(p), host)
      } else {
        await unloadPlugin(id, host.router, host.pinia)
      }
      if (p) p.enabled = enabled
    } finally {
      busyId.value = null
    }
  }

  async function uninstall(id: string) {
    const host = window.__SEKAI_HOST__
    if (!host) throw new Error('host bridge unavailable')
    busyId.value = id
    try {
      // Unload from the running app first, then delete on the backend.
      await unloadPlugin(id, host.router, host.pinia)
      await api.pluginUninstall(id)
      list.value = list.value.filter((x) => x.id !== id)
    } finally {
      busyId.value = null
    }
  }

  // Install a .sekplugin from a local file path. Replaces any existing plugin
  // of the same id (reloading it live if it was already running). Returns the
  // installed plugin's id.
  async function installFromPath(srcPath: string): Promise<string> {
    const host = window.__SEKAI_HOST__
    if (!host) throw new Error('host bridge unavailable')
    const hostVersion = typeof __APP_VERSION__ !== 'undefined' ? __APP_VERSION__ : ''
    const installed = await api.pluginInstall(srcPath, hostVersion)
    // If a plugin with this id was already loaded, unload it so the new payload
    // takes effect on the next load.
    await unloadPlugin(installed.id, host.router, host.pinia)
    await refresh()
    const p = list.value.find((x) => x.id === installed.id)
    if (p && p.enabled) await loadPlugin(p.id, pluginEntryUrl(p), host)
    return installed.id
  }

  return { list, loading, busyId, refresh, setEnabled, uninstall, installFromPath }
})
