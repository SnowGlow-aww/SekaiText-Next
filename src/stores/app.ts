import { defineStore } from 'pinia'
import { useLocalStorage, usePreferredDark } from '@vueuse/core'
import { computed, ref, watch } from 'vue'
import { DEFAULT_ACCENT } from '../data/characterColors'
import { idbGet, idbPut, idbDel } from '../lib/idb'
import { useSettingsStore } from './settings'

export type ThemeMode = 'system' | 'light' | 'dark'

const THEME_STORAGE_KEY = 'sekaitext-theme-mode'
const ACCENT_STORAGE_KEY = 'sekaitext-accent'

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
  // Legacy migration: the removed 'rainbow' default falls back to the default accent.
  const hex = value === 'rainbow' ? DEFAULT_ACCENT : value
  root.style.setProperty('--accent', hex)
  root.style.setProperty('--accent-content', contrastContent(hex))
}

const FONT_STORAGE_KEY = 'sekaitext-font'
const CUSTOM_FONTS_KEY = 'sekaitext-custom-fonts'

// Appended after a user-imported family so glyphs it lacks (e.g. CJK in a
// latin-only font) fall through gracefully instead of showing tofu.
const CUSTOM_FONT_FALLBACK = "'PingFang SC','Microsoft YaHei',system-ui,sans-serif"

// id -> the unique CSS family name we registered the imported font under.
const customFamilies = new Map<string, string>()

// Register an imported font blob with the document so CSS can reference it.
// No-op if already registered (boot + re-import are idempotent).
async function registerCustomFont(id: string, blob: Blob): Promise<void> {
  if (customFamilies.has(id)) return
  const family = `SekaiUserFont-${id}`
  const face = new FontFace(family, await blob.arrayBuffer())
  await face.load()
  document.fonts.add(face)
  customFamilies.set(id, family)
}

// App-wide UI font choices. Each maps to a CSS font-family stack applied to
// <html> via --app-font-family (style.css body falls back to the default stack
// when unset). The 'default' entry leaves --app-font-family unset, so the body
// inherits the bundled 荆南麦圆体 (Jingnan Maiyuan) @font-face from style.css;
// the rest are system families that fall through if unavailable.
export const FONT_OPTIONS: { value: string; label: string; stack: string }[] = [
  { value: 'default', label: '荆南麦圆体 · 默认', stack: '' },
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
  const builtin = FONT_OPTIONS.find((o) => o.value === value)
  if (builtin) {
    if (builtin.stack) root.style.setProperty('--app-font-family', builtin.stack)
    else root.style.removeProperty('--app-font-family')
    return
  }
  // Custom imported font (value is its id). If not yet registered (boot races
  // the async IDB load), fall back to default; boot re-applies once registered.
  const family = customFamilies.get(value)
  if (family) root.style.setProperty('--app-font-family', `"${family}",${CUSTOM_FONT_FALLBACK}`)
  else root.style.removeProperty('--app-font-family')
}

// ── Personalised background image ──────────────────────────────────────────
const BG_ENABLED_KEY = 'sekaitext-bg-enabled'
const BG_VEIL_KEY = 'sekaitext-bg-veil'
const BG_BLUR_KEY = 'sekaitext-bg-blur'
// Readability floor: the theme-tinted veil over the wallpaper never drops below
// this opacity (%), so text on --color-bg surfaces always keeps contrast.
export const BG_VEIL_MIN = 60
// Surfaces (cards / panels / the full-bleed editor) are kept more opaque than
// the page veil so dense translated text never loses contrast over a wallpaper.
// Driven by the same slider as --bg-veil, just floored higher.
const SURFACE_VEIL_MIN = 86

let bgUrl = '' // current object URL for the wallpaper blob

