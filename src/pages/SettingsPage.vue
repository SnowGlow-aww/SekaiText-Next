<script setup lang="ts">
import { ref, computed, onMounted, onActivated, onDeactivated, onUnmounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Palette, SlidersHorizontal, Download, Save, Wifi, Keyboard, FolderOpen, Puzzle, Blocks, Info, RotateCcw, FileUp, Store, Trash2, Globe, Github, Compass } from 'lucide-vue-next'
import { rebaseSettings, useSettingsStore } from '../stores/settings'
import { useEditorStore } from '../stores/editor'
import { useAppUpdateStore } from '../stores/appUpdate'
import { useToast } from '../composables/useToast'
import { useConfirm } from '../composables/useConfirm'
import { api } from '../api/client'
import { SHORTCUT_ACTIONS, resolveCombo, formatCombo, comboFromEvent } from '../constants/shortcuts'
import { usePluginRegistry } from '../plugin-host/registry'
import { usePluginsStore } from '../stores/plugins'
import ThemePicker from '../components/ui/ThemePicker.vue'
import SkSelect from '../components/ui/SkSelect.vue'
import { openExternal, LINKS } from '../utils/openExternal'
import { useTour } from '../onboarding/useTour'
import { appWelcomeTour } from '../onboarding/tours'
import { useFileDialog } from '../composables/useFileDialog'
import AppPageHeader from '../components/ui/AppPageHeader.vue'
import type { Settings } from '../types/api'

const router = useRouter()
const settings = useSettingsStore()
const editor = useEditorStore()
const toast = useToast()
const { confirm } = useConfirm()
const pluginRegistry = usePluginRegistry()
const plugins = usePluginsStore()
const appUpdate = useAppUpdateStore()

const appVersion = __APP_VERSION__
const checking = ref(false)
const saving = ref(false)
const draft = ref<Settings>(settings.createDraft())
const draftBase = ref<Settings>(settings.createDraft())

function resetDraft() {
  const latest = settings.createDraft()
  draft.value = latest
  draftBase.value = settings.createDraft()
}

function draftIsDirty(): boolean {
  return JSON.stringify(draft.value) !== JSON.stringify(draftBase.value)
}

// App.vue loads persisted settings asynchronously. A cold navigation directly
// to /settings mounts this page with defaults first; refresh the untouched draft
// when that load completes so Save cannot overwrite persisted configuration.
watch(() => settings.loading, (loading, wasLoading) => {
  if (!wasLoading || loading) return
  if (!draftIsDirty()) {
    resetDraft()
    return
  }
  const latest = settings.createDraft()
  draft.value = rebaseSettings(draftBase.value, draft.value, latest)
  draftBase.value = latest
})

const settingsSections = [
  { id: 'settings-appearance', label: '外观', icon: Palette },
  { id: 'settings-editor', label: '编辑器', icon: SlidersHorizontal },
  { id: 'settings-files', label: '保存与下载', icon: Save },
  { id: 'settings-network', label: '网络与调试', icon: Wifi },
  { id: 'settings-shortcuts', label: '快捷键', icon: Keyboard },
  { id: 'settings-local', label: '本地文件', icon: FolderOpen },
  { id: 'settings-plugins', label: '插件', icon: Puzzle },
  { id: 'settings-about', label: '关于', icon: Info },
]
const activeSettingsSection = ref(settingsSections[0].id)
const settingsPage = ref<HTMLElement | null>(null)
let settingsScrollRaf = 0
let scrollingToSettingsSection: string | null = null
let settingsScrollUnlockTimer: ReturnType<typeof setTimeout> | null = null

function settingsNavigationOffset(): number {
  const pageTop = settingsPage.value?.getBoundingClientRect().top ?? 0
  const headerBottom = document.querySelector<HTMLElement>('.app-page-header')?.getBoundingClientRect().bottom ?? 0
  const compactNav = document.querySelector<HTMLElement>('[data-settings-nav="compact"]')
  const compactBottom = compactNav && compactNav.getBoundingClientRect().height > 0
    ? compactNav.getBoundingClientRect().bottom
    : 0
  return Math.ceil(Math.max(headerBottom, compactBottom) - pageTop + 8)
}

