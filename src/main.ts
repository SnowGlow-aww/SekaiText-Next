import { createApp } from 'vue'
import { createPinia } from 'pinia'
import router from './router'
import { useDebugLog } from './composables/useDebugLog'
import { installHostBridge } from './plugin-host/bridge'
import { autoLoadPlugins } from './plugin-host/autoload'
import './style.css'
import App from './App.vue'

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
