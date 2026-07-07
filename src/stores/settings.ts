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
    // Left empty on purpose: a hardcoded default would be developer-specific
    // and invalid on the user's machine (and on Windows). Resolved per-user at
    // runtime — the JSON download handler falls back to {DataDir}/json and the
    // save flow falls back to the OS save-dialog default location when empty.
    jsonDownloadDir: '',
    saveBaseDir: '',
    preserveStoryOnModeSwitch: true,
    undoDepth: 20,
    keepHighlightWhenCompareOff: true,

    indexOrder: 'asc',
    shortcuts: {},
    hideAgreementImportHint: false,
    live2dPosition: 'window',
  })
  const loading = ref(false)

  async function fetchSettings() {
    loading.value = true
    try {
      const s = await api.getSettings()
      // Migrate configs saved before uiFontSize existed (absent → 0): keep the
      // browser-default 16px so the UI doesn't collapse to a 0px root font.
      if (!s.uiFontSize) s.uiFontSize = 16
      // Default the Live2D dock to a standalone window for pre-existing configs.
      if (!s.live2dPosition) s.live2dPosition = 'window'
      // The 右侧 (right) placement option was retired; migrate any saved 'right'
      // to the standalone window so the removed dropdown option can't strand the
      // layout (or render a blank select for that now-unknown value).
      if (s.live2dPosition === 'right') s.live2dPosition = 'window'
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
