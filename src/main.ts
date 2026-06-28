import { createApp } from 'vue'
import { createPinia } from 'pinia'
import router from './router'
import { useDebugLog } from './composables/useDebugLog'
import { installHostBridge } from './plugin-host/bridge'
import { autoLoadPlugins } from './plugin-host/autoload'
import './style.css'
import App from './App.vue'

// --- Capability token (security) ---------------------------------------------
// In the packaged app the Tauri shell injects a random per-launch token via the
// `auth_token` command, and the Go sidecar requires it on mutating requests so a
// stray web page hitting 127.0.0.1:9800 can't drive settings→download→open. Wrap
// window.fetch so every backend call — the app's own client AND plugin bundles
// (same window.fetch) — carries X-Sekai-Token. In dev there is no Tauri runtime →
// token is '' → the sidecar doesn't enforce.
const __BACKEND_ORIGIN = 'http://localhost:9800'
const __tokenPromise: Promise<string> = (async () => {
  try {
    const { invoke } = await import('@tauri-apps/api/core')
    return (await invoke<string>('auth_token')) || ''
  } catch {
    return ''
  }
})()
const __nativeFetch = window.fetch.bind(window)
window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
  if (typeof url === 'string' && url.startsWith(__BACKEND_ORIGIN)) {
    const token = await __tokenPromise
    if (token) {
      const headers = new Headers(init?.headers ?? (input instanceof Request ? input.headers : undefined))
      headers.set('X-Sekai-Token', token)
      init = { ...init, headers }
    }
  }
  return __nativeFetch(input, init)
}

const app = createApp(App)
const pinia = createPinia()
app.use(pinia)
app.use(router)

// Expose the host bridge to plugins (after pinia + router are active), then
// auto-load any installed/enabled plugins. Plugin routes register asynchronously,
// so a cold start landing directly on a plugin URL (e.g. #/live2d) initially
// resolves to no match (blank page). After autoload, if the current route never
// matched, re-resolve it so the freshly-registered plugin route takes effect.
const host = installHostBridge(router, pinia)
void autoLoadPlugins(host).then(async () => {
  await router.isReady()
  const cur = router.currentRoute.value
  if (cur.matched.length === 0) {
    await router.replace(cur.fullPath).catch(() => {})
  }
})

// Initialize console log capture for debug panel
const debug = useDebugLog()
debug.initConsoleCapture()

app.config.errorHandler = (err, _instance, info) => {
  console.error('[Vue Error]', err, info)
}

app.mount('#app')
