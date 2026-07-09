import * as VueRuntime from 'vue'
import type { Router, RouteRecordRaw } from 'vue-router'
import type { Pinia } from 'pinia'
import { useStoryStore } from '../stores/story'
import { useAppStore } from '../stores/app'
import { useSettingsStore } from '../stores/settings'
import { useLive2dDockStore } from '../stores/live2dDock'
import { api, ORIGIN } from '../api/client'
import { useToast } from '../composables/useToast'
import { usePluginRegistry } from './registry'
import { useTour } from '../onboarding/useTour'
import StoryNavigator from '../components/navigation/StoryNavigator.vue'
import type { SekaiHost, PluginSidebarItem, PluginSettingsSection, PluginDockPanel } from './types'

declare const __APP_VERSION__: string

// Builds the host bridge and installs it on window.__SEKAI_HOST__ exactly once.
// Must run AFTER app.use(pinia) and app.use(router) in main.ts, since stores and
// route registration need the active instances. The bridge hands plugins the
// host's own Vue/router/pinia so a dynamically-imported plugin shares the same
// singletons (no second Vue instance).
export function installHostBridge(router: Router, pinia: Pinia): SekaiHost {
  if (window.__SEKAI_HOST__) return window.__SEKAI_HOST__

  const { show: toast } = useToast()

  const registerRoute = (pluginId: string, route: RouteRecordRaw) => {
    const registry = usePluginRegistry(pinia)
    if (typeof pluginId !== 'string' || !pluginId) {
      console.error('[plugin] registerRoute(pluginId, route): pluginId 必须是字符串', pluginId)
      return
    }
    if (!route || typeof route !== 'object' || !route.path) {
      console.error(`[plugin] ${pluginId} registerRoute: route 无效`, route)
      return
    }
    // name defaults to a namespaced value so removeRoute can target it.
    if (!route.name) route.name = `plugin:${pluginId}:${route.path}`
    if (!router.hasRoute(route.name)) router.addRoute(route)
    registry.trackRoute(pluginId, route.path)
  }

  const registerSidebarItem = (pluginId: string, item: PluginSidebarItem) => {
    const registry = usePluginRegistry(pinia)
    if (typeof pluginId !== 'string' || !pluginId) {
      console.error('[plugin] registerSidebarItem(pluginId, item): pluginId 必须是字符串。请使用 host.registerSidebarItem(PLUGIN_ID, {...})', pluginId)
      return
    }
    if (!item || typeof item !== 'object' || typeof item.id !== 'string' || !item.to) {
      console.error(`[plugin] ${pluginId} registerSidebarItem: item 无效（需要 {id, label, to}）`, item)
      return
    }
    registry.addSidebarItem(pluginId, item)
  }

  const registerSettingsSection = (pluginId: string, section: PluginSettingsSection) => {
    const registry = usePluginRegistry(pinia)
    if (typeof pluginId !== 'string' || !pluginId) {
      console.error('[plugin] registerSettingsSection(pluginId, section): pluginId 必须是字符串', pluginId)
      return
    }
    if (!section || typeof section !== 'object' || typeof section.id !== 'string' || !section.component) {
      console.error(`[plugin] ${pluginId} registerSettingsSection: section 无效（需要 {id, component}）`, section)
      return
    }
    registry.addSettingsSection(pluginId, section)
  }

  const registerDockPanel = (pluginId: string, panel: PluginDockPanel) => {
    const registry = usePluginRegistry(pinia)
    if (typeof pluginId !== 'string' || !pluginId) {
      console.error('[plugin] registerDockPanel(pluginId, panel): pluginId 必须是字符串', pluginId)
      return
    }
    if (!panel || typeof panel !== 'object' || typeof panel.id !== 'string' || !panel.component) {
      console.error(`[plugin] ${pluginId} registerDockPanel: panel 无效（需要 {id, component}）`, panel)
      return
    }
    registry.addDockPanel(pluginId, panel)
  }

  const host: SekaiHost = {
    version: typeof __APP_VERSION__ !== 'undefined' ? __APP_VERSION__ : '0.0.0',
    // Backend origin (= window.__SEKAI_ORIGIN__) so plugins read it from the host
    // instead of hard-coding localhost:9800.
    backendOrigin: ORIGIN,
    vue: VueRuntime,
    router,
    pinia,
    stores: {
      story: () => useStoryStore(pinia),
      app: () => useAppStore(pinia),
      settings: () => useSettingsStore(pinia),
      live2dDock: () => useLive2dDockStore(pinia),
    },
    api,
    ui: { toast },
    // Reuse the host's own Tauri file dialog (lazy import, same as the editor's
    // file-open paths) so plugins can pick absolute paths instead of forcing the
    // user to type them.
    dialog: {
      open: async (options?: any) => {
        const { open } = await import('@tauri-apps/plugin-dialog')
        return open(options) as Promise<string | string[] | null>
      },
      save: async (options?: any) => {
        const { save } = await import('@tauri-apps/plugin-dialog')
        return save(options)
      },
    },
    components: { StoryNavigator },
    // 导览：插件可自带分步引导（首次进入自动弹一次用 startTourOnce，id 会按
    // 插件命名空间隔离并持久化到 settings.seenTours；强制重放用 startTour）。
    startTour: (pluginId: string, def: { id: string; steps: any[] }) => {
      useTour().start({ id: `plugin:${pluginId}:${def.id}`, steps: def.steps })
    },
    startTourOnce: (pluginId: string, def: { id: string; steps: any[] }) => {
      useTour().startOnce({ id: `plugin:${pluginId}:${def.id}`, steps: def.steps })
    },
    registerRoute,
    registerSidebarItem,
    registerSettingsSection,
    registerDockPanel,
  }

  window.__SEKAI_HOST__ = host
  return host
}
