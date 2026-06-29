import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api/client'
import type { Settings } from '../types/api'

export const useSettingsStore = defineStore('settings', () => {
  const settings = ref<Settings>({
    fontSize: 18,
    uiFontSize: 16,
    downloadSource: 'haruki',
    saveN: true,
    saveVoice: false,
    disableSSL: false,
    debugEnabled: false,
    jsonDownloadDir: './downloads/json',
    saveBaseDir: '/Users/amia/Documents/Translation/PJS字幕组',
    preserveStoryOnModeSwitch: true,
    undoDepth: 20,
    keepHighlightWhenCompareOff: true,

    indexOrder: 'asc',
    shortcuts: {},
    hideAgreementImportHint: false,
    live2dPosition: 'right',
  })
  const loading = ref(false)

  async function fetchSettings() {
    loading.value = true
    try {
      const s = await api.getSettings()
      // Migrate configs saved before uiFontSize existed (absent → 0): keep the
      // browser-default 16px so the UI doesn't collapse to a 0px root font.
      if (!s.uiFontSize) s.uiFontSize = 16
      // Default the Live2D dock to the right edge for pre-existing configs.
      if (!s.live2dPosition) s.live2dPosition = 'right'
      settings.value = s
    } finally {
      loading.value = false
    }
  }

  async function saveSettings() {
    await api.putSettings(settings.value)
  }

  return { settings, loading, fetchSettings, saveSettings }
})
