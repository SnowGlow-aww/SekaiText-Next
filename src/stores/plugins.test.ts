// @ts-nocheck -- mocks intentionally provide only the plugin APIs used here.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

const mocks = vi.hoisted(() => ({
  pluginSetEnabled: vi.fn(),
  pluginUninstall: vi.fn(),
  pluginRollback: vi.fn(),
  fetchInstalledPlugins: vi.fn(),
  pluginEntryUrl: vi.fn(plugin => `/plugins/${plugin.id}/entry.js`),
  cancelPluginLoad: vi.fn(),
  loadPlugin: vi.fn(),
  unloadPlugin: vi.fn(),
}))

vi.hoisted(() => { globalThis.window = {} })
vi.mock('../api/client', () => ({ api: {
  pluginSetEnabled: mocks.pluginSetEnabled,
  pluginUninstall: mocks.pluginUninstall,
  pluginRollback: mocks.pluginRollback,
} }))
vi.mock('../plugin-host/autoload', () => ({
  fetchInstalledPlugins: mocks.fetchInstalledPlugins,
  pluginEntryUrl: mocks.pluginEntryUrl,
}))
vi.mock('../plugin-host/loader', () => ({
  cancelPluginLoad: mocks.cancelPluginLoad,
  loadPlugin: mocks.loadPlugin,
  unloadPlugin: mocks.unloadPlugin,
}))

import { usePluginsStore } from './plugins'

const installed = {
  id: 'demo',
  name: 'Demo',
  version: '1.0.0',
  enabled: true,
  local: true,
  loadToken: 'token',
}

describe('plugin backend/runtime consistency', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    const pinia = createPinia()
    setActivePinia(pinia)
    window.__SEKAI_HOST__ = { pinia, ui: { toast: vi.fn() } }
    mocks.fetchInstalledPlugins.mockResolvedValue([{ ...installed }])
    mocks.loadPlugin.mockResolvedValue(undefined)
    mocks.unloadPlugin.mockResolvedValue(undefined)
  })

  it('reloads an enabled runtime when disabling fails in the backend', async () => {
    const plugins = usePluginsStore()
    plugins.list = [{ ...installed }]
    mocks.pluginSetEnabled.mockRejectedValue(new Error('backend write failed'))

    await expect(plugins.setEnabled('demo', false)).rejects.toThrow('backend write failed')

    expect(mocks.unloadPlugin).toHaveBeenCalledWith('demo', window.__SEKAI_HOST__.pinia)
    expect(mocks.loadPlugin).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'demo', enabled: true }),
      '/plugins/demo/entry.js',
      window.__SEKAI_HOST__,
    )
  })

  it('reloads an enabled runtime when uninstalling fails in the backend', async () => {
    const plugins = usePluginsStore()
    plugins.list = [{ ...installed }]
    mocks.pluginUninstall.mockRejectedValue(new Error('delete failed'))

    await expect(plugins.uninstall('demo')).rejects.toThrow('delete failed')

    expect(mocks.unloadPlugin).toHaveBeenCalledWith('demo', window.__SEKAI_HOST__.pinia)
    expect(mocks.loadPlugin).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'demo', enabled: true }),
      '/plugins/demo/entry.js',
      window.__SEKAI_HOST__,
    )
  })
})