function updateActiveSettingsSection() {
  settingsScrollRaf = 0
  if (scrollingToSettingsSection) return

  const scroller = settingsPage.value
  if (!scroller) return
  const atPageBottom = scroller.scrollTop + scroller.clientHeight >= scroller.scrollHeight - 2
  if (atPageBottom) {
    activeSettingsSection.value = settingsSections[settingsSections.length - 1].id
    return
  }

  const threshold = settingsNavigationOffset()
  let current = settingsSections[0].id
  for (const section of settingsSections) {
    const el = document.getElementById(section.id)
    if (el && el.getBoundingClientRect().top <= threshold) current = section.id
  }
  activeSettingsSection.value = current
}

function onSettingsScroll() {
  if (scrollingToSettingsSection) {
    if (settingsScrollUnlockTimer) window.clearTimeout(settingsScrollUnlockTimer)
    settingsScrollUnlockTimer = window.setTimeout(() => {
      scrollingToSettingsSection = null
      settingsScrollUnlockTimer = null
    }, 160)
    return
  }
  if (!settingsScrollRaf) settingsScrollRaf = window.requestAnimationFrame(updateActiveSettingsSection)
}

function jumpToSettingsSection(id: string) {
  const el = document.getElementById(id)
  if (!el) return

  activeSettingsSection.value = id
  scrollingToSettingsSection = id
  if (settingsScrollUnlockTimer) window.clearTimeout(settingsScrollUnlockTimer)
  settingsScrollUnlockTimer = window.setTimeout(() => {
    scrollingToSettingsSection = null
    settingsScrollUnlockTimer = null
  }, 800)

  const scroller = settingsPage.value
  if (!scroller) return
  const top = scroller.scrollTop + el.getBoundingClientRect().top - scroller.getBoundingClientRect().top - settingsNavigationOffset()
  const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  scroller.scrollTo({ top: Math.max(0, top), behavior: reduceMotion ? 'auto' : 'smooth' })
}

// 更新与插件市场的下载渠道；所选源优先、另一侧自动兜底（后端 routeDownloadURL）。
const downloadMirrorOptions = [
  { value: 'cdn', label: '国内 CDN 加速（默认）' },
  { value: 'github', label: 'GitHub 直连' },
]

async function checkUpdate() {
  if (checking.value) return
  checking.value = true
  try {
    const r = await appUpdate.check({ manual: true })
    if (r === 'available') {
      toast.show(`发现新版本 v${appUpdate.info?.latest ?? ''}，见顶部更新提示`, 'success', 5000)
    } else if (r === 'latest') {
      toast.show('已是最新版本', 'success')
    } else {
      toast.show('检查更新失败，请稍后再试', 'error')
    }
  } finally {
    checking.value = false
  }
}

// Load the installed-plugins listing for the management panel and start each
// settings visit from the last successfully persisted state.
onMounted(() => {
  resetDraft()
  plugins.refresh().catch(() => {})
})

async function togglePlugin(id: string, local: boolean, event: Event) {
  const input = event.target as HTMLInputElement
  const enabled = input.checked
  if (enabled && local && !(await confirm({
    title: '启用本地插件？',
    message: '本地插件未经过官方签名验证，将获得与应用相同的完整权限。',
    detail: '它可以读取应用数据、访问文件和网络并执行任意代码。仅在你信任插件来源及作者时启用。',
    tone: 'danger',
    confirmText: '我了解风险，启用',
  }))) {
    input.checked = false
    return
  }
  try {
    await plugins.setEnabled(id, enabled, enabled && local)
    toast.show(enabled ? '插件已启用' : '插件已禁用', 'success')
  } catch (e: any) {
    toast.show('操作失败: ' + (e?.message || '未知错误'), 'error')
    plugins.refresh().catch(() => {})
  }
}

async function uninstallPlugin(id: string, name: string) {
  if (!(await confirm({
    title: '卸载插件',
    message: `确定卸载插件「${name}」？`,
    detail: '此操作会删除其文件，可在插件市场重新安装。',
    tone: 'danger',
    confirmText: '卸载',
  }))) return
  try {
    await plugins.uninstall(id)
    toast.show('插件已卸载', 'success')
  } catch (e: any) {
    toast.show('卸载失败: ' + (e?.message || '未知错误'), 'error')
  }
}

const isTauri = typeof window !== 'undefined' && !!(window as any).__TAURI_INTERNALS__
const { pickDirectory } = useFileDialog()

