import { isPluginLoadCancelled, loadPlugin, samePluginLoadIdentity } from './loader'
import type { SekaiHost } from './types'
import { api, BASE_URL } from '../api/client'
import { fetchPluginJson } from './fetch'

// A plugin entry as returned by the Go backend's /plugins/list. The backend
// serves plugins from the writable app-data dir ({dataDir}/plugins/<id>/), so
// they can be installed/enabled/uninstalled at runtime — unlike the read-only
// frontend bundle. First-party plugins are seeded on backend startup.
export interface InstalledPlugin {
  id: string
  name: string
  version: string
  description?: string
  author?: string
  entry?: string // file within the plugin dir, defaults to entry.js
  minHostVersion?: string
  icon?: string
  enabled: boolean
  local: boolean
  provenance?: PluginProvenance
  loadToken: string
}

export interface PluginProvenance {
  source: 'verified-market'
  publisher: string
  keyId: string
  sha256: string
  indexVersion: 2 | 3
  sequence?: number
}

export interface PluginUpdateResult {
  id: string
  name: string
  fromVersion?: string
  toVersion?: string
  error?: string
}

export interface PluginAutoUpdateSummary {
  updated: PluginUpdateResult[]
  failed: PluginUpdateResult[]
}

let startupPromise: Promise<PluginAutoUpdateSummary | null> | null = null

// Fetch the installed-plugins listing from the backend.
export async function fetchInstalledPlugins(signal?: AbortSignal): Promise<InstalledPlugin[]> {
  const { response, data } = await fetchPluginJson<InstalledPlugin[]>(
    `${BASE_URL}/plugins/list`,
    { cache: 'no-store', signal },
  )
  if (!response.ok) throw new Error(`插件列表获取失败: HTTP ${response.status}`)
  return data ?? []
}

// Absolute URL to a plugin's entry file, served by the backend.
export function pluginEntryUrl(p: InstalledPlugin): string {
  return `${BASE_URL}/plugins/${p.id}/files/${p.entry || 'entry.js'}?token=${encodeURIComponent(p.loadToken)}`
}

export async function autoLoadPlugins(host: SekaiHost): Promise<void> {
  let index: InstalledPlugin[] = []
  try {
    index = await fetchInstalledPlugins()
  } catch {
    return // backend unreachable or no plugins — that's fine
  }

  for (const entry of index) {
    if (!entry.enabled) continue
    try {
      await loadPlugin(entry, pluginEntryUrl(entry), host)
    } catch (e) {
      if (isPluginLoadCancelled(e)) continue
      console.error(`[plugin] 加载 ${entry.id} 失败`, e)
      try {
        // A concurrent replacement invalidates the stale load deliberately. It
        // must never use that failure to roll the newly installed payload back.
        const current = (await fetchInstalledPlugins()).find((plugin) => plugin.id === entry.id)
        if (!current || !samePluginLoadIdentity(current, entry)) continue
        await api.pluginRollback(entry.id)
        const restored = (await fetchInstalledPlugins()).find((plugin) => plugin.id === entry.id)
        if (!restored || !restored.enabled) throw new Error('rollback payload is not enabled')
        await loadPlugin(restored, pluginEntryUrl(restored), host)
        host.ui.toast(`插件 ${entry.id} 新版本加载失败，已恢复上一个可用版本`, 'warn', 6000)
        continue
      } catch (rollbackError) {
        console.error(`[plugin] ${entry.id} 自动恢复失败，已禁用`, rollbackError)
      }
      try {
        await api.pluginSetEnabled(entry.id, false)
      } catch {
        // The backend may be disappearing during shutdown; the original load
        // failure is still surfaced below.
      }
      host.ui.toast(`插件 ${entry.id} 加载失败，已禁用`, 'error')
    }
  }
}

// Boot updates complete before any installed code executes. The identity check
// above is still retained as a second fence for manual replacement races.
export function startPluginStartup(host: SekaiHost, hostVersion: string): Promise<PluginAutoUpdateSummary | null> {
  if (startupPromise) return startupPromise
  startupPromise = (async () => {
    let summary: PluginAutoUpdateSummary | null = null
    try {
      summary = await api.marketAutoUpdate(hostVersion)
    } catch {
      // Offline/signing failures do not block loading the current installation.
    }
    await autoLoadPlugins(host)
    return summary
  })()
  return startupPromise
}

export function pluginStartupResult(): Promise<PluginAutoUpdateSummary | null> {
  return startupPromise ?? Promise.resolve(null)
}
