import { defineStore } from 'pinia'
import { useLocalStorage, usePreferredDark } from '@vueuse/core'
import { computed, ref, watch } from 'vue'

export type ThemeMode = 'system' | 'light' | 'dark'

const THEME_STORAGE_KEY = 'sekaitext-theme-mode'

export const useAppStore = defineStore('app', () => {
  const fontSize = ref(18)
  const editorMode = ref<0 | 1 | 2>(0)
  const showFlashback = ref(true)
  const syncScroll = ref(true)
  // showCompare: when on (校对/合意 only), each edited line shows a baseline row
  // (read-only, original/校对 text, removals in red) above the edit row (green
  // additions). Diff highlighting itself is always-on; this toggles the baseline row.
  const showCompare = ref(false)
  const saveN = ref(true)
  // Search / replace state (toolbar search bar).
  const searchOpen = ref(false)
  const searchQuery = ref('')
  const searchReplace = ref('')
  // Navigation: active match index and total, owned by EditorWorkspace which
  // computes the ordered match list; the toolbar buttons step the index.
  const searchActiveIndex = ref(0)
  const searchTotal = ref(0)
  const themeMode = useLocalStorage<ThemeMode>(THEME_STORAGE_KEY, 'system')
  const isSystemDark = usePreferredDark()
  const isDark = computed(() => themeMode.value === 'dark' || (themeMode.value === 'system' && isSystemDark.value))

  function applyTheme(dark: boolean) {
    document.documentElement.classList.toggle('dark', dark)
  }

  watch(isDark, applyTheme, { immediate: true })

  function setEditorMode(mode: 0 | 1 | 2) {
    editorMode.value = mode
    // 对比 follows the mode: on for 校对/合意, off for 翻译. This covers every
    // entry point (mode button, file open, recovery); the 对比 button can still
    // toggle it afterward within 校对/合意.
    showCompare.value = mode >= 1
  }

  return {
    fontSize,
    editorMode,
    showFlashback,
    syncScroll,
    showCompare,
    saveN,
    searchOpen,
    searchQuery,
    searchReplace,
    searchActiveIndex,
    searchTotal,
    themeMode,
    isDark,
    setEditorMode,
  }
})