async function browseJsonDownloadDir() {
  const dir = await pickDirectory('选择下载页默认目录')
  if (dir) draft.value.jsonDownloadDir = dir
}

// 更换文稿保存位置：后端把旧根目录下已生成的内容整体迁移到新位置（同名文件
// 不覆盖）并立即持久化设置；随后把编辑器各模式已绑定的文档路径改写到新根，
// 否则 autosave 会把译文写回旧位置。还没生成过任何文稿时等价于纯切换。
async function changeSaveBaseDir() {
  const dir = await pickDirectory('选择文稿保存位置')
  if (!dir || dir === draft.value.saveBaseDir) return
  const from = draft.value.saveBaseDir || '默认位置'
  if (!(await confirm({
    title: '更换文稿保存位置',
    message: `将把现有文稿从「${from}」迁移到「${dir}」，之后的自动保存也会落在新位置。`,
    detail: '同名文件不会被覆盖。',
    tone: 'primary',
    confirmText: '迁移',
  }))) return
  try {
    const res = await settings.migrateSaveDir(dir, committed => {
      // Keep draft publication and every open-document rebind inside the shared
      // migration transaction; no queued autosave can observe a half-migrated UI.
      draft.value.saveBaseDir = committed.newDir
      draftBase.value.saveBaseDir = committed.newDir
      // 被跳过（同名冲突）的文件仍在旧目录原处——把这些相对路径传给 rebindPaths，
      // 让对应文档的绑定继续指向旧文件，别改到新根那个同名的陌生文件（数据安全）。
      editor.rebindPaths(committed.oldDir, committed.newDir, committed.skippedPaths)
    })
    toast.show(res.moved > 0 ? `已迁移 ${res.moved} 项到新位置` : '已切换保存位置', 'success')
    if (res.skipped > 0) toast.show(`${res.skipped} 项因目标已存在同名文件被跳过，仍保留在原位置`, 'warn')
  } catch (e: any) {
    toast.show('迁移失败: ' + (e.message || '未知错误'), 'error')
  }
}

async function openSaveDirNow() {
  try {
    await api.openSaveDir()
  } catch (e: any) {
    toast.show('打开失败: ' + (e.message || '未知错误'), 'error')
  }
}

// 重看新手导览：回到主界面再启动（导览锚点都在编辑器页）。
const tour = useTour()
function restartTour() {
  router.push('/')
  tour.start(appWelcomeTour())
}

// 角色头像材质：状态即 chr-custom 目录是否存在，无设置项。
const chrIcon = ref<{ active: boolean; count: number }>({ active: false, count: 0 })
const chrIconBusy = ref(false)
onMounted(() => { api.chrIconCustomStatus().then((s) => { chrIcon.value = s }).catch(() => {}) })

async function importChrIcons() {
  if (!isTauri || chrIconBusy.value) return
  const { open } = await import('@tauri-apps/plugin-dialog')
  const dir = await open({ title: '选择头像材质文件夹', directory: true })
  if (!dir) return
  chrIconBusy.value = true
  try {
    chrIcon.value = await api.chrIconCustomImport(dir as string)
    toast.show(`已导入 ${chrIcon.value.count} 张头像`, 'success')
  } catch (e: any) {
    toast.show('导入失败: ' + (e?.message || '未知错误'), 'error')
  } finally {
    chrIconBusy.value = false
  }
}

async function resetChrIcons() {
  if (chrIconBusy.value) return
  chrIconBusy.value = true
  try {
    chrIcon.value = await api.chrIconCustomReset()
    toast.show('已恢复默认头像', 'success')
  } catch (e: any) {
    toast.show('操作失败: ' + (e?.message || '未知错误'), 'error')
  } finally {
    chrIconBusy.value = false
  }
}

async function installPluginFromFile() {
  if (!isTauri) {
    toast.show('从文件安装仅在桌面版可用', 'info')
    return
  }
  try {
    const { open } = await import('@tauri-apps/plugin-dialog')
    const path = await open({
      title: '选择插件包',
      filters: [{ name: 'SekaiText 插件包', extensions: ['sekplugin'] }],
    })
    if (!path) return
    const id = await plugins.installFromPath(path as string)
    toast.show(`插件「${id}」已安装并保持禁用；请确认来源后手动启用`, 'success', 6000)
  } catch (e: any) {
    toast.show('安装失败: ' + (e?.message || '未知错误'), 'error')
  }
}

