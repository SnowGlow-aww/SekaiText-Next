import { describe, expect, it } from 'vitest'
import { capabilitiesFor, detectPlatform } from './capabilities'

describe('platform capabilities', () => {
  it('isolates Android from desktop-only features', () => {
    expect(detectPlatform({ platform: 'android' })).toBe('android')
    expect(capabilitiesFor('android')).toMatchObject({
      isAndroid: true,
      hasEmbeddedBackend: true,
      supportsPlugins: false,
      supportsAppUpdater: true,
      supportsDetachedLive2DWindow: false,
      supportsEngine: false,
    })
  })

  it('preserves the desktop feature surface', () => {
    expect(detectPlatform({ platform: 'desktop', tauri: {} })).toBe('desktop')
    expect(detectPlatform({ tauri: {} })).toBe('desktop')
    expect(capabilitiesFor('desktop')).toMatchObject({
      isDesktop: true,
      supportsPlugins: true,
      supportsAppUpdater: true,
      supportsDesktopDirectories: true,
      supportsDetachedLive2DWindow: true,
      supportsEngine: true,
    })
  })

  it('preserves the browser/Vite backend feature surface', () => {
    expect(detectPlatform({})).toBe('web')
    expect(capabilitiesFor('web')).toMatchObject({
      platform: 'web',
      isAndroid: false,
      isDesktop: false,
      supportsPlugins: true,
      supportsAppUpdater: true,
      supportsDesktopDirectories: true,
      supportsDetachedLive2DWindow: true,
      supportsEngine: true,
    })
  })
})
