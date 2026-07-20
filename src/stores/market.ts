import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api/client'
import { fetchInstalledPlugins, pluginEntryUrl } from '../plugin-host/autoload'
import { cancelPluginLoad, loadPlugin, unloadPlugin } from '../plugin-host/loader'

declare const __APP_VERSION__: string

// One entry in the remote plugin index, annotated by the backend with local
// install state (installed / installedVersion / updateAvailable).
export interface MarketListing {
  id: string
  name: string
  version: string
  description?: string
  author?: string
  icon?: string
  minHostVersion?: string
  download: string
  sha256: string
  publisher?: string
  keyId?: string
  signatureAlgorithm?: string
  packageSignature?: string
  homepage?: string
  sequence?: number
  expiresAt?: string
  metadataSignature?: string
  installed: boolean
  installedVersion?: string
  updateAvailable: boolean
  reinstallAvailable: boolean
  signatureVerified: boolean
  signatureError?: string
}

// Drives the plugin marketplace page. Installs and updates only happen from the
// visible user action on that page; the backend authenticates every market
// package before either an interactive install or an automatic update.
export const useMarketStore = defineStore('market', () => {
  const listings = ref<MarketListing[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  const busyId = ref<string | null>(null)

  async function refresh() {
    loading.value = true
    error.value = null
    try {
      listings.value = await api.marketIndex()
    } catch (e: any) {
      error.value = e?.message || '插件市场获取失败'
      listings.value = []
    } finally {
      loading.value = false
    }
  }

  // Install (or update) a plugin by id. Returns the installed id.
  async function install(id: string): Promise<string> {
    const host = window.__SEKAI_HOST__
    if (!host) throw new Error('host bridge unavailable')
    const hostVersion = typeof __APP_VERSION__ !== 'undefined' ? __APP_VERSION__ : ''
    busyId.value = id
    try {
      cancelPluginLoad(id)
      const installed = await api.marketInstall(id, hostVersion)
      // Reload live. A load failure restores the retained payload before the
      // failed version can become the next-launch payload.
      await unloadPlugin(installed.id, host.pinia)
      const all = await fetchInstalledPlugins()
      const p = all.find((x) => x.id === installed.id)
      if (p?.enabled) {
        try {
          await loadPlugin(p, pluginEntryUrl(p), host)
        } catch (loadError) {
          try {
            await api.pluginRollback(installed.id)
            const restored = (await fetchInstalledPlugins()).find((x) => x.id === installed.id)
            if (!restored?.enabled) throw new Error('恢复版本未启用')
            await loadPlugin(restored, pluginEntryUrl(restored), host)
          } catch (rollbackError) {
            console.error(`[plugin] ${installed.id} 恢复失败，正在禁用`, rollbackError)
            try {
              await api.pluginSetEnabled(installed.id, false)
            } catch {
              // Keep the original load failure as the user-facing error.
            }
          }
          await refresh()
          throw new Error(`新版本加载失败，已尝试恢复旧版: ${loadError instanceof Error ? loadError.message : String(loadError)}`)
        }
      }
      // Reflect new state, keyed on the manifest id the backend actually
      // installed (not the requested id) — the backend guards against mismatch,
      // but this keeps the UI honest if they ever diverge.
      const row = listings.value.find((x) => x.id === installed.id)
      if (row) {
        row.installed = true
        row.installedVersion = installed.version
        row.updateAvailable = false
        row.reinstallAvailable = false
      }
      return installed.id
    } finally {
      busyId.value = null
    }
  }

  return { listings, loading, error, busyId, refresh, install }
})