// Open the app's writable data directory in Finder/Explorer (downloaded JSON,
// Live2D asset mirror, recovery files, etc. all live under it).
async function openDataDir() {
  try {
    await api.openDataDir()
  } catch {
    toast.show('打开失败', 'error')
  }
}

async function saveAndBack() {
  if (saving.value || settings.loading) return
  saving.value = true
  try {
    await settings.saveSettings(draft.value, draftBase.value)
    toast.show('设置已保存', 'success')
    await router.push('/')
  } catch (e: any) {
    toast.show('保存失败: ' + (e?.message || '未知错误'), 'error')
  } finally {
    saving.value = false
  }
}

function cancelAndBack() {
  resetDraft()
  router.push('/')
}

// ---- Shortcut customization ----
const recordingId = ref<string | null>(null)
function comboFor(id: string): string {
  return resolveCombo(draft.value.shortcuts, id)
}
// combo -> list of action ids, to flag conflicts (same combo bound twice).
const comboCounts = computed(() => {
  const m: Record<string, number> = {}
  for (const a of SHORTCUT_ACTIONS) {
    const c = comboFor(a.id)
    m[c] = (m[c] || 0) + 1
  }
  return m
})
function isConflict(id: string): boolean {
  return comboCounts.value[comboFor(id)] > 1
}
function startRecord(id: string) {
  recordingId.value = id
}
function onRecordKey(e: KeyboardEvent) {
  if (!recordingId.value) return
  e.preventDefault()
  if (e.key === 'Escape') { recordingId.value = null; return }
  const combo = comboFromEvent(e)
  if (!combo) return // pure modifier; keep waiting
  if (!draft.value.shortcuts) draft.value.shortcuts = {}
  draft.value.shortcuts[recordingId.value] = combo
  recordingId.value = null
}
function resetShortcut(id: string) {
  if (draft.value.shortcuts) delete draft.value.shortcuts[id]
}
function resetAllShortcuts() {
  draft.value.shortcuts = {}
}

// Capture the next keystroke globally while recording.
watch(recordingId, (id) => {
  if (id) window.addEventListener('keydown', onRecordKey, true)
  else window.removeEventListener('keydown', onRecordKey, true)
})
function attachSettingsNavigation() {
  settingsPage.value?.addEventListener('scroll', onSettingsScroll, { passive: true })
  // Router scroll restoration runs alongside keep-alive activation. Wait one
  // extra frame so the highlighted category reflects the final scroll offset,
  // not the cached position from before navigation.
  settingsScrollRaf = window.requestAnimationFrame(() => {
    settingsScrollRaf = window.requestAnimationFrame(updateActiveSettingsSection)
  })
}
function detachSettingsNavigation() {
  settingsPage.value?.removeEventListener('scroll', onSettingsScroll)
  if (settingsScrollRaf) window.cancelAnimationFrame(settingsScrollRaf)
  if (settingsScrollUnlockTimer) window.clearTimeout(settingsScrollUnlockTimer)
  settingsScrollRaf = 0
  settingsScrollUnlockTimer = null
  scrollingToSettingsSection = null
}
onActivated(() => {
  resetDraft()
  attachSettingsNavigation()
})
onDeactivated(() => {
  recordingId.value = null
  detachSettingsNavigation()
})
onUnmounted(() => {
  window.removeEventListener('keydown', onRecordKey, true)
  detachSettingsNavigation()
})
</script>


