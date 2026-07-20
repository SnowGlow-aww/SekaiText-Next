// Plugin host types shared by the bridge, loader, registry and (via the global)
// by plugins themselves. Plugins run with the host's full process permissions.
// The bridge hands them the live Vue/Pinia/router singletons so they never bundle
// their own copies (which would create a second Vue instance and break reactivity).
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

// A dockable panel contributed by a plugin. The host mounts `component` into the
// editor dock region (top/right/bottom edge, chosen in settings) so the user can
// keep e.g. the Live2D player visible beside the translation. The component
// receives the full editor width/height of its docked strip and should fill it.
// It drives playback by reading the shared dock store (host.stores.live2dDock()).
export interface PluginDockPanel {
  id: string // unique, namespaced by plugin (e.g. "live2d:dock")
  component: any // a Vue component (resolved via host.vue)
  // Stamped by the registry when flattening so the host can key the v-for.
  pluginId?: string
}

// Navigation-only router surface. Route table mutation remains private to the
// owner-bound registerRoute API so plugins cannot remove or replace host routes.
export type PluginRouter = Pick<Router,
  'currentRoute' | 'push' | 'replace' | 'resolve' | 'back' | 'forward' | 'go'
>

// What the host exposes to every plugin's setup(host).
export interface SekaiHost {
  // Host version (semver) so a plugin can check minHostVersion.
  version: string
  // The backend origin (no trailing slash), injected as window.__SEKAI_ORIGIN__:
  // the custom scheme (sekai://localhost / http://sekai.localhost) in packaged
  // builds, or http://localhost:9800 in dev. Plugins read this instead of
  // hard-coding the origin for asset/voice/proxy URLs.
  backendOrigin: string
  // The host's Vue runtime — plugins call host.vue.defineComponent/h/ref/... and
  // SFCs are compiled with vue externalized so they resolve to THIS instance.
  vue: typeof VueRuntime
  // Navigation surface plus the host's pinia singleton.
  router: PluginRouter
  pinia: Pinia
  // Lazily-resolved store accessors (call to get the live store instance).
  stores: {
    story: () => any
    app: () => any
    settings: () => any
    // Shared Live2D dock state: the editor's jump button publishes a pending jump
    // here and the plugin's docked panel/player watches `pendingJump` to act. The
    // store is host-owned so host and plugin coordinate through one reactive
    // singleton without sharing imports. See src/stores/live2dDock.ts.
    live2dDock: () => any
  }
  // The API client (same instance the core uses).
  api: any
  // UI helpers.
  ui: {
    toast: (message: string, type?: 'success' | 'error' | 'info' | 'warn', duration?: number) => void
  }
  // 分步导览：steps 结构见 src/onboarding/useTour.ts 的 TourStep。
  // startTourOnce 的 id 持久化到 settings.seenTours（自动加 plugin:<id>: 前缀），只弹一次。
  startTour: (pluginId: string, def: { id: string; steps: any[] }) => void
  startTourOnce: (pluginId: string, def: { id: string; steps: any[] }) => void
  // Native file dialogs (Tauri). Resolve to an absolute path / paths / null. In a
  // non-Tauri context (web dev) the underlying import throws — callers should
  // catch and fall back to manual path entry.
  dialog: {
    open: (options?: any) => Promise<string | string[] | null>
    save: (options?: any) => Promise<string | null>
  }
  // Pre-compiled core components plugins can reuse (shared, not re-bundled).
  components: {
    StoryNavigator: any
  }
  // Registration — a plugin calls these in setup(); the host tracks them under
  // the plugin id so unload can reverse them.
  registerRoute: (pluginId: string, route: RouteRecordRaw) => () => void
  registerSidebarItem: (pluginId: string, item: PluginSidebarItem) => void
  registerSettingsSection: (pluginId: string, section: PluginSettingsSection) => void
  // Contribute a dockable panel (e.g. the Live2D player) the host mounts in the
  // editor dock region. Tracked under the plugin id so unload reverses it.
  registerDockPanel: (pluginId: string, panel: PluginDockPanel) => void
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
