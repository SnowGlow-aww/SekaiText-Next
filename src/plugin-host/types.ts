// Plugin host types shared by the bridge, loader, registry and (via the global)
// by plugins themselves. Plugins are FIRST-PARTY and trusted — the bridge hands
// them the host's live Vue/Pinia/router singletons so they never bundle their
// own copies (which would create a second Vue instance and break reactivity).
import type * as VueRuntime from 'vue'
import type { Router, RouteRecordRaw } from 'vue-router'
import type { Pinia } from 'pinia'

// A sidebar entry contributed by a plugin. `icon` is a lucide-vue-next icon
// NAME (string) resolved by the host, so plugins don't import the icon set.
export interface PluginSidebarItem {
  id: string // unique, namespaced by plugin (e.g. "live2d:player")
  label: string
  icon: string // lucide icon name, e.g. "Drama"
  to: string // route path
  order?: number
  // Stamped by the registry when flattening so the host can build a globally
  // unique v-for key (two plugins may pick the same item id).
  pluginId?: string
}

// A settings-page section contributed by a plugin. The host renders `component`
// (a plugin-provided Vue component using host.vue) inside the settings layout,
// wrapped in the standard section card + title. Lets a plugin own its own
// settings UI instead of the core hard-coding it.
export interface PluginSettingsSection {
  id: string // unique, namespaced by plugin (e.g. "live2d:assets")
  title: string // section heading shown above the card
  component: any // a Vue component (resolved via host.vue)
  order?: number
  // Stamped by the registry when flattening so the host can build a globally
  // unique v-for key (two plugins may pick the same section id).
  pluginId?: string
}

// What the host exposes to every plugin's setup(host).
export interface SekaiHost {
  // Host version (semver) so a plugin can check minHostVersion.
  version: string
  // The host's Vue runtime — plugins call host.vue.defineComponent/h/ref/... and
  // SFCs are compiled with vue externalized so they resolve to THIS instance.
  vue: typeof VueRuntime
  // The host's router + pinia singletons.
  router: Router
  pinia: Pinia
  // Lazily-resolved store accessors (call to get the live store instance).
  stores: {
    story: () => any
    app: () => any
    settings: () => any
  }
  // The API client (same instance the core uses).
  api: any
  // UI helpers.
  ui: {
    toast: (message: string, type?: 'success' | 'error' | 'info' | 'warn', duration?: number) => void
  }
  // Pre-compiled core components plugins can reuse (shared, not re-bundled).
  components: {
    StoryNavigator: any
  }
  // Registration — a plugin calls these in setup(); the host tracks them under
  // the plugin id so unload can reverse them.
  registerRoute: (pluginId: string, route: RouteRecordRaw) => void
  registerSidebarItem: (pluginId: string, item: PluginSidebarItem) => void
  registerSettingsSection: (pluginId: string, section: PluginSettingsSection) => void
}

// The shape a plugin's ESM entry must export.
export interface PluginModule {
  setup: (host: SekaiHost) => void | Promise<void>
  teardown?: () => void | Promise<void>
}

declare global {
  interface Window {
    __SEKAI_HOST__?: SekaiHost
  }
}

export {}
