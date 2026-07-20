// @ts-nocheck -- Vitest is supplied by the shared frontend-test setup.
import { beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  loadPlugin: vi.fn(),
  marketAutoUpdate: vi.fn(),
  pluginRollback: vi.fn(),
  pluginSetEnabled: vi.fn(),
}))

vi.mock('./loader', () => ({
  isPluginLoadCancelled: () => false,
  loadPlugin: mocks.loadPlugin,
  samePluginLoadIdentity: (current, expected) =>
    current.id === expected.id && current.enabled &&
    current.version === expected.version && current.loadToken === expected.loadToken &&
    JSON.stringify(current.provenance ?? null) === JSON.stringify(expected.provenance ?? null),
}))

vi.mock('../api/client', () => ({
  BASE_URL: 'http://plugin.test',
  api: {
    marketAutoUpdate: mocks.marketAutoUpdate,
    pluginRollback: mocks.pluginRollback,
    pluginSetEnabled: mocks.pluginSetEnabled,
  },
}))

import { autoLoadPlugins, startPluginStartup } from './autoload'

const plugin = (version: string) => ({
  id: 'demo', name: 'Demo', version, enabled: true, local: false,
  loadToken: `token-${version}`,
  provenance: {
    source: 'verified-market', publisher: 'sekaitext-official', keyId: 'key',
    sha256: version.padEnd(64, 'a'), indexVersion: 3, sequence: 1,
  },
})

const host = { ui: { toast: vi.fn() } }

describe('plugin startup coordination', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  it('finishes auto-update before taking the autoload snapshot', async () => {
    const events: string[] = []
    mocks.marketAutoUpdate.mockImplementation(async () => {
      events.push('update')
      return { updated: [], failed: [] }
    })
    vi.stubGlobal('fetch', vi.fn(async () => {
      events.push('list')
      return new Response(JSON.stringify([plugin('2.0.0')]), { status: 200 })
    }))
    mocks.loadPlugin.mockImplementation(async () => { events.push('load') })

    await startPluginStartup(host, '5.9.0')
    expect(events).toEqual(['update', 'list', 'load'])
  })

  it('does not roll back a newer payload after a stale load is invalidated', async () => {
    const responses = [[plugin('1.0.0')], [plugin('2.0.0')]]
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(responses.shift()), { status: 200 })))
    mocks.loadPlugin.mockRejectedValueOnce(new Error('stale load invalidated'))

    await autoLoadPlugins(host)
    expect(mocks.pluginRollback).not.toHaveBeenCalled()
    expect(mocks.pluginSetEnabled).not.toHaveBeenCalled()
  })
})
