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
import { useAppUpdateStore } from './stores/appUpdate'
import { useToast } from './composables/useToast'
import { useDebugLog } from './composables/useDebugLog'
import { api, BASE_URL } from './api/client'
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

function applyDebug(enabled: boolean) {
  if (enabled) initConsoleCapture()
}

watch(() => settings.settings.fontSize, applyFontSize, { immediate: true })
watch(() => settings.settings.debugEnabled, (v) => { enabled.value = v; applyDebug(v) })

onMounted(async () => {
  // Clear recovery file on normal exit so only crashes leave it behind.
  // Use the absolute backend origin: in the packaged Tauri app a relative
  // /api path resolves against tauri://localhost and the beacon never arrives.
  window.addEventListener('beforeunload', () => {
    navigator.sendBeacon(`${BASE_URL}/recovery/clear`, '')
  })

  try {
    await settings.fetchSettings()
  } catch {
    // backend not available, use defaults
  }
  enabled.value = settings.settings.debugEnabled
  applyDebug(settings.settings.debugEnabled)
  applyFontSize(settings.settings.fontSize)

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
})
</script>
