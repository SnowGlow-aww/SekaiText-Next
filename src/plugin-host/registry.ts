import { defineStore } from 'pinia'
import { ref, computed, markRaw } from 'vue'
import type { PluginSidebarItem, PluginSettingsSection, PluginDockPanel } from './types'

// Tracks plugin contributions at runtime so the sidebar/router reflect them
// reactively, and so unloading a plugin can reverse exactly what it added.
export const usePluginRegistry = defineStore('plugin-registry', () => {
  // pluginId -> contributed sidebar items
  const sidebarByPlugin = ref<Record<string, PluginSidebarItem[]>>({})
  // pluginId -> contributed settings-page sections
  const settingsByPlugin = ref<Record<string, PluginSettingsSection[]>>({})
  // pluginId -> contributed dock panels (e.g. the Live2D player)
  const dockPanelsByPlugin = ref<Record<string, PluginDockPanel[]>>({})
  // pluginId -> route paths it added (for removeRoute on unload)
  const routesByPlugin = ref<Record<string, string[]>>({})
  // ids of plugins whose setup() has run
  const loaded = ref<string[]>([])

  // Flattened, ordered sidebar items across all plugins. filter(Boolean) is a
  // defensive guard so a malformed contribution can never inject a null/undefined
  // into the v-for that renders the sidebar (which would white-screen the host).
  // Stamp each item with its owning pluginId so the host can render a globally
  // unique v-for key (`${pluginId}:${id}`) — two different plugins may pick the
  // same item id, which would otherwise produce duplicate keys and corrupt the patch.
  const sidebarItems = computed<PluginSidebarItem[]>(() =>
    Object.entries(sidebarByPlugin.value)
      .flatMap(([pluginId, items]) =>
        (items ?? []).filter((i) => !!i && typeof i === 'object').map((i) => ({ ...i, pluginId })),
      )
      .sort((a, b) => (a.order ?? 100) - (b.order ?? 100)),
  )

  // Flattened, ordered settings sections across all plugins.
  const settingsSections = computed<PluginSettingsSection[]>(() =>
    Object.entries(settingsByPlugin.value)
      .flatMap(([pluginId, sections]) =>
        (sections ?? []).filter((s) => !!s && typeof s === 'object').map((s) => ({ ...s, pluginId })),
      )
      .sort((a, b) => (a.order ?? 100) - (b.order ?? 100)),
  )

  // Flattened dock panels across all plugins (same defensive stamping/filtering).
  const dockPanels = computed<PluginDockPanel[]>(() =>
    Object.entries(dockPanelsByPlugin.value)
      .flatMap(([pluginId, panels]) => (panels ?? []).map((p) => ({ ...p, pluginId })))
      .filter((p) => !!p && typeof p === 'object' && !!p.component),
  )

  function addSidebarItem(pluginId: string, item: PluginSidebarItem) {
    const list = sidebarByPlugin.value[pluginId] ?? []
    if (!list.some((i) => i.id === item.id)) {
      sidebarByPlugin.value = { ...sidebarByPlugin.value, [pluginId]: [...list, item] }
    }
  }

  // markRaw the component so pinia/Vue never makes the component definition
  // reactive (would warn and waste cycles proxying an inert object).
  function addSettingsSection(pluginId: string, section: PluginSettingsSection) {
    const list = settingsByPlugin.value[pluginId] ?? []
    if (!list.some((s) => s.id === section.id)) {
      const safe = { ...section, component: markRaw(section.component) }
      settingsByPlugin.value = { ...settingsByPlugin.value, [pluginId]: [...list, safe] }
    }
  }

  function addDockPanel(pluginId: string, panel: PluginDockPanel) {
    const list = dockPanelsByPlugin.value[pluginId] ?? []
    if (!list.some((p) => p.id === panel.id)) {
      const safe = { ...panel, component: markRaw(panel.component) }
      dockPanelsByPlugin.value = { ...dockPanelsByPlugin.value, [pluginId]: [...list, safe] }
    }
  }

  function trackRoute(pluginId: string, path: string) {
    const list = routesByPlugin.value[pluginId] ?? []
    if (!list.includes(path)) {
      routesByPlugin.value = { ...routesByPlugin.value, [pluginId]: [...list, path] }
    }
  }

  function markLoaded(pluginId: string) {
    if (!loaded.value.includes(pluginId)) loaded.value = [...loaded.value, pluginId]
  }

  function isLoaded(pluginId: string) {
    return loaded.value.includes(pluginId)
  }

  // Forget everything a plugin contributed (the loader handles router.removeRoute
  // using the tracked paths before calling this).
  function forget(pluginId: string) {
    const { [pluginId]: _s, ...restS } = sidebarByPlugin.value
    sidebarByPlugin.value = restS
    const { [pluginId]: _set, ...restSet } = settingsByPlugin.value
    settingsByPlugin.value = restSet
    const { [pluginId]: _d, ...restD } = dockPanelsByPlugin.value
    dockPanelsByPlugin.value = restD
    const { [pluginId]: _r, ...restR } = routesByPlugin.value
    routesByPlugin.value = restR
    loaded.value = loaded.value.filter((id) => id !== pluginId)
  }

  function routePaths(pluginId: string): string[] {
    return routesByPlugin.value[pluginId] ?? []
  }

  return {
    sidebarByPlugin, settingsByPlugin, dockPanelsByPlugin, routesByPlugin, loaded,
    sidebarItems, settingsSections, dockPanels,
    addSidebarItem, addSettingsSection, addDockPanel, trackRoute, markLoaded, isLoaded, forget, routePaths,
  }
})
