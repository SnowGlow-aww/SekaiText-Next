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
import type { SekaiHost, PluginSidebarItem, PluginSettingsSection, PluginDockPanel, PluginRouter } from './types'

declare const __APP_VERSION__: string

interface HostRegistrations {
  registerRoute: SekaiHost['registerRoute']
  registerSidebarItem: SekaiHost['registerSidebarItem']
  registerSettingsSection: SekaiHost['registerSettingsSection']
  registerDockPanel: SekaiHost['registerDockPanel']
  startTour: SekaiHost['startTour']
  startTourOnce: SekaiHost['startTourOnce']
}

export interface ScopedPluginHost {
  host: SekaiHost
  runSetup: <T>(setup: () => T) => T
  revoke: () => void
}

const registrationsByHost = new WeakMap<SekaiHost, HostRegistrations>()
const disposerMethods = new Set<PropertyKey>([
  'watch', 'watchEffect', 'watchPostEffect', 'watchSyncEffect', '$subscribe', '$onAction',
])

interface CapabilityScope<T extends object> {
  value: T
  assertActive: () => void
  run: <R>(operation: () => R) => R
  revoke: () => void
}

function createCapabilityScope<T extends object>(value: T, ownerId: string): CapabilityScope<T> {
  let active = true
  const effects = VueRuntime.effectScope(true)
  const proxies = new WeakMap<object, any>()
  const rawByProxy = new WeakMap<object, object>()
  const callbackWrappers = new WeakMap<Function, Function>()
  const disposerFactories = new WeakSet<Function>()
  const disposers = new Set<Function>()

  const assertActive = () => {
    if (!active) throw new Error(`插件 ${ownerId} 的 host capability 已撤销`)
  }

  const unwrap = <V,>(candidate: V): V => {
    if ((typeof candidate === 'object' && candidate !== null) || typeof candidate === 'function') {
      return (rawByProxy.get(candidate as object) ?? candidate) as V
    }
    return candidate
  }

  const guardCallback = <F extends Function>(callback: F): F => {
    const existing = callbackWrappers.get(callback)
    if (existing) return existing as F
    const guarded = function (this: unknown, ...args: unknown[]) {
      if (!active) return
      return callback.apply(wrap(this), args.map(wrap))
    }
    callbackWrappers.set(callback, guarded)
    return guarded as unknown as F
  }

  const prepareArgument = <V,>(candidate: V): V => {
    const raw = unwrap(candidate)
    if (raw !== candidate) return raw
    if (typeof candidate === 'function') return guardCallback(candidate) as V
    return candidate
  }

  const handler: ProxyHandler<any> = {
    get(target, property) {
      assertActive()
      const result = Reflect.get(target, property, target)
      if (typeof result === 'function' && disposerMethods.has(property)) disposerFactories.add(result)
      return wrap(result)
    },
    set(target, property, next) {
      assertActive()
      return Reflect.set(target, property, prepareArgument(next), target)
    },
    deleteProperty(target, property) {
      assertActive()
      return Reflect.deleteProperty(target, property)
    },
    defineProperty(target, property, descriptor) {
      assertActive()
      const next = { ...descriptor }
      if ('value' in next) next.value = prepareArgument(next.value)
      if (next.get) next.get = guardCallback(next.get)
      if (next.set) next.set = guardCallback(next.set)
      return Reflect.defineProperty(target, property, next)
    },
    getOwnPropertyDescriptor(target, property) {
      assertActive()
      const descriptor = Reflect.getOwnPropertyDescriptor(target, property)
      if (!descriptor || (!descriptor.configurable && 'value' in descriptor && !descriptor.writable)) {
        return descriptor
      }
      const next = { ...descriptor }
      if ('value' in next) next.value = wrap(next.value)
      if (next.get) next.get = guardCallback(next.get)
      if (next.set) next.set = guardCallback(next.set)
      return next
    },
    has(target, property) {
      assertActive()
      return Reflect.has(target, property)
    },
    ownKeys(target) {
      assertActive()
      return Reflect.ownKeys(target)
    },
    apply(target, thisArg, args) {
      assertActive()
      const result = Reflect.apply(target, unwrap(thisArg), args.map(prepareArgument))
      if (disposerFactories.has(target) && typeof result === 'function') disposers.add(result)
      return wrap(result)
    },
    construct(target, args, newTarget) {
      assertActive()
      return wrap(Reflect.construct(target, args.map(prepareArgument), unwrap(newTarget)))
    },
  }

  function wrap<V>(candidate: V): V {
    if ((typeof candidate !== 'object' || candidate === null) && typeof candidate !== 'function') return candidate
    // Awaited API/dialog results are data, not retained host capabilities. Keep
    // native promises intact; revocation fences every capability used after await.
    if (candidate instanceof Promise) return candidate
    const existing = proxies.get(candidate as object)
    if (existing) return existing
    const proxy = new Proxy(candidate as object, handler)
    proxies.set(candidate as object, proxy)
    rawByProxy.set(proxy, candidate as object)
    return proxy as V
  }

  return {
    value: wrap(value),
    assertActive,
    run: <R>(operation: () => R): R => {
      assertActive()
      return effects.run(operation) as R
    },
    revoke: () => {
      if (!active) return
      active = false
      effects.stop()
      for (const dispose of disposers) {
        try { dispose() } catch { /* best-effort cleanup; the capability is already fenced */ }
      }
      disposers.clear()
    },
  }
}