function applyBgVeil(v: number) {
  const root = document.documentElement.style
  root.setProperty('--bg-veil', Math.max(BG_VEIL_MIN, v) + '%')
  root.setProperty('--surface-veil', Math.max(SURFACE_VEIL_MIN, v) + '%')
}
function applyBgBlur(px: number) {
  document.documentElement.style.setProperty('--bg-blur', Math.max(0, px) + 'px')
}
// Point the fixed background layer at a blob and return its object URL (for the
// picker's preview thumbnail). Revokes the previous URL to avoid leaks.
function showBackground(blob: Blob): string {
  if (bgUrl) URL.revokeObjectURL(bgUrl)
  bgUrl = URL.createObjectURL(blob)
  const root = document.documentElement
  root.style.setProperty('--bg-image', `url("${bgUrl}")`)
  root.setAttribute('data-bg', 'on')
  return bgUrl
}
function hideBackground() {
  if (bgUrl) {
    URL.revokeObjectURL(bgUrl)
    bgUrl = ''
  }
  const root = document.documentElement
  root.removeAttribute('data-bg')
  root.style.removeProperty('--bg-image')
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
  // saveN mirrors the persisted 保存\N toggle held by the settings store, which is
  // what the 设置 page writes. Every save path reads app.saveN, so proxy the setting
  // here instead of keeping a separate ref that never got synced. The settings store
  // is resolved lazily inside the getter to avoid a store init cycle at boot.
  const saveN = computed(() => useSettingsStore().settings.saveN)
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

  // accentColor: a character 代表色 hex; defaults to Miku's DEFAULT_ACCENT.
  const accentColor = useLocalStorage(ACCENT_STORAGE_KEY, DEFAULT_ACCENT)
  // Migrate legacy accents to the current default (Miku 代表色): the removed
  // 'rainbow' gradient and the removed brand-violet default both fall through.
  if (accentColor.value === 'rainbow' || accentColor.value.toLowerCase() === '#6c4cff') {
    accentColor.value = DEFAULT_ACCENT
  }
  function setAccent(value: string) {
    accentColor.value = value
  }

  // fontFamily: app-wide UI font — a FONT_OPTIONS value, or a custom font id.
  const fontFamily = useLocalStorage(FONT_STORAGE_KEY, 'default')
  function setFont(value: string) {
    fontFamily.value = value
  }
  // customFonts: user-imported families (blob lives in IndexedDB under font:<id>).
  const customFonts = useLocalStorage<{ id: string; label: string }[]>(CUSTOM_FONTS_KEY, [])
  async function importFont(file: File) {
    const id = String(Date.now())
    await registerCustomFont(id, file) // throws on an invalid/unsupported font
    await idbPut(`font:${id}`, file)
    const label = file.name.replace(/\.[^.]+$/, '').trim() || '自定义字体'
    customFonts.value = [...customFonts.value, { id, label }]
    setFont(id)
  }
  async function removeCustomFont(id: string) {
    await idbDel(`font:${id}`)
    customFonts.value = customFonts.value.filter((f) => f.id !== id)
    customFamilies.delete(id)
    if (fontFamily.value === id) setFont('default')
  }

  // Background wallpaper. bgEnabled gates the fixed image layer; veil/blur tune
  // readability (veil floored at BG_VEIL_MIN). Blob lives in IDB under bg:image.
  const bgEnabled = useLocalStorage(BG_ENABLED_KEY, false)
  const bgVeil = useLocalStorage(BG_VEIL_KEY, 82)
  const bgBlur = useLocalStorage(BG_BLUR_KEY, 2)
  const bgThumb = ref('') // object URL for the picker preview
  async function importBackground(file: File) {
    if (!file.type.startsWith('image/')) throw new Error('not an image')
    await idbPut('bg:image', file)
    bgEnabled.value = true
    bgThumb.value = showBackground(file)
  }
  function removeBackground() {
    void idbDel('bg:image')
    bgEnabled.value = false
    bgThumb.value = ''
    hideBackground()
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
  watch(bgVeil, applyBgVeil, { immediate: true })
  watch(bgBlur, applyBgBlur, { immediate: true })

  // Restore imported fonts + wallpaper from IndexedDB on boot. Runs async after
  // the synchronous watchers above have applied defaults; a font/image that
  // fails to load is dropped rather than blocking the rest.
  async function restorePersonalization() {
    for (const f of customFonts.value) {
      try {
        const blob = await idbGet(`font:${f.id}`)
        if (blob) await registerCustomFont(f.id, blob)
      } catch {
        /* skip one bad font */
      }
    }
    // The active font may have been a custom one not yet registered when the
    // immediate watcher first ran — re-apply now that families are loaded.
    if (!FONT_OPTIONS.some((o) => o.value === fontFamily.value)) applyFont(fontFamily.value)
    if (bgEnabled.value) {
      try {
        const blob = await idbGet('bg:image')
        if (blob) bgThumb.value = showBackground(blob)
        else bgEnabled.value = false
      } catch {
        bgEnabled.value = false
      }
    }
  }
  void restorePersonalization()

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
    customFonts,
    importFont,
    removeCustomFont,
    bgEnabled,
    bgVeil,
    bgBlur,
    bgThumb,
    importBackground,
    removeBackground,
    setEditorMode,
  }
})
