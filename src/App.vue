<template>
  <router-view v-slot="{ Component }">
    <keep-alive>
      <component :is="Component" />
    </keep-alive>
  </router-view>
  <Toast />
  <UpdateBanner />
  <DownloadFloat />
  <ConfirmHost />
  <RecoveryDialog
    v-if="showRecovery"
    @restore="showRecovery = false"
    @discard="showRecovery = false"
  />
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

const settings = useSettingsStore()
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

watch(() => settings.settings.fontSize, applyFontSize, { immediate: true })
watch(() => settings.settings.uiFontSize, applyUiFontSize, { immediate: true })
watch(() => settings.settings.debugEnabled, (v) => { enabled.value = v; applyDebug(v) })

onMounted(async () => {
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
    if (recovery.exists && recovery.content) {
      showRecovery.value = true
    }
  } catch {
    // backend not available, skip
  }

  // Auto-update (non-blocking): silently bring installed plugins up to date
  // (effective next launch) and check for a newer app version (→ UpdateBanner).
  // Both swallow offline/mirror failures so a cold start is never blocked.
  void appUpdate.autoUpdatePlugins().then((sum) => {
    if (sum && sum.updated?.length) {
      const names = sum.updated.map((p) => p.name || p.id).join('、')
      toast.show(`已自动更新 ${sum.updated.length} 个插件（${names}），重启后生效`, 'success', 6000)
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
})
</script>
