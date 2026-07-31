import { activatePluginModule } from './loader'
import type { PluginModule, SekaiHost } from './types'

const BUILTIN_LIVE2D_ID = 'live2d'
const BUILTIN_LIVE2D_ENTRY = '/builtin-plugins/live2d/entry.js'

/**
 * Android ships the reviewed Live2D 1.3.1 bundle as an immutable app asset.
 * Desktop intentionally keeps using its signed marketplace installation path.
 */
export async function loadBuiltinLive2D(host: SekaiHost): Promise<void> {
  const module = await import(/* @vite-ignore */ BUILTIN_LIVE2D_ENTRY) as PluginModule
  if (typeof module.setup !== 'function') {
    throw new Error('内置 Live2D 模块缺少 setup()')
  }
  await activatePluginModule(BUILTIN_LIVE2D_ID, module, host)
}
