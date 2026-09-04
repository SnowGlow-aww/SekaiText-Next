import { createApp } from 'vue'
import { createPinia } from 'pinia'
import router from './router'
import { useDebugLog } from './composables/useDebugLog'
import { installHostBridge } from './plugin-host/bridge'
import { startPluginStartup } from './plugin-host/autoload'
import { loadBuiltinLive2D } from './plugin-host/builtinLive2d'
import { capabilities } from './platform/capabilities'
import './style.css'
import App from './App.vue'

// Transport: in packaged builds the frontend talks to the Go backend over the
// Tauri custom scheme (sekai://) instead of TCP, so there is no externally
// reachable socket to defend. Development stays same-origin and Vite proxies API
// calls with an ephemeral TCP capability; the backend origin is read from
// window.__SEKAI_ORIGIN__ in src/api/client.ts.

const app = createApp(App)
const pinia = createPinia()
app.use(pinia)
app.use(router)

// Expose the host bridge after pinia + router are active. Desktop/web keeps its
// signed dynamic-plugin startup unchanged; Android only activates the immutable
// Live2D bundle shipped inside the APK.
const needsHostBridge = capabilities.supportsPlugins || capabilities.isAndroid
if (needsHostBridge) {
  const host = installHostBridge(router, pinia)
  const hostVersion = typeof __APP_VERSION__ !== 'undefined' ? __APP_VERSION__ : ''
  const startup = capabilities.isAndroid
    ? loadBuiltinLive2D(host)
    : startPluginStartup(host, hostVersion)

  // Plugin routes register asynchronously, so a cold start landing directly on
  // #/live2d initially resolves to no match. Re-resolve after startup so the new
  // route becomes active. Android failures are visible but do not blank the editor.
  void startup.then(async () => {
    await router.isReady()
    const cur = router.currentRoute.value
    if (cur.matched.length === 0) {
      await router.replace(cur.fullPath).catch(() => {})
    }
  }).catch((error) => {
    console.error('[startup] Live2D/plugin initialization failed', error)
  })
}

// Initialize console log capture for debug panel
const debug = useDebugLog()
debug.initConsoleCapture()

// Prevent Backspace outside of active text inputs from triggering browser history navigation.
if (typeof window !== 'undefined') {
  window.addEventListener('keydown', (e: KeyboardEvent) => {
    if (e.key === 'Backspace') {
      const ae = document.activeElement
      const isInput = ae instanceof HTMLElement && (
        ae.tagName === 'INPUT' ||
        ae.tagName === 'TEXTAREA' ||
        ae.isContentEditable
      )
      if (!isInput) {
        e.preventDefault()
      }
    }
  })
}

app.config.errorHandler = (err, _instance, info) => {
  console.error('[Vue Error]', err, info)
}

app.mount('#app')
