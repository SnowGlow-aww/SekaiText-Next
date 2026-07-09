<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowLeft, Palette, SlidersHorizontal, Download, Save, Wifi, Keyboard, FolderOpen, Puzzle, Blocks, Info, RotateCcw, FileUp, Store, Trash2 } from 'lucide-vue-next'
import { useSettingsStore } from '../stores/settings'
import { useAppUpdateStore } from '../stores/appUpdate'
import { useToast } from '../composables/useToast'
import { useConfirm } from '../composables/useConfirm'
import { api } from '../api/client'
import { SHORTCUT_ACTIONS, resolveCombo, formatCombo, comboFromEvent } from '../constants/shortcuts'
import { usePluginRegistry } from '../plugin-host/registry'
import { usePluginsStore } from '../stores/plugins'
import ThemePicker from '../components/ui/ThemePicker.vue'
import SkSelect from '../components/ui/SkSelect.vue'

const router = useRouter()
const settings = useSettingsStore()
const toast = useToast()
const { confirm } = useConfirm()
const pluginRegistry = usePluginRegistry()
const plugins = usePluginsStore()
const appUpdate = useAppUpdateStore()

const appVersion = __APP_VERSION__
const checking = ref(false)

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

// Load the installed-plugins listing for the management panel.
onMounted(() => { plugins.refresh().catch(() => {}) })

async function togglePlugin(id: string, enabled: boolean) {
  try {
    await plugins.setEnabled(id, enabled)
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
      filters: [{ name: '插件包', extensions: ['sekplugin', 'zip'] }],
    })
    if (!path) return
    const id = await plugins.installFromPath(path as string)
    toast.show(`插件「${id}」安装成功`, 'success')
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

function saveAndBack() {
  settings.saveSettings().then(() => {
    toast.show('设置已保存', 'success')
    router.push('/')
  }).catch(() => {
    toast.show('保存失败', 'error')
  })
}

// ---- Shortcut customization ----
const recordingId = ref<string | null>(null)
function comboFor(id: string): string {
  return resolveCombo(settings.settings.shortcuts, id)
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
  if (!settings.settings.shortcuts) settings.settings.shortcuts = {}
  settings.settings.shortcuts[recordingId.value] = combo
  recordingId.value = null
}
function resetShortcut(id: string) {
  if (settings.settings.shortcuts) delete settings.settings.shortcuts[id]
}
function resetAllShortcuts() {
  settings.settings.shortcuts = {}
}

// Capture the next keystroke globally while recording.
import { watch, onUnmounted } from 'vue'
watch(recordingId, (id) => {
  if (id) window.addEventListener('keydown', onRecordKey, true)
  else window.removeEventListener('keydown', onRecordKey, true)
})
onUnmounted(() => window.removeEventListener('keydown', onRecordKey, true))
</script>


