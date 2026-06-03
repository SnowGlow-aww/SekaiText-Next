<template>
  <router-view v-slot="{ Component }">
    <keep-alive>
      <component :is="Component" />
    </keep-alive>
  </router-view>
  <Toast />
  <DownloadFloat />
  <RecoveryDialog
    v-if="showRecovery"
    @restore="showRecovery = false"
    @discard="showRecovery = false"
  />
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { useSettingsStore } from './stores/settings'
import { useDebugLog } from './composables/useDebugLog'
import { api } from './api/client'
import Toast from './components/Toast.vue'
import DownloadFloat from './components/DownloadFloat.vue'
import RecoveryDialog from './components/RecoveryDialog.vue'

const settings = useSettingsStore()
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
  // Clear recovery file on normal exit so only crashes leave it behind
  window.addEventListener('beforeunload', () => {
    navigator.sendBeacon('/api/v1/recovery/clear', '')
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
})
</script>
