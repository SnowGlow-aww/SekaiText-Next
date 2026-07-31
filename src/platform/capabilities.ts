export type SekaiPlatform = 'android' | 'desktop' | 'web'

export interface PlatformSignals {
  platform?: Window['__SEKAI_PLATFORM__']
  tauri?: unknown
}

export function detectPlatform(signals?: PlatformSignals): SekaiPlatform {
  const current = signals ?? (typeof window === 'undefined'
    ? {}
    : { platform: window.__SEKAI_PLATFORM__, tauri: window.__TAURI_INTERNALS__ })
  if (current.platform === 'android') return 'android'
  if (current.platform === 'desktop' || current.tauri) return 'desktop'
  return 'web'
}

export function capabilitiesFor(platform: SekaiPlatform) {
  const isAndroid = platform === 'android'
  const isDesktop = platform === 'desktop'
  const isTauri = isAndroid || isDesktop
  return Object.freeze({
    platform,
    isAndroid,
    isDesktop,
    isTauri,
    hasDesktopBackend: isDesktop,
    hasEmbeddedBackend: isAndroid,
    // Browser/Vite historically exposes the same backend-backed feature surface
    // as desktop. Android is the only reduced shell.
    supportsPlugins: !isAndroid,
    supportsAppUpdater: !isAndroid,
    supportsDesktopDirectories: !isAndroid,
    supportsDetachedLive2DWindow: !isAndroid,
    supportsEngine: !isAndroid,
  })
}

/** Platform gates are centralized so Android work cannot silently alter desktop. */
export const platform = detectPlatform()
export const capabilities = capabilitiesFor(platform)
export const { isAndroid, isDesktop, isTauri } = capabilities