<template>
  <div class="min-h-screen bg-[var(--color-bg)] text-[var(--color-text)]">
    <header class="sticky top-0 z-[var(--z-sticky)] bg-[color-mix(in_oklch,var(--color-bg)_82%,transparent)] backdrop-blur-md border-b border-[var(--color-border)]">
      <div class="max-w-4xl mx-auto px-6 h-14 flex items-center gap-3">
        <button @click="router.push('/')" class="icon-btn -ml-1" title="返回编辑器"><ArrowLeft :size="18" /></button>
        <h1 class="text-base font-bold tracking-tight">设置</h1>
        <button @click="saveAndBack()" class="btn btn-sm btn-brand ml-auto">保存并返回</button>
      </div>
    </header>

    <main class="max-w-4xl mx-auto px-6 py-8 space-y-6">

      <!-- ====== 外观 ====== -->
      <section class="app-card p-5">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-primary/12 text-primary"><Palette :size="15" /></span>
          <div class="section-title">外观</div>
        </div>
        <ThemePicker />

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
      <section class="app-card p-5">
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
              <input v-model.number="settings.settings.fontSize" type="range" min="10" max="48" step="1" class="range range-primary range-xs w-28" />
              <span class="text-sm w-8 text-center font-mono">{{ settings.settings.fontSize }}</span>
            </div>
          </div>

          <div class="flex items-center justify-between gap-3">
            <div>
              <div class="text-sm font-medium">撤销深度</div>
              <div class="app-help mt-0.5">Ctrl+Z/Y 可撤销/重做的最大次数</div>
            </div>
            <div class="flex items-center gap-2 shrink-0">
              <input v-model.number="settings.settings.undoDepth" type="range" min="1" max="100" step="1" class="range range-primary range-xs w-28" />
              <span class="text-sm w-8 text-center font-mono">{{ settings.settings.undoDepth }}</span>
            </div>
          </div>

          <div class="flex items-center justify-between gap-3">
            <div>
              <div class="text-sm font-medium">索引排序</div>
              <div class="app-help mt-0.5">故事索引下拉列表的显示顺序</div>
            </div>
            <SkSelect
              class="w-44 shrink-0"
              :model-value="settings.settings.indexOrder"
              @update:model-value="settings.settings.indexOrder = $event as 'asc' | 'desc'"
              :options="[{ value: 'desc', label: '降序（最新的在底部）' }, { value: 'asc', label: '升序（最新的在顶部）' }]"
            />
          </div>

          <label class="flex items-center justify-between gap-3 cursor-pointer">
            <div>
              <div class="text-sm font-medium">切模式保留剧情</div>
              <div class="app-help mt-0.5">切换翻/校/合时保留当前译文</div>
            </div>
            <input v-model="settings.settings.preserveStoryOnModeSwitch" type="checkbox" class="toggle toggle-primary toggle-sm" />
          </label>

          <label class="flex items-center justify-between gap-3 cursor-pointer">
            <div>
              <div class="text-sm font-medium">关闭对比时保留高亮</div>
              <div class="app-help mt-0.5">关闭对比后，校对/合意的改动处仍以绿色标出</div>
            </div>
            <input v-model="settings.settings.keepHighlightWhenCompareOff" type="checkbox" class="toggle toggle-primary toggle-sm" />
          </label>

          <label class="flex items-center justify-between gap-3 cursor-pointer">
            <div>
              <div class="text-sm font-medium">进入合意模式时提示导入顺序</div>
              <div class="app-help mt-0.5">提醒先导入翻译稿再导入校对稿</div>
            </div>
            <input :checked="!settings.settings.hideAgreementImportHint" @change="settings.settings.hideAgreementImportHint = !($event.target as HTMLInputElement).checked" type="checkbox" class="toggle toggle-primary toggle-sm" />
          </label>
        </div>
      </section>

      <!-- ====== 下载 ====== -->
      <section class="app-card p-5">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-secondary/12 text-secondary"><Download :size="15" /></span>
          <div class="section-title">下载</div>
        </div>
        <label class="app-label">下载页默认目录</label>
        <p class="app-help mt-0.5 mb-2">专用下载页面 (/download) 的默认保存位置</p>
        <input
          v-model="settings.settings.jsonDownloadDir"
          type="text"
          placeholder="./downloads/json"
          class="app-input"
        />
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
            <input v-model="settings.settings.saveN" type="checkbox" class="toggle toggle-primary toggle-sm" />
          </label>

          <label class="flex items-center justify-between gap-3 cursor-pointer">
            <div>
              <div class="text-sm font-medium">保存语音文件</div>
              <div class="app-help mt-0.5">下载并保存语音文件到本地</div>
            </div>
            <input v-model="settings.settings.saveVoice" type="checkbox" class="toggle toggle-primary toggle-sm" />
          </label>

          <div class="sm:col-span-2">
            <label class="app-label">语音输出目录</label>
            <input
              v-model="settings.settings.voiceOutputDir"
              type="text"
              placeholder="留空使用默认目录"
              class="app-input mt-1.5"
            />
          </div>
        </div>
      </section>

      <!-- ====== 网络与调试 ====== -->
      <section class="app-card p-5">
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
              :model-value="settings.settings.downloadMirror || 'cdn'"
              @update:model-value="settings.settings.downloadMirror = $event as string"
              :options="downloadMirrorOptions"
            />
          </div>

          <label class="flex items-center justify-between gap-3 cursor-pointer">
            <div>
              <div class="text-sm font-medium">SSL 验证</div>
              <div class="app-help mt-0.5">禁用 SSL 证书验证（某些网络环境需要）</div>
            </div>
            <input v-model="settings.settings.disableSSL" type="checkbox" class="toggle toggle-primary toggle-sm" />
          </label>

          <label class="flex items-center justify-between gap-3 cursor-pointer">
            <div>
              <div class="text-sm font-medium">调试日志</div>
              <div class="app-help mt-0.5">在底部显示调试日志窗口</div>
            </div>
            <input v-model="settings.settings.debugEnabled" type="checkbox" class="toggle toggle-primary toggle-sm" />
          </label>
        </div>
      </section>

      <!-- ====== 快捷键 ====== -->
      <section class="app-card p-5">
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
      <section class="app-card p-5">
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
      <section class="app-card p-5">
        <div class="flex items-center justify-between mb-4">
          <div class="flex items-center gap-2">
            <span class="grid place-items-center w-7 h-7 rounded-lg bg-secondary/12 text-secondary"><Puzzle :size="15" /></span>
            <div class="section-title">插件</div>
            <button @click="router.push('/market')" class="btn btn-ghost btn-xs gap-1 text-primary"><Store :size="13" />插件市场</button>
          </div>
          <button @click="installPluginFromFile" class="btn btn-ghost btn-xs gap-1"><FileUp :size="13" />从文件安装</button>
        </div>
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
                @change="togglePlugin(p.id, ($event.target as HTMLInputElement).checked)"
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
      <section class="app-card p-5">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-success/12 text-success"><Info :size="15" /></span>
          <div class="section-title">关于</div>
        </div>
        <div class="text-sm font-medium">SekaiText Next by 雪莹ちゃん</div>
        <div class="flex items-center gap-3 mt-1">
          <span class="app-help font-mono">v{{ appVersion }}</span>
          <button @click="checkUpdate" :disabled="checking"
            class="btn btn-xs btn-ghost border border-[var(--color-border)] gap-1">
            <RotateCcw :size="12" :class="checking ? 'animate-spin' : ''" />
            {{ checking ? '检查中…' : '检查更新' }}
          </button>
        </div>
      </section>

      <div class="flex justify-end gap-2 border-t border-[var(--color-border)] pt-6">
        <button @click="router.push('/')" class="btn btn-sm btn-ghost border border-[var(--color-border)]">取消</button>
        <button @click="saveAndBack()" class="btn btn-sm btn-brand">保存设置</button>
      </div>
    </main>
  </div>
</template>