<template>
  <div ref="settingsPage" class="h-full min-h-0 overflow-y-auto page-bg text-[var(--color-text)]">
    <AppPageHeader title="设置" subtitle="调整界面、编辑体验与本地文件行为" width="6xl">
      <button @click="saveAndBack()" :disabled="saving || settings.loading" class="btn btn-sm btn-brand">{{ saving ? '保存中…' : '保存并返回' }}</button>
    </AppPageHeader>

    <main class="max-w-6xl mx-auto px-6 py-7">
      <nav
        data-settings-nav="compact"
        class="lg:hidden sticky top-16 z-[var(--z-sticky)] -mx-2 mb-5 px-2 py-2 flex gap-1 overflow-x-auto rounded-xl border border-[var(--color-border)] bg-[color-mix(in_oklch,var(--color-bg)_88%,transparent)] backdrop-blur-md"
        aria-label="设置分类"
      >
        <button
          v-for="section in settingsSections"
          :key="`compact-${section.id}`"
          class="settings-nav-link w-auto shrink-0"
          :class="{ 'is-active': activeSettingsSection === section.id }"
          :aria-current="activeSettingsSection === section.id ? 'location' : undefined"
          @click="jumpToSettingsSection(section.id)"
        >
          <component :is="section.icon" :size="15" />
          <span>{{ section.label }}</span>
        </button>
      </nav>

      <div class="grid grid-cols-1 lg:grid-cols-[10.5rem_minmax(0,1fr)] gap-7 items-start">
        <aside class="hidden lg:block sticky top-24">
          <div class="section-eyebrow px-2 mb-2">设置分类</div>
          <nav class="space-y-0.5" aria-label="设置分类">
            <button
              v-for="section in settingsSections"
              :key="section.id"
              class="settings-nav-link"
              :class="{ 'is-active': activeSettingsSection === section.id }"
              :aria-current="activeSettingsSection === section.id ? 'location' : undefined"
              @click="jumpToSettingsSection(section.id)"
            >
              <component :is="section.icon" :size="15" />
              <span>{{ section.label }}</span>
            </button>
          </nav>
        </aside>

        <div class="min-w-0 space-y-4">

      <!-- ====== 外观 ====== -->
      <section id="settings-appearance" class="app-card p-5 scroll-mt-24" data-tour="set-appearance">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-primary/12 text-primary"><Palette :size="15" /></span>
          <div class="section-title">外观</div>
        </div>
        <ThemePicker :settings-model="draft" />

        <div class="mt-4 pt-4 border-t border-[var(--color-border)] flex items-center justify-between gap-3">
          <div>
            <div class="text-sm font-medium">角色头像材质</div>
            <div class="app-help mt-0.5">
              {{ chrIcon.active ? `已使用自定义头像（${chrIcon.count} 张）` : '选择包含 chr_1.png ~ chr_31.png 的文件夹，替换编辑器角色头像' }}
            </div>
          </div>
          <div class="flex items-center gap-2 shrink-0">
            <button
              @click="importChrIcons"
              :disabled="!isTauri || chrIconBusy"
              :title="isTauri ? '' : '仅桌面端可用'"
              class="btn btn-sm btn-ghost border border-[var(--color-border)]"
            >
              <FolderOpen :size="15" /> 选择文件夹
            </button>
            <button
              v-if="chrIcon.active"
              @click="resetChrIcons"
              :disabled="chrIconBusy"
              class="btn btn-sm btn-ghost border border-[var(--color-border)]"
            >
              <RotateCcw :size="15" /> 恢复默认
            </button>
          </div>
        </div>
      </section>

      <!-- ====== 编辑器 ====== -->
      <section id="settings-editor" class="app-card p-5 scroll-mt-24" data-tour="set-editor">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-info/12 text-info"><SlidersHorizontal :size="15" /></span>
          <div class="section-title">编辑器</div>
        </div>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-4">
          <div class="flex items-center justify-between gap-3">
            <div>
              <div class="text-sm font-medium">字号</div>
              <div class="app-help mt-0.5">编辑器文本显示大小</div>
            </div>
            <div class="flex items-center gap-2 shrink-0">
              <input v-model.number="draft.fontSize" type="range" min="10" max="48" step="1" class="range range-primary range-xs w-28" />
              <span class="text-sm w-8 text-center font-mono">{{ draft.fontSize }}</span>
            </div>
          </div>

          <div class="flex items-center justify-between gap-3">
            <div>
              <div class="text-sm font-medium">撤销深度</div>
              <div class="app-help mt-0.5">Ctrl+Z/Y 可撤销/重做的最大次数</div>
            </div>
            <div class="flex items-center gap-2 shrink-0">
              <input v-model.number="draft.undoDepth" type="range" min="1" max="100" step="1" class="range range-primary range-xs w-28" />
              <span class="text-sm w-8 text-center font-mono">{{ draft.undoDepth }}</span>
            </div>
          </div>

          <div class="flex items-center justify-between gap-3">
            <div>
              <div class="text-sm font-medium">索引排序</div>
              <div class="app-help mt-0.5">故事索引下拉列表的显示顺序</div>
            </div>
            <SkSelect
              class="w-44 shrink-0"
              :model-value="draft.indexOrder"
              @update:model-value="draft.indexOrder = $event as 'asc' | 'desc'"
              :options="[{ value: 'desc', label: '降序（最新的在底部）' }, { value: 'asc', label: '升序（最新的在顶部）' }]"
            />
          </div>

          <label class="flex items-center justify-between gap-3 cursor-pointer">
            <div>
              <div class="text-sm font-medium">关闭对比时保留高亮</div>
              <div class="app-help mt-0.5">关闭对比后，校对/合意的改动处仍以绿色标出</div>
            </div>
            <input v-model="draft.keepHighlightWhenCompareOff" type="checkbox" class="toggle toggle-primary toggle-sm" />
          </label>

          <label class="flex items-center justify-between gap-3 cursor-pointer">
            <div>
              <div class="text-sm font-medium">进入合意模式时提示导入顺序</div>
              <div class="app-help mt-0.5">提醒先导入翻译稿再导入校对稿</div>
            </div>
            <input :checked="!draft.hideAgreementImportHint" @change="draft.hideAgreementImportHint = !($event.target as HTMLInputElement).checked" type="checkbox" class="toggle toggle-primary toggle-sm" />
          </label>
        </div>
      </section>

      <!-- ====== 下载 ====== -->
      <section id="settings-files" class="app-card p-5 scroll-mt-24">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-secondary/12 text-secondary"><Download :size="15" /></span>
          <div class="section-title">下载</div>
        </div>
        <label class="app-label">下载页默认目录</label>
        <p class="app-help mt-0.5 mb-2">专用下载页面 (/download) 的默认保存位置</p>
        <div class="flex gap-2">
          <input
            v-model="draft.jsonDownloadDir"
            type="text"
            placeholder="./downloads/json"
            class="app-input flex-1"
          />
          <button v-if="isTauri" @click="browseJsonDownloadDir" class="btn btn-sm btn-ghost border border-[var(--color-border)] whitespace-nowrap">
            <FolderOpen :size="15" /> 浏览
          </button>
        </div>
      </section>

      <!-- ====== 文稿保存路径 ====== -->
      <section class="app-card p-5">
        <div class="flex items-center gap-2 mb-1.5">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-primary/12 text-primary"><FolderOpen :size="15" /></span>
          <div class="section-title">文稿保存路径</div>
        </div>
        <p class="app-help mb-3">译文自动建档与保存的根目录，按 <span class="font-mono">故事类型/索引/【模式】标题.txt</span> 自动分级归档；点「保存」与自动保存都落在这里。</p>
        <div class="flex gap-2">
          <input
            :value="draft.saveBaseDir"
            type="text"
            readonly
            placeholder="默认 ~/Documents/SekaiText"
            class="app-input flex-1 cursor-default"
          />
          <button v-if="isTauri" @click="changeSaveBaseDir" class="btn btn-sm btn-brand whitespace-nowrap">
            <FolderOpen :size="15" /> 更换位置
          </button>
          <button v-if="isTauri" @click="openSaveDirNow" class="btn btn-sm btn-ghost border border-[var(--color-border)] whitespace-nowrap">
            打开
          </button>
        </div>
        <div class="app-help mt-1.5">更换位置时会把已生成的文稿自动迁移到新目录（同名文件不覆盖）；还没生成过则直接在新位置建档。</div>
      </section>

      <!-- ====== 文件保存 ====== -->
      <section class="app-card p-5">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-success/12 text-success"><Save :size="15" /></span>
          <div class="section-title">文件保存</div>
        </div>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-4">
          <label class="flex items-center justify-between gap-3 cursor-pointer">
            <div>
              <div class="text-sm font-medium">保存 \N 换行符</div>
              <div class="app-help mt-0.5">翻译文件中保留 \N 换行标记</div>
            </div>
            <input v-model="draft.saveN" type="checkbox" class="toggle toggle-primary toggle-sm" />
          </label>

        </div>
      </section>

      <!-- ====== 网络与调试 ====== -->
      <section id="settings-network" class="app-card p-5 scroll-mt-24" data-tour="set-network">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-accent/12 text-accent"><Wifi :size="15" /></span>
          <div class="section-title">网络与调试</div>
        </div>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-4">
          <div class="flex items-center justify-between gap-3">
            <div>
              <div class="text-sm font-medium">下载源</div>
              <div class="app-help mt-0.5">故事 JSON 数据来源</div>
            </div>
            <span class="text-sm text-[var(--color-text-secondary)] shrink-0">HarukiBot NEO</span>
          </div>

          <div class="flex items-center justify-between gap-3">
            <div>
              <div class="text-sm font-medium">更新与插件下载源</div>
              <div class="app-help mt-0.5">应用更新、插件市场走哪个渠道；海外或 CDN 异常时选 GitHub 直连</div>
            </div>
            <SkSelect
              class="w-[200px] shrink-0"
              :model-value="draft.downloadMirror || 'cdn'"
              @update:model-value="draft.downloadMirror = $event as string"
              :options="downloadMirrorOptions"
            />
          </div>

          <label class="flex items-center justify-between gap-3 cursor-pointer">
            <div>
              <div class="text-sm font-medium">调试日志</div>
              <div class="app-help mt-0.5">在底部显示调试日志窗口</div>
            </div>
            <input v-model="draft.debugEnabled" type="checkbox" class="toggle toggle-primary toggle-sm" />
          </label>
        </div>
      </section>

      <!-- ====== 快捷键 ====== -->
      <section id="settings-shortcuts" class="app-card p-5 scroll-mt-24" data-tour="set-shortcuts">
        <div class="flex items-center justify-between mb-4">
          <div class="flex items-center gap-2">
            <span class="grid place-items-center w-7 h-7 rounded-lg bg-primary/12 text-primary"><Keyboard :size="15" /></span>
            <div class="section-title">快捷键</div>
          </div>
          <button @click="resetAllShortcuts" class="btn btn-ghost btn-xs">全部恢复默认</button>
        </div>
        <div class="space-y-2.5">
          <div v-for="a in SHORTCUT_ACTIONS" :key="a.id" class="flex items-center justify-between gap-3">
            <div class="min-w-0">
              <div class="text-sm font-medium">{{ a.label }}</div>
              <div v-if="a.note" class="app-help mt-0.5">{{ a.note }}</div>
            </div>
            <div class="flex items-center gap-2 flex-shrink-0">
              <span v-if="isConflict(a.id)" class="app-chip bg-error/15 text-error">冲突</span>
              <button
                @click="startRecord(a.id)"
                class="min-w-[72px] px-2.5 py-1 rounded-[var(--radius-control)] border text-xs font-mono transition-colors"
                :class="recordingId === a.id
                  ? 'border-[var(--color-primary)] text-[var(--color-primary)] animate-pulse'
                  : 'border-[var(--color-border)] text-[var(--color-text)] hover:border-[var(--color-primary)]'"
              >{{ recordingId === a.id ? '按下按键…' : formatCombo(comboFor(a.id)) }}</button>
              <button @click="resetShortcut(a.id)" title="恢复默认" class="icon-btn"><RotateCcw :size="14" /></button>
            </div>
          </div>
          <div class="app-help pt-1">点击键位按钮后按下新组合键录制</div>
        </div>
      </section>

      <!-- ====== 本地文件 ====== -->
      <section id="settings-local" class="app-card p-5 scroll-mt-24">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-info/12 text-info"><FolderOpen :size="15" /></span>
          <div class="section-title">本地文件</div>
        </div>
        <div class="flex items-center justify-between gap-3">
          <div>
            <div class="text-sm font-medium">应用数据文件夹</div>
            <div class="app-help mt-0.5">下载的剧情 JSON、Live2D 本地素材库、自动恢复文件都在此处</div>
          </div>
          <button @click="openDataDir" class="btn btn-sm btn-ghost border border-[var(--color-border)] shrink-0">
            <FolderOpen :size="15" /> 打开文件夹
          </button>
        </div>
      </section>

      <!-- ====== 插件管理 ====== -->
      <section id="settings-plugins" class="app-card p-5 scroll-mt-24">
        <div class="flex items-center justify-between mb-4">
          <div class="flex items-center gap-2">
            <span class="grid place-items-center w-7 h-7 rounded-lg bg-secondary/12 text-secondary"><Puzzle :size="15" /></span>
            <div class="section-title">插件</div>
            <button @click="router.push('/market')" class="btn btn-ghost btn-xs gap-1 text-primary"><Store :size="13" />插件市场</button>
          </div>
          <button @click="installPluginFromFile" class="btn btn-ghost btn-xs gap-1"><FileUp :size="13" />从文件安装</button>
        </div>
        <p class="app-help mb-3">本地及旧版未验证插件默认禁用；可确认风险后重新授权，或从插件市场重新安装官方验证包。</p>
        <div v-if="plugins.loading" class="flex items-center gap-2 py-2 text-sm text-[var(--color-text-secondary)]">
          <span class="loading loading-spinner loading-sm" /> 加载中…
        </div>
        <div v-else-if="plugins.list.length === 0" class="flex flex-col items-center gap-2 py-8 text-center text-[var(--color-text-tertiary)]">
          <Puzzle :size="28" class="opacity-50" />
          <span class="text-sm">暂无已安装插件</span>
        </div>
        <div v-else class="space-y-2">
          <div
            v-for="p in plugins.list"
            :key="p.id"
            class="flex items-center justify-between gap-4 rounded-[var(--radius-control)] border border-[var(--color-border)] bg-[var(--color-bg)] px-3 py-2.5 transition-colors hover:border-[var(--color-border-strong)]"
          >
            <div class="min-w-0">
              <div class="flex items-center gap-2">
                <span class="text-sm font-medium truncate">{{ p.name || p.id }}</span>
                <span class="text-xs text-[var(--color-text-tertiary)] font-mono">v{{ p.version }}</span>
                <span v-if="p.local" class="app-chip bg-warning/12 text-warning">未验证，需确认</span>
              </div>
              <div v-if="p.description" class="app-help mt-0.5 truncate">{{ p.description }}</div>
            </div>
            <div class="flex items-center gap-3 flex-shrink-0">
              <button
                @click="uninstallPlugin(p.id, p.name || p.id)"
                :disabled="plugins.busyId === p.id"
                class="btn btn-ghost btn-xs gap-1 text-error hover:bg-error/10"
                title="卸载"
              >
                <Trash2 :size="13" /><span class="hidden sm:inline">卸载</span>
              </button>
              <input
                type="checkbox"
                class="toggle toggle-primary toggle-sm"
                :checked="p.enabled"
                :disabled="plugins.busyId === p.id"
                @change="togglePlugin(p.id, p.local, $event)"
              />
            </div>
          </div>
        </div>
      </section>

      <!-- ====== 插件贡献的设置区块 ====== -->
      <section v-for="sec in pluginRegistry.settingsSections" :key="`${sec.pluginId}:${sec.id}`" class="app-card p-5">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-accent/12 text-accent"><Blocks :size="15" /></span>
          <div class="section-title">{{ sec.title }}</div>
        </div>
        <component :is="sec.component" />
      </section>

      <!-- ====== 关于 ====== -->
      <section id="settings-about" class="app-card p-5 scroll-mt-24">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-success/12 text-success"><Info :size="15" /></span>
          <div class="section-title">关于</div>
        </div>
        <div class="text-sm font-medium">SekaiText Next by 雪莹ちゃん</div>
        <div class="flex items-center flex-wrap gap-2 mt-2" data-tour="set-about">
          <span class="app-help font-mono mr-1">v{{ appVersion }}</span>
          <button @click="checkUpdate" :disabled="checking"
            class="btn btn-xs btn-ghost border border-[var(--color-border)] gap-1">
            <RotateCcw :size="12" :class="checking ? 'animate-spin' : ''" />
            {{ checking ? '检查中…' : '检查更新' }}
          </button>
          <button @click="openExternal(LINKS.website)" class="btn btn-xs btn-ghost border border-[var(--color-border)] gap-1">
            <Globe :size="12" /> 官网
          </button>
          <button @click="openExternal(LINKS.github)" class="btn btn-xs btn-ghost border border-[var(--color-border)] gap-1">
            <Github :size="12" /> GitHub
          </button>
          <button @click="restartTour" class="btn btn-xs btn-ghost border border-[var(--color-border)] gap-1">
            <Compass :size="12" /> 新手导览
          </button>
        </div>
      </section>

      <div class="flex justify-end gap-2 border-t border-[var(--color-border)] pt-6">
        <button @click="cancelAndBack" :disabled="saving" class="btn btn-sm btn-ghost border border-[var(--color-border)]">取消</button>
        <button @click="saveAndBack()" :disabled="saving || settings.loading" class="btn btn-sm btn-brand">{{ saving ? '保存中…' : '保存设置' }}</button>
      </div>
        </div>
      </div>
    </main>
  </div>
</template>
