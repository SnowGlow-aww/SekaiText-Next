import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api/client'
import { fetchInstalledPlugins, pluginEntryUrl } from '../plugin-host/autoload'
import { loadPlugin, unloadPlugin } from '../plugin-host/loader'

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
  homepage?: string
  installed: boolean
  installedVersion?: string
  updateAvailable: boolean
}

// Drives the plugin marketplace page: fetch the remote index, install/update by
// id (backend downloads the .sekplugin → installs), applying the result live so
// a freshly-installed plugin's routes/sidebar appear without a restart.
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
      const installed = await api.marketInstall(id, hostVersion)
      // Reload live: unload any prior payload, then load the new one if enabled.
      await unloadPlugin(installed.id, host.router, host.pinia)
      const all = await fetchInstalledPlugins()
      const p = all.find((x) => x.id === installed.id)
      if (p && p.enabled) await loadPlugin(p.id, pluginEntryUrl(p), host)
      // Reflect new state, keyed on the manifest id the backend actually
      // installed (not the requested id) — the backend guards against mismatch,
      // but this keeps the UI honest if they ever diverge.
      const row = listings.value.find((x) => x.id === installed.id)
      if (row) {
        row.installed = true
        row.installedVersion = installed.version
        row.updateAvailable = false
      }
      return installed.id
    } finally {
      busyId.value = null
    }
  }

  return { listings, loading, error, busyId, refresh, install }
})
