<template>
  <AppShell>
    <router-view v-slot="{ Component }">
      <transition name="page-shift" mode="out-in">
        <keep-alive :include="keptAlivePages" :max="keptAlivePages.length">
          <component :is="Component" />
        </keep-alive>
      </transition>
    </router-view>
  </AppShell>
  <Toast />
  <UpdateBanner />
  <DownloadFloat />
  <ConfirmHost />
  <RecoveryDialog
    v-if="showRecovery"
    @restore="showRecovery = false; maybeStartBootTour()"
    @discard="showRecovery = false; maybeStartBootTour()"
  />
  <TourOverlay />
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { useSettingsStore } from './stores/settings'
import { useAppStore } from './stores/app'
import { useEditorStore } from './stores/editor'
import { useAppUpdateStore } from './stores/appUpdate'
import { useToast } from './composables/useToast'
import { useDebugLog } from './composables/useDebugLog'
import { api, ORIGIN } from './api/client'
import Toast from './components/Toast.vue'
import UpdateBanner from './components/ui/UpdateBanner.vue'
import DownloadFloat from './components/DownloadFloat.vue'
import ConfirmHost from './components/ui/ConfirmHost.vue'
import RecoveryDialog from './components/RecoveryDialog.vue'
import TourOverlay from './onboarding/TourOverlay.vue'
import AppShell from './components/ui/AppShell.vue'
import { useTour } from './onboarding/useTour'
import { appWelcomeTour, pluginIntroTour, whatsNewTour } from './onboarding/tours'
import { usePluginRegistry } from './plugin-host/registry'
import { useRouter } from 'vue-router'
import { hasRecovery } from './editor/recovery'
import { pluginStartupResult } from './plugin-host/autoload'

const settings = useSettingsStore()
// Keep only pages with activation-bound lifecycle/state. Utility/listing pages
// remount cheaply instead of accumulating every visited route for the app lifetime.
const keptAlivePages = ['EditorPage', 'SettingsPage', 'DebugPage', 'GlossaryPage']
const appUpdate = useAppUpdateStore()
const toast = useToast()
// Instantiate the app store at boot so theme + accent are applied immediately,
// regardless of which route mounts first (its watchers run on creation).
useAppStore()
const { enabled, initConsoleCapture } = useDebugLog()
const showRecovery = ref(false)

function applyFontSize(size: number) {
  document.documentElement.style.setProperty('--editor-font-size', size + 'px')
}

// UI zoom scales the whole interface by setting the root font-size; all rem-based
// Tailwind/DaisyUI sizing follows it. 16 = browser default = no change. The editor
// body text uses the absolute px var --editor-font-size, so it stays independent.
function applyUiFontSize(size: number) {
  const px = Math.min(Math.max(Number(size) || 16, 12), 25)
  document.documentElement.style.fontSize = px + 'px'
}

function applyDebug(enabled: boolean) {
  if (enabled) initConsoleCapture()
}

// 新手导览 / 版本更新说明。恢复对话框在场时推迟到其关闭后再弹，避免叠层。
// lastSeenVersion 每次启动都跟进当前版本，补丁版不重复打扰。
const tour = useTour()
let bootTourDone = false
function maybeStartBootTour() {
  if (bootTourDone || showRecovery.value) return
  bootTourDone = true
  const cur = __APP_VERSION__
  const prevSeen = settings.settings.lastSeenVersion
  // lastSeenVersion 是新字段，存量用户首次升上来时也是空的——用「加载过剧情」
  // 区分：真新人走欢迎导览，老用户只看版本更新说明，不被完整导览打扰。
  const isExistingUser = !!settings.settings.lastStoryType
  if (!tour.seen('app-welcome') && !isExistingUser) {
    tour.startOnce(appWelcomeTour())
  } else if (prevSeen !== cur) {
    const wn = whatsNewTour(cur)
    if (wn) tour.startOnce(wn)
  }
  if (prevSeen !== cur) {
    settings.settings.lastSeenVersion = cur
    settings.saveSettings().catch(() => {})
  }
}

