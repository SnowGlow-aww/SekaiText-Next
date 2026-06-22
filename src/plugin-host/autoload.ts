import { loadPlugin } from './loader'
import type { SekaiHost } from './types'
import { BASE_URL } from '../api/client'

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
}

// Fetch the installed-plugins listing from the backend.
export async function fetchInstalledPlugins(): Promise<InstalledPlugin[]> {
  const res = await fetch(`${BASE_URL}/plugins/list`, { cache: 'no-store' })
  if (!res.ok) throw new Error(`插件列表获取失败: HTTP ${res.status}`)
  return res.json()
}

// Absolute URL to a plugin's entry file, served by the backend.
export function pluginEntryUrl(p: InstalledPlugin): string {
  return `${BASE_URL}/plugins/${p.id}/files/${p.entry || 'entry.js'}`
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
      await loadPlugin(entry.id, pluginEntryUrl(entry), host)
    } catch (e) {
      console.error(`[plugin] 加载 ${entry.id} 失败`, e)
      host.ui.toast(`插件 ${entry.id} 加载失败`, 'error')
    }
  }
}
