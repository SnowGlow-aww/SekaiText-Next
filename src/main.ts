import { createApp } from 'vue'
import { createPinia } from 'pinia'
import router from './router'
import { useDebugLog } from './composables/useDebugLog'
import { installHostBridge } from './plugin-host/bridge'
import { startPluginStartup } from './plugin-host/autoload'
import './style.css'
import App from './App.vue'

// Transport: in packaged builds the frontend talks to the Go backend over the
// Tauri custom scheme (sekai://) instead of TCP, so there is no externally
// reachable socket to defend — the capability-token fetch monkey-patch and the
// `auth_token` invoke have been removed (the IPC stdio channel is in-process and
// the token middleware no-ops). The backend origin is read from
// window.__SEKAI_ORIGIN__ in src/api/client.ts.

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
const hostVersion = typeof __APP_VERSION__ !== 'undefined' ? __APP_VERSION__ : ''
void startPluginStartup(host, hostVersion).then(async () => {
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