// 插件功能介绍：安装后第一次进入该插件页面时弹一次。路径经
// registry.routesByPlugin 反查归属插件，宿主与插件都不用硬编码路由。
const pluginRegistry = usePluginRegistry()
const appRouter = useRouter()
appRouter.afterEach((to) => {
  const attributedPlugin = to.meta.sekaiPluginId
  if (typeof attributedPlugin === 'string') {
    const intro = pluginIntroTour(attributedPlugin)
    if (intro) tour.startOnce(intro)
    return
  }
  for (const [pluginId, paths] of Object.entries(pluginRegistry.routesByPlugin)) {
    if (paths.includes(to.path)) {
      const intro = pluginIntroTour(pluginId)
      if (intro) tour.startOnce(intro)
      break
    }
  }
})

watch(() => settings.settings.fontSize, applyFontSize, { immediate: true })
watch(() => settings.settings.uiFontSize, applyUiFontSize, { immediate: true })
watch(() => settings.settings.debugEnabled, (v) => { enabled.value = v; applyDebug(v) })

onMounted(async () => {
  // 打包应用里禁用 WebView 原生右键菜单（Back / Reload）：误触 Back 会整页跳走、
  // Reload 会丢掉未落盘状态。自定义右键菜单（编辑器换括号等）自带 contextmenu
  // 监听，不受影响；网页开发环境保留原生菜单方便检查元素。
  if ((window as any).__TAURI_INTERNALS__) {
    document.addEventListener('contextmenu', (e) => e.preventDefault())
  }
  // Recovery is cleared on normal exit by the Tauri shell in RELEASE (Rust sends
  // DELETE /api/v1/recovery/clear on RunEvent::Exit — on macOS an Apple-event quit
  // fires ONLY Exit, not ExitRequested, so Exit is the single reliable quit hook).
  // A beforeunload sendBeacon can't reliably reach a custom-scheme backend,
  // so it's gone there. In DEV (dev:web / dev:tauri talk to the backend over TCP and
  // have no Rust quit hook), keep a dev-only beacon so a clean exit still clears
  // recovery. In-app explicit "clear recovery" actions still go through the api client.
  if (import.meta.env.DEV) {
    window.addEventListener('beforeunload', () => {
      // Only clear when nothing is dirty: a dev reload (Cmd+R) with unsaved
      // edits unloads the page — the edits die with it, and clearing here would
      // destroy the autosave too, i.e. the only remaining copy.
      const ed = useEditorStore()
      if (!ed.hasAnyUnsaved()) navigator.sendBeacon(`${ORIGIN}/api/v1/recovery/clear`)
    })
  }
  try {
    await settings.fetchSettings()
  } catch {
    // backend not available, use defaults
  }
  enabled.value = settings.settings.debugEnabled
  applyDebug(settings.settings.debugEnabled)
  applyFontSize(settings.settings.fontSize)
  applyUiFontSize(settings.settings.uiFontSize)

  // Check for autosave recovery
  try {
    const recovery = await api.recoveryLoad()
    if (hasRecovery(recovery)) {
      showRecovery.value = true
    }
  } catch {
    // backend not available, skip
  }

  // Plugin startup performs its update before autoload so stale code cannot
  // race a valid replacement and roll it back. Only surface its result here.
  void pluginStartupResult().then((sum) => {
    if (sum && sum.updated?.length) {
      const names = sum.updated.map((p) => p.name || p.id).join('、')
      toast.show(`已自动更新并加载 ${sum.updated.length} 个插件（${names}）`, 'success', 6000)
    }
  }).catch(() => {})
  void appUpdate.check()

  // Re-check for app updates when the user refocuses the window (throttled), so a
  // long-running session surfaces a new release without needing a restart.
  const recheck = () => { void appUpdate.maybeRecheck() }
  window.addEventListener('focus', recheck)
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') recheck()
  })

  maybeStartBootTour()
})
</script>

<style>
.page-shift-enter-active,
.page-shift-leave-active {
  transition: opacity 150ms ease, transform 220ms var(--ease-out);
}
.page-shift-enter-from {
  opacity: 0;
  transform: translateY(0.35rem) scale(0.997);
}
.page-shift-leave-to {
  opacity: 0;
  transform: translateY(-0.15rem) scale(0.999);
}
@media (prefers-reduced-motion: reduce) {
  .page-shift-enter-active,
  .page-shift-leave-active { transition: none; }
}
</style>
