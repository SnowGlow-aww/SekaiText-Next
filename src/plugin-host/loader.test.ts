// @ts-nocheck -- Vitest is supplied by the shared frontend-test setup.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { nextTick } from 'vue'
import { installHostBridge } from './bridge'
import { activatePluginModule, samePluginLoadIdentity } from './loader'
import { usePluginRegistry } from './registry'

vi.hoisted(() => {
  globalThis.window = {}
})

const { toast } = vi.hoisted(() => ({ toast: vi.fn() }))

vi.mock('../components/navigation/StoryNavigator.vue', () => ({ default: {} }))
vi.mock('../composables/useToast', () => ({ useToast: () => ({ show: toast }) }))

function createHost(routes = []) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createRouter({ history: createMemoryHistory(), routes })
  return { host: installHostBridge(router, pinia), pinia, router }
}

describe('plugin activation reliability', () => {
  beforeEach(() => {
    delete window.__SEKAI_HOST__
    toast.mockReset()
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })
  afterEach(() => vi.useRealTimers())

  it('rejects a route conflict without accounting ownership', async () => {
    const { host, pinia, router } = createHost([
      { path: '/taken', name: 'core:taken', component: { render: () => null } },
    ])
    const teardown = vi.fn()

    await expect(activatePluginModule('conflict', {
      setup(scopedHost) {
        scopedHost.registerRoute('conflict', {
          path: '/taken',
          name: 'plugin:taken',
          component: { render: () => null },
        })
      },
      teardown,
    }, host)).rejects.toThrow('路由路径冲突')

    const registry = usePluginRegistry(pinia)
    expect(registry.routesByPlugin.conflict).toBeUndefined()
    expect(router.hasRoute('core:taken')).toBe(true)
    expect(teardown).toHaveBeenCalledOnce()
  })

  it('best-effort tears down and rolls back every partial contribution', async () => {
    const { host, pinia, router } = createHost()
    const teardown = vi.fn(() => { throw new Error('teardown also failed') })

    await expect(activatePluginModule('partial', {
      setup(scopedHost) {
        scopedHost.registerRoute('partial', {
          path: '/partial',
          name: 'plugin:partial',
          component: { render: () => null },
        })
        scopedHost.registerSidebarItem('partial', { id: 'nav', label: 'Partial', icon: 'Puzzle', to: '/partial' })
        scopedHost.registerSettingsSection('partial', { id: 'settings', title: 'Partial', component: { render: () => null } })
        scopedHost.registerDockPanel('partial', { id: 'dock', component: { render: () => null } })
        throw new Error('setup failed')
      },
      teardown,
    }, host)).rejects.toThrow('setup failed')

    const registry = usePluginRegistry(pinia)
    expect(teardown).toHaveBeenCalledOnce()
    expect(router.hasRoute('plugin:partial')).toBe(false)
    expect(registry.sidebarByPlugin.partial).toBeUndefined()
    expect(registry.settingsByPlugin.partial).toBeUndefined()
    expect(registry.dockPanelsByPlugin.partial).toBeUndefined()
    expect(registry.routesByPlugin.partial).toBeUndefined()
    expect(registry.isLoaded('partial')).toBe(false)
  })

  it('rejects a plugin claiming another loader-owned id', async () => {
    const { host, pinia } = createHost()

    await expect(activatePluginModule('owner', {
      setup(scopedHost) {
        scopedHost.registerSidebarItem('victim', { id: 'spoof', label: 'Spoof', icon: 'Puzzle', to: '/' })
      },
    }, host)).rejects.toThrow('不能以 victim 的身份')

    const registry = usePluginRegistry(pinia)
    expect(registry.sidebarByPlugin.owner).toBeUndefined()
    expect(registry.sidebarByPlugin.victim).toBeUndefined()
  })

  it.each([
    ['child name', { path: 'child', name: 'core:taken', component: { render: () => null } }],
    ['child path', { path: '/taken', name: 'plugin:child-path', component: { render: () => null } }],
    ['child alias', { path: 'child', name: 'plugin:child-alias', alias: '/taken', component: { render: () => null } }],
  ])('recursively rejects a conflicting %s', async (_label, child) => {
    const { host } = createHost([
      { path: '/taken', name: 'core:taken', component: { render: () => null } },
    ])

    await expect(activatePluginModule('nested', {
      setup(scopedHost) {
        scopedHost.registerRoute('nested', {
          path: '/plugin',
          name: 'plugin:root',
          component: { render: () => null },
          children: [child],
        })
      },
    }, host)).rejects.toThrow('冲突')
  })

  it('expands parent aliases when checking and attributing child routes', async () => {
    const { host, pinia, router } = createHost([
      { path: '/shortcut/child', name: 'core:shortcut-child', component: { render: () => null } },
    ])

    await expect(activatePluginModule('aliased', {
      setup(scopedHost) {
        scopedHost.registerRoute('aliased', {
          path: '/plugin',
          alias: '/shortcut',
          component: { render: () => null },
          children: [{ path: 'child', component: { render: () => null } }],
        })
      },
    }, host)).rejects.toThrow('路由路径冲突: /shortcut/child')

    expect(usePluginRegistry(pinia).routesByPlugin.aliased).toBeUndefined()
    expect(router.hasRoute('core:shortcut-child')).toBe(true)
  })

  it('attributes dynamic child matches to the owning plugin', async () => {
    const { host, pinia, router } = createHost()

    await activatePluginModule('owner', {
      setup(scopedHost) {
        scopedHost.registerRoute('owner', {
          path: '/plugin',
          alias: '/shortcut',
          component: { render: () => null },
          children: [{ path: ':id', component: { render: () => null } }],
        })
      },
    }, host)

    expect(router.resolve('/shortcut/42').meta.sekaiPluginId).toBe('owner')
    expect(usePluginRegistry(pinia).routesByPlugin.owner).toEqual(expect.arrayContaining([
      '/plugin/:id',
      '/shortcut/:id',
    ]))
  })

  it('exposes navigation without raw route-table mutation methods', () => {
    const { host } = createHost()

    expect(host.router.push).toBeTypeOf('function')
    expect(host.router.addRoute).toBeUndefined()
    expect(host.router.removeRoute).toBeUndefined()
  })

  it('rolls back when a generation is invalidated during async setup', async () => {
    const { host, pinia, router } = createHost()
    let finishSetup
    let current = true
    const setupGate = new Promise((resolve) => { finishSetup = resolve })

    const activation = activatePluginModule('cancelled', {
      async setup(scopedHost) {
        scopedHost.registerRoute('cancelled', {
          path: '/cancelled',
          name: 'plugin:cancelled',
          component: { render: () => null },
        })
        await setupGate
      },
    }, host, () => current)

    current = false
    finishSetup()
    await expect(activation).rejects.toMatchObject({ name: 'AbortError' })
    expect(router.hasRoute('plugin:cancelled')).toBe(false)
    expect(usePluginRegistry(pinia).isLoaded('cancelled')).toBe(false)
  })

  it('cancels a stuck setup and prevents late registrations', async () => {
    const { host, pinia, router } = createHost()
    const controller = new AbortController()
    let continueSetup
    let setupStarted
    const started = new Promise((resolve) => { setupStarted = resolve })
    const gate = new Promise((resolve) => { continueSetup = resolve })

    const activation = activatePluginModule('stuck', {
      async setup(scopedHost) {
        scopedHost.registerRoute('stuck', {
          path: '/stuck',
          name: 'plugin:stuck',
          component: { render: () => null },
        })
        setupStarted()
        await gate
        scopedHost.registerSidebarItem('stuck', { id: 'late', label: 'Late', icon: 'Puzzle', to: '/stuck' })
      },
    }, host, () => true, controller.signal, 1000)

    await started
    controller.abort(new DOMException('disabled', 'AbortError'))
    await expect(activation).rejects.toMatchObject({ name: 'AbortError' })
    continueSetup()
    await Promise.resolve()

    expect(router.hasRoute('plugin:stuck')).toBe(false)
    expect(usePluginRegistry(pinia).sidebarByPlugin.stuck).toBeUndefined()
  })

  it('times out and rolls back a setup that never settles', async () => {
    vi.useFakeTimers()
    const { host, pinia } = createHost()
    const activation = activatePluginModule('timeout', {
      setup(scopedHost) {
        scopedHost.registerSidebarItem('timeout', { id: 'partial', label: 'Partial', icon: 'Puzzle', to: '/' })
        return new Promise(() => {})
      },
    }, host, () => true, undefined, 25)
    const rejection = expect(activation).rejects.toMatchObject({ name: 'TimeoutError' })

    await vi.advanceTimersByTimeAsync(25)
    await rejection

    expect(usePluginRegistry(pinia).sidebarByPlugin.timeout).toBeUndefined()
  })

  it('revokes retained store, API, UI, router, and listener capabilities after setup timeout', async () => {
    vi.useFakeTimers()
    const { host, pinia, router } = createHost()
    const apiCall = vi.fn()
    host.api.testLateCall = apiCall
    const listener = vi.fn()
    let continueSetup
    let setupStarted
    const started = new Promise((resolve) => { setupStarted = resolve })
    const gate = new Promise((resolve) => { continueSetup = resolve })

    const activation = activatePluginModule('delayed', {
      async setup(scopedHost) {
        const story = scopedHost.stores.story()
        const lateApiCall = scopedHost.api.testLateCall
        const lateToast = scopedHost.ui.toast
        const latePush = scopedHost.router.push
        story.$subscribe(listener)
        setupStarted()
        await gate

        for (const operation of [
          () => { story.selectedType = 'late' },
          () => lateApiCall(),
          () => lateToast('late'),
          () => latePush('/late'),
          () => story.$subscribe(listener),
        ]) {
          try { operation() } catch { /* revoked capabilities fail closed */ }
        }
      },
    }, host, () => true, undefined, 25)
    const rejection = expect(activation).rejects.toMatchObject({ name: 'TimeoutError' })

    await started
    await vi.advanceTimersByTimeAsync(25)
    await rejection

    const story = host.stores.story()
    story.selectedType = 'host-change'
    await nextTick()
    continueSetup()
    await Promise.resolve()
    await Promise.resolve()

    expect(story.selectedType).toBe('host-change')
    expect(listener).not.toHaveBeenCalled()
    expect(apiCall).not.toHaveBeenCalled()
    expect(toast).not.toHaveBeenCalled()
    expect(router.currentRoute.value.path).toBe('/')
    delete host.api.testLateCall
  })

  it('requires enabled, version, provenance, and load token to match', () => {
    const expected = {
      id: 'demo', name: 'Demo', version: '1.0.0', enabled: true, local: false,
      loadToken: 'token-a', provenance: {
        source: 'verified-market', publisher: 'sekaitext-official', keyId: 'key',
        sha256: 'a'.repeat(64), indexVersion: 3, sequence: 7,
      },
    }
    expect(samePluginLoadIdentity({ ...expected }, expected)).toBe(true)
    expect(samePluginLoadIdentity({ ...expected, enabled: false }, expected)).toBe(false)
    expect(samePluginLoadIdentity({ ...expected, version: '1.0.1' }, expected)).toBe(false)
    expect(samePluginLoadIdentity({ ...expected, loadToken: 'token-b' }, expected)).toBe(false)
    expect(samePluginLoadIdentity({
      ...expected,
      provenance: { ...expected.provenance, sequence: 8 },
    }, expected)).toBe(false)
  })

})
