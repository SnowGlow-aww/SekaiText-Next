import { defineStore } from 'pinia'
import { useLocalStorage, usePreferredDark } from '@vueuse/core'
import { computed, ref, watch } from 'vue'

export type ThemeMode = 'system' | 'light' | 'dark'

const THEME_STORAGE_KEY = 'sekaitext-theme-mode'
const ACCENT_STORAGE_KEY = 'sekaitext-accent'

// The default 'rainbow' accent maps to a fixed PJSK multicolour gradient; any
// other value is a character 代表色 hex ('#rrggbb'). The teal below (Miku) is the
// solid colour rainbow mode uses for primary controls.
// Light pastel multicolour so the gradient's dark text (accent-content below)
// stays legible across every stop — the deep-blue/purple version washed out the
// label on CTAs.
const RAINBOW_GRADIENT =
  'linear-gradient(135deg,#5fdccb 0%,#8fa6f5 28%,#c9a0f0 50%,#ff9ec9 73%,#ffc06b 100%)'

// Pick black-ish vs white text for a given accent so labels on primary buttons
// stay legible across the whole pastel-to-saturated PJSK range (WCAG relative
// luminance, threshold tuned for these colours).
function contrastContent(hex: string): string {
  const h = hex.replace('#', '')
  if (h.length < 6) return '#ffffff'
  const ch = (i: number) => parseInt(h.slice(i, i + 2), 16) / 255
  const lin = (c: number) => (c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4))
  const L = 0.2126 * lin(ch(0)) + 0.7152 * lin(ch(2)) + 0.0722 * lin(ch(4))
  return L > 0.55 ? '#15131f' : '#ffffff'
}

function applyAccent(value: string) {
  const root = document.documentElement
  if (value === 'rainbow') {
    root.style.setProperty('--accent', '#33ccbb')
    root.style.setProperty('--accent-content', '#12203a')
    root.style.setProperty('--brand-gradient', RAINBOW_GRADIENT)
    return
  }
  root.style.setProperty('--accent', value)
  root.style.setProperty('--accent-content', contrastContent(value))
  // Drop the inline override so the CSS-derived (accent-based) gradient applies.
  root.style.removeProperty('--brand-gradient')
}

const FONT_STORAGE_KEY = 'sekaitext-font'

// App-wide UI font choices. Each maps to a CSS font-family stack applied to
// <html> via --app-font-family (style.css body falls back to the default stack
// when unset). Only system-available fonts — nothing is bundled, so unavailable
// families just fall through to the next in the stack.
export const FONT_OPTIONS: { value: string; label: string; stack: string }[] = [
  { value: 'default', label: '默认', stack: '' },
  { value: 'system', label: '系统 UI', stack: 'system-ui,-apple-system,"Segoe UI",sans-serif' },
  { value: 'pingfang', label: '苹方', stack: '"PingFang SC","PingFang TC","Hiragino Sans GB",sans-serif' },
  { value: 'yahei', label: '微软雅黑', stack: '"Microsoft YaHei","微软雅黑",sans-serif' },
  { value: 'sourcehan', label: '思源黑体', stack: '"Source Han Sans SC","Noto Sans CJK SC","思源黑体",sans-serif' },
  { value: 'yuanti', label: '圆体', stack: '"Yuanti SC","YouYuan","幼圆",sans-serif' },
  { value: 'kaiti', label: '楷体', stack: '"Kaiti SC","KaiTi","楷体",serif' },
  { value: 'songti', label: '宋体', stack: '"Songti SC","SimSun","宋体",serif' },
  { value: 'mono', label: '等宽', stack: 'ui-monospace,"SF Mono",Menlo,Consolas,"Courier New",monospace' },
]

function applyFont(value: string) {
  const root = document.documentElement
  const opt = FONT_OPTIONS.find((o) => o.value === value)
  if (!opt || !opt.stack) root.style.removeProperty('--app-font-family')
  else root.style.setProperty('--app-font-family', opt.stack)
}

export const useAppStore = defineStore('app', () => {
  const fontSize = ref(18)
  const editorMode = ref<0 | 1 | 2>(0)
  const showFlashback = ref(true)
  // showGlossary: when on, source text in the editor highlights matched glossary
  // terms and hovering shows the translation + appellation suggestion. Default
  // on, and persisted (user can turn it off via the toolbar checkbox).
  const showGlossary = useLocalStorage('sekaitext-show-glossary', true)
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

  // accentColor: 'rainbow' (default PJSK multicolour) or a character 代表色 hex.
  const accentColor = useLocalStorage(ACCENT_STORAGE_KEY, 'rainbow')
  function setAccent(value: string) {
    accentColor.value = value
  }

  // fontFamily: app-wide UI font (see FONT_OPTIONS). 'default' uses the base stack.
  const fontFamily = useLocalStorage(FONT_STORAGE_KEY, 'default')
  function setFont(value: string) {
    fontFamily.value = value
  }

  function applyTheme(dark: boolean) {
    // Keep the legacy .dark class for existing `dark:` utility classes, and set
    // daisyUI's data-theme so its components (and the bridged --color-* vars)
    // switch between the hand-tuned sekai-light / sekai-dark themes.
    document.documentElement.classList.toggle('dark', dark)
    document.documentElement.setAttribute('data-theme', dark ? 'sekai-dark' : 'sekai-light')
  }

  watch(isDark, applyTheme, { immediate: true })
  watch(accentColor, applyAccent, { immediate: true })
  watch(fontFamily, applyFont, { immediate: true })

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
    showGlossary,
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
    accentColor,
    setAccent,
    fontFamily,
    setFont,
    setEditorMode,
  }
})