function resolveRoutePath(parent: string, path: string): string {
  if (path.startsWith('/')) return path || '/'
  const base = parent === '/' ? '' : parent.replace(/\/$/, '')
  return `${base}/${path}` || '/'
}

function validatePluginRoute(router: Router, pluginId: string, route: RouteRecordRaw, topName: string | symbol) {
  const existingNames = new Set(router.getRoutes().map(record => record.name).filter(name => name != null))
  const existingPaths = new Set(router.getRoutes().map(record => record.path))
  const proposedNames = new Set<string | symbol>()
  const proposedPaths = new Set<string>()

  const visit = (record: RouteRecordRaw, parentPaths: string[], fallbackName?: string | symbol) => {
    if (!record || typeof record !== 'object' || typeof record.path !== 'string') {
      throw new Error(`插件 ${pluginId} registerRoute: 子路由无效`)
    }
    const name = record.name ?? fallbackName
    if (name != null) {
      if (existingNames.has(name) || proposedNames.has(name)) {
        throw new Error(`插件 ${pluginId} 路由名称冲突: ${String(name)}`)
      }
      proposedNames.add(name)
    }

    const aliases = record.alias == null
      ? []
      : (Array.isArray(record.alias) ? record.alias : [record.alias])
    const paths = new Set<string>()
    for (const parentPath of parentPaths) {
      paths.add(resolveRoutePath(parentPath, record.path))
      for (const alias of aliases) {
        if (typeof alias !== 'string') throw new Error(`插件 ${pluginId} 路由别名无效: ${String(alias)}`)
        paths.add(resolveRoutePath(parentPath, alias))
      }
    }
    for (const path of paths) {
      if (existingPaths.has(path) || proposedPaths.has(path)) {
        throw new Error(`插件 ${pluginId} 路由路径冲突: ${path}`)
      }
      proposedPaths.add(path)
    }
    // A relative child is reachable below every parent alias, not only below the
    // parent's canonical path. Expand the complete route tree before checking or
    // attributing paths so an alias cannot bypass a host/plugin collision.
    for (const child of record.children ?? []) visit(child, [...paths])
  }

  visit(route, [''], topName)
  return [...proposedPaths]
}

function attributePluginRoute(route: RouteRecordRaw, pluginId: string): RouteRecordRaw {
  return {
    ...route,
    meta: { ...route.meta, sekaiPluginId: pluginId },
    children: route.children?.map(child => attributePluginRoute(child, pluginId)),
  } as RouteRecordRaw
}

function unavailableRegistration(): never {
  throw new Error('插件注册 API 只能通过 loader 提供的 scoped host 调用')
}

// Bind every identity-bearing API to the id selected by the loader. Keeping the
// legacy pluginId argument lets existing plugins run, but it is now an assertion
// rather than caller-controlled ownership.
export function scopeHostForPlugin(host: SekaiHost, ownerId: string): ScopedPluginHost {
  const registrations = registrationsByHost.get(host)
  if (!registrations) throw new Error('host bridge is not installed')
  let capabilities: CapabilityScope<SekaiHost>
  const assertOwner = (claimedId: string) => {
    capabilities.assertActive()
    if (claimedId !== ownerId) {
      throw new Error(`插件 ${ownerId} 不能以 ${claimedId || '(empty)'} 的身份注册贡献`)
    }
  }
  const scopedHost: SekaiHost = {
    ...host,
    // Namespace/module objects can contain non-configurable exports, so copy the
    // capability containers before placing the revocable membrane over them.
    vue: { ...host.vue },
    router: { ...host.router },
    stores: { ...host.stores },
    api: { ...host.api },
    ui: { ...host.ui },
    dialog: { ...host.dialog },
    components: { ...host.components },
    registerRoute: (pluginId, route) => {
      assertOwner(pluginId)
      return registrations.registerRoute(ownerId, route)
    },
    registerSidebarItem: (pluginId, item) => {
      assertOwner(pluginId)
      registrations.registerSidebarItem(ownerId, item)
    },
    registerSettingsSection: (pluginId, section) => {
      assertOwner(pluginId)
      registrations.registerSettingsSection(ownerId, section)
    },
    registerDockPanel: (pluginId, panel) => {
      assertOwner(pluginId)
      registrations.registerDockPanel(ownerId, panel)
    },
    startTour: (pluginId, def) => {
      assertOwner(pluginId)
      registrations.startTour(ownerId, def)
    },
    startTourOnce: (pluginId, def) => {
      assertOwner(pluginId)
      registrations.startTourOnce(ownerId, def)
    },
  }
  capabilities = createCapabilityScope(scopedHost, ownerId)
  return {
    host: capabilities.value,
    runSetup: capabilities.run,
    revoke: capabilities.revoke,
  }
}

// Builds the host bridge and installs it on window.__SEKAI_HOST__ exactly once.
// Must run AFTER app.use(pinia) and app.use(router) in main.ts, since stores and
// route registration need the active instances. The bridge hands plugins the
// host's own Vue/router/pinia so a dynamically-imported plugin shares the same
// singletons (no second Vue instance).
export function installHostBridge(router: Router, pinia: Pinia): SekaiHost {
  if (window.__SEKAI_HOST__) return window.__SEKAI_HOST__

  const { show: toast } = useToast()

  const pluginRouter: PluginRouter = Object.freeze({
    currentRoute: router.currentRoute,
    push: router.push.bind(router),
    replace: router.replace.bind(router),
    resolve: router.resolve.bind(router),
    back: router.back.bind(router),
    forward: router.forward.bind(router),
    go: router.go.bind(router),
  })

  const registerRoute = (pluginId: string, route: RouteRecordRaw): (() => void) => {
    const registry = usePluginRegistry(pinia)
    if (typeof pluginId !== 'string' || !pluginId) {
      throw new Error('registerRoute(pluginId, route): pluginId 必须是非空字符串')
    }
    if (!route || typeof route !== 'object' || typeof route.path !== 'string' || !route.path) {
      throw new Error(`插件 ${pluginId} registerRoute: route 无效`)
    }
    const name = route.name ?? `plugin:${pluginId}:${route.path}`
    const attributedRoute = attributePluginRoute(route, pluginId)
    const paths = validatePluginRoute(router, pluginId, attributedRoute, name)
    const dispose = router.addRoute({ ...attributedRoute, name } as RouteRecordRaw)
    return registry.trackRoute(pluginId, paths, name, dispose)
  }

  const registerSidebarItem = (pluginId: string, item: PluginSidebarItem) => {
    const registry = usePluginRegistry(pinia)
    if (typeof pluginId !== 'string' || !pluginId) {
      throw new Error('registerSidebarItem(pluginId, item): pluginId 必须是非空字符串')
    }
    if (!item || typeof item !== 'object' || typeof item.id !== 'string' || !item.to) {
      throw new Error(`插件 ${pluginId} registerSidebarItem: item 无效（需要 {id, label, to}）`)
    }
    if (!registry.addSidebarItem(pluginId, item)) {
      throw new Error(`插件 ${pluginId} 侧栏项冲突: ${item.id}`)
    }
  }

  const registerSettingsSection = (pluginId: string, section: PluginSettingsSection) => {
    const registry = usePluginRegistry(pinia)
    if (typeof pluginId !== 'string' || !pluginId) {
      throw new Error('registerSettingsSection(pluginId, section): pluginId 必须是非空字符串')
    }
    if (!section || typeof section !== 'object' || typeof section.id !== 'string' || !section.component) {
      throw new Error(`插件 ${pluginId} registerSettingsSection: section 无效（需要 {id, component}）`)
    }
    if (!registry.addSettingsSection(pluginId, section)) {
      throw new Error(`插件 ${pluginId} 设置区块冲突: ${section.id}`)
    }
  }

  const registerDockPanel = (pluginId: string, panel: PluginDockPanel) => {
    const registry = usePluginRegistry(pinia)
    if (typeof pluginId !== 'string' || !pluginId) {
      throw new Error('registerDockPanel(pluginId, panel): pluginId 必须是非空字符串')
    }
    if (!panel || typeof panel !== 'object' || typeof panel.id !== 'string' || !panel.component) {
      throw new Error(`插件 ${pluginId} registerDockPanel: panel 无效（需要 {id, component}）`)
    }
    if (!registry.addDockPanel(pluginId, panel)) {
      throw new Error(`插件 ${pluginId} 停靠面板冲突: ${panel.id}`)
    }
  }

  const host: SekaiHost = {
    version: typeof __APP_VERSION__ !== 'undefined' ? __APP_VERSION__ : '0.0.0',
    // Backend origin (= window.__SEKAI_ORIGIN__) so plugins read it from the host
    // instead of hard-coding localhost:9800.
    backendOrigin: ORIGIN,
    vue: VueRuntime,
    router: pluginRouter,
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
    // The global bridge deliberately cannot register contributions. loadPlugin
    // replaces these with owner-bound functions on the scoped host it passes to setup().
    startTour: unavailableRegistration,
    startTourOnce: unavailableRegistration,
    registerRoute: unavailableRegistration,
    registerSidebarItem: unavailableRegistration,
    registerSettingsSection: unavailableRegistration,
    registerDockPanel: unavailableRegistration,
  }

  registrationsByHost.set(host, {
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
  })

  window.__SEKAI_HOST__ = host
  return host
}
