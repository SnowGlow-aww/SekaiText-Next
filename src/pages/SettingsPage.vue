<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowLeft } from 'lucide-vue-next'
import { useAppStore } from '../stores/app'
import { useSettingsStore } from '../stores/settings'
import { useToast } from '../composables/useToast'
import { api } from '../api/client'
import { SHORTCUT_ACTIONS, resolveCombo, formatCombo, comboFromEvent } from '../constants/shortcuts'
import { usePluginRegistry } from '../plugin-host/registry'
import { usePluginsStore } from '../stores/plugins'

const router = useRouter()
const app = useAppStore()
const settings = useSettingsStore()
const toast = useToast()
const pluginRegistry = usePluginRegistry()
const plugins = usePluginsStore()

const appVersion = __APP_VERSION__

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
  if (!confirm(`确定卸载插件「${name}」？此操作会删除其文件，可在插件市场重新安装。`)) return
  try {
    await plugins.uninstall(id)
    toast.show('插件已卸载', 'success')
  } catch (e: any) {
    toast.show('卸载失败: ' + (e?.message || '未知错误'), 'error')
  }
}

const isTauri = typeof window !== 'undefined' && !!(window as any).__TAURI_INTERNALS__

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
  <div class="min-h-screen bg-[var(--color-bg)]">
    <header class="border-b border-[var(--color-border)] bg-[var(--color-surface)] px-6 py-3 flex items-center justify-between">
      <div class="flex items-center gap-4">
        <button
          @click="router.push('/')"
          class="flex items-center gap-1.5 text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-text)] transition-colors"
        >
          <ArrowLeft :size="18" />
          返回编辑器
        </button>
        <span class="text-sm font-medium">设置</span>
      </div>
      <button
        @click="saveAndBack()"
        class="btn btn-primary btn-sm"
      >
        保存并返回
      </button>
    </header>

    <main class="p-6 max-w-5xl mx-auto">

      <!-- ====== 编辑器 ====== -->
      <section class="mb-6">
        <h2 class="text-xs font-semibold text-[var(--color-text-secondary)] uppercase tracking-wider mb-3 px-1">编辑器</h2>
        <div class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-xl p-6">
          <div class="grid grid-cols-2 gap-6">
            <div class="flex items-center justify-between">
              <div>
                <div class="text-sm font-medium">字号</div>
                <div class="text-xs text-[var(--color-text-secondary)] mt-0.5">编辑器文本显示大小</div>
              </div>
              <div class="flex items-center gap-2">
                <input v-model.number="settings.settings.fontSize" type="range" min="10" max="48" step="1" class="range range-primary range-xs w-28" />
                <span class="text-sm w-8 text-center font-mono">{{ settings.settings.fontSize }}</span>
              </div>
            </div>

            <div class="flex items-center justify-between">
              <div>
                <div class="text-sm font-medium">撤销深度</div>
                <div class="text-xs text-[var(--color-text-secondary)] mt-0.5">Ctrl+Z/Y 可撤销/重做的最大次数</div>
              </div>
              <div class="flex items-center gap-2">
                <input v-model.number="settings.settings.undoDepth" type="range" min="1" max="100" step="1" class="range range-primary range-xs w-28" />
                <span class="text-sm w-8 text-center font-mono">{{ settings.settings.undoDepth }}</span>
              </div>
            </div>

            <div class="flex items-center justify-between">
              <div>
                <div class="text-sm font-medium">索引排序</div>
                <div class="text-xs text-[var(--color-text-secondary)] mt-0.5">故事索引下拉列表的显示顺序</div>
              </div>
              <select v-model="settings.settings.indexOrder" class="select select-bordered select-sm w-44">
                <option value="desc">降序（最新的在底部）</option>
                <option value="asc">升序（最新的在顶部）</option>
              </select>
            </div>

            <label class="flex items-center justify-between cursor-pointer">
              <div>
                <div class="text-sm font-medium">切模式保留剧情</div>
                <div class="text-xs text-[var(--color-text-secondary)] mt-0.5">切换翻/校/合时保留当前译文</div>
              </div>
              <input v-model="settings.settings.preserveStoryOnModeSwitch" type="checkbox" class="toggle toggle-primary toggle-sm" />
            </label>

            <label class="flex items-center justify-between cursor-pointer">
              <div>
                <div class="text-sm font-medium">关闭对比时保留高亮</div>
                <div class="text-xs text-[var(--color-text-secondary)] mt-0.5">关闭对比后，校对/合意的改动处仍以绿色标出</div>
              </div>
              <input v-model="settings.settings.keepHighlightWhenCompareOff" type="checkbox" class="toggle toggle-primary toggle-sm" />
            </label>

            <label class="flex items-center justify-between cursor-pointer">
              <div>
                <div class="text-sm font-medium">进入合意模式时提示导入顺序</div>
                <div class="text-xs text-[var(--color-text-secondary)] mt-0.5">提醒先导入翻译稿再导入校对稿</div>
              </div>
              <input :checked="!settings.settings.hideAgreementImportHint" @change="settings.settings.hideAgreementImportHint = !($event.target as HTMLInputElement).checked" type="checkbox" class="toggle toggle-primary toggle-sm" />
            </label>
          </div>
        </div>
      </section>

      <!-- ====== 下载 + 外观 (左右各半, 等高) ====== -->
      <div class="grid grid-cols-2 gap-6 mb-6 items-stretch">
        <section class="flex flex-col">
          <h2 class="text-xs font-semibold text-[var(--color-text-secondary)] uppercase tracking-wider mb-3 px-1">下载</h2>
          <div class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-xl p-6 flex-1">
            <h3 class="text-sm font-semibold mb-1">下载页默认目录</h3>
            <p class="text-xs text-[var(--color-text-secondary)] mb-3">专用下载页面 (/download) 的默认保存位置</p>
            <input
              v-model="settings.settings.jsonDownloadDir"
              type="text"
              placeholder="./downloads/json"
              class="input input-bordered input-sm w-full"
            />
          </div>
        </section>

        <section class="flex flex-col">
          <h2 class="text-xs font-semibold text-[var(--color-text-secondary)] uppercase tracking-wider mb-3 px-1">外观</h2>
          <div class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-xl p-6 flex-1 flex items-center">
            <div class="flex items-center justify-between w-full">
              <div>
                <div class="text-sm font-medium">外观模式</div>
                <div class="text-xs text-[var(--color-text-secondary)] mt-0.5">跟随系统或手动指定亮暗色</div>
              </div>
              <select
                v-model="app.themeMode"
                class="select select-bordered select-sm w-44"
              >
                <option value="system">跟随系统</option>
                <option value="light">浅色</option>
                <option value="dark">深色</option>
              </select>
            </div>
          </div>
        </section>
      </div>

      <!-- ====== 文件保存 ====== -->
      <section class="mb-6">
        <h2 class="text-xs font-semibold text-[var(--color-text-secondary)] uppercase tracking-wider mb-3 px-1">文件保存</h2>
        <div class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-xl p-6">
          <div class="grid grid-cols-2 gap-6">
            <label class="flex items-center justify-between cursor-pointer">
              <div>
                <div class="text-sm font-medium">保存 \N 换行符</div>
                <div class="text-xs text-[var(--color-text-secondary)] mt-0.5">翻译文件中保留 \N 换行标记</div>
              </div>
              <input v-model="settings.settings.saveN" type="checkbox" class="toggle toggle-primary toggle-sm" />
            </label>

            <label class="flex items-center justify-between cursor-pointer">
              <div>
                <div class="text-sm font-medium">保存语音文件</div>
                <div class="text-xs text-[var(--color-text-secondary)] mt-0.5">下载并保存语音文件到本地</div>
              </div>
              <input v-model="settings.settings.saveVoice" type="checkbox" class="toggle toggle-primary toggle-sm" />
            </label>

            <div class="col-span-2">
              <div class="text-sm font-medium mb-1">语音输出目录</div>
              <input
                v-model="settings.settings.voiceOutputDir"
                type="text"
                placeholder="留空使用默认目录"
                class="input input-bordered input-sm w-full"
              />
            </div>
          </div>
        </div>
      </section>

      <!-- ====== 网络与调试 ====== -->
      <section class="mb-6">
        <h2 class="text-xs font-semibold text-[var(--color-text-secondary)] uppercase tracking-wider mb-3 px-1">网络与调试</h2>
        <div class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-xl p-6">
          <div class="grid grid-cols-2 gap-6">
            <div class="flex items-center justify-between">
              <div>
                <div class="text-sm font-medium">下载源</div>
                <div class="text-xs text-[var(--color-text-secondary)] mt-0.5">故事 JSON 数据来源</div>
              </div>
              <span class="text-sm text-[var(--color-text-secondary)]">HarukiBot NEO</span>
            </div>

            <label class="flex items-center justify-between cursor-pointer">
              <div>
                <div class="text-sm font-medium">SSL 验证</div>
                <div class="text-xs text-[var(--color-text-secondary)] mt-0.5">禁用 SSL 证书验证（某些网络环境需要）</div>
              </div>
              <input v-model="settings.settings.disableSSL" type="checkbox" class="toggle toggle-primary toggle-sm" />
            </label>

            <label class="flex items-center justify-between cursor-pointer">
              <div>
                <div class="text-sm font-medium">调试日志</div>
                <div class="text-xs text-[var(--color-text-secondary)] mt-0.5">在底部显示调试日志窗口</div>
              </div>
              <input v-model="settings.settings.debugEnabled" type="checkbox" class="toggle toggle-primary toggle-sm" />
            </label>
          </div>
        </div>
      </section>

      <section class="mb-6">
        <div class="flex items-center justify-between mb-3 px-1">
          <h2 class="text-xs font-semibold text-[var(--color-text-secondary)] uppercase tracking-wider">快捷键</h2>
          <button @click="resetAllShortcuts" class="text-xs text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]">全部恢复默认</button>
        </div>
        <div class="rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] p-4 space-y-2.5">
          <div v-for="a in SHORTCUT_ACTIONS" :key="a.id" class="flex items-center justify-between gap-3">
            <div class="min-w-0">
              <div class="text-sm font-medium">{{ a.label }}</div>
              <div v-if="a.note" class="text-xs text-[var(--color-text-secondary)] mt-0.5">{{ a.note }}</div>
            </div>
            <div class="flex items-center gap-2 flex-shrink-0">
              <span v-if="isConflict(a.id)" class="text-xs text-red-500">冲突</span>
              <button
                @click="startRecord(a.id)"
                class="min-w-[72px] px-2.5 py-1 rounded border text-xs font-mono transition-colors"
                :class="recordingId === a.id
                  ? 'border-[var(--color-primary)] text-[var(--color-primary)] animate-pulse'
                  : 'border-[var(--color-border)] text-[var(--color-text)] hover:border-[var(--color-primary)]'"
              >{{ recordingId === a.id ? '按下按键…' : formatCombo(comboFor(a.id)) }}</button>
              <button @click="resetShortcut(a.id)" title="恢复默认" class="text-xs text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]">↺</button>
            </div>
          </div>
          <div class="text-xs text-[var(--color-text-secondary)] pt-1">点击键位按钮后按下新组合键录制</div>
        </div>
      </section>

      <section class="mb-6">
        <h2 class="text-xs font-semibold text-[var(--color-text-secondary)] uppercase tracking-wider mb-3 px-1">本地文件</h2>
        <div class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-xl p-6">
          <div class="flex items-center justify-between">
            <div>
              <div class="text-sm font-medium">应用数据文件夹</div>
              <div class="text-xs text-[var(--color-text-secondary)] mt-0.5">下载的剧情 JSON、Live2D 本地素材库、自动恢复文件都在此处</div>
            </div>
            <button @click="openDataDir" class="btn btn-outline btn-sm">打开文件夹</button>
          </div>
        </div>
      </section>

      <!-- ====== 插件管理 ====== -->
      <section class="mb-6">
        <div class="flex items-center justify-between mb-3 px-1">
          <div class="flex items-center gap-3">
            <h2 class="text-xs font-semibold text-[var(--color-text-secondary)] uppercase tracking-wider">插件</h2>
            <button @click="router.push('/market')" class="text-xs text-[var(--color-primary)] hover:underline">插件市场</button>
          </div>
          <button
            @click="installPluginFromFile"
            class="text-xs text-[var(--color-primary)] hover:underline"
          >从文件安装</button>
        </div>
        <div class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-xl p-6">
          <div v-if="plugins.loading" class="text-sm text-[var(--color-text-secondary)]">加载中…</div>
          <div v-else-if="plugins.list.length === 0" class="text-sm text-[var(--color-text-secondary)]">暂无已安装插件</div>
          <div v-else class="space-y-3">
            <div v-for="p in plugins.list" :key="p.id" class="flex items-center justify-between gap-4">
              <div class="min-w-0">
                <div class="flex items-center gap-2">
                  <span class="text-sm font-medium truncate">{{ p.name || p.id }}</span>
                  <span class="text-xs text-[var(--color-text-secondary)] font-mono">v{{ p.version }}</span>
                </div>
                <div v-if="p.description" class="text-xs text-[var(--color-text-secondary)] mt-0.5 truncate">{{ p.description }}</div>
              </div>
              <div class="flex items-center gap-3 flex-shrink-0">
                <button
                  @click="uninstallPlugin(p.id, p.name || p.id)"
                  :disabled="plugins.busyId === p.id"
                  class="text-xs text-[var(--color-text-secondary)] hover:text-red-500 transition-colors"
                >卸载</button>
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
        </div>
      </section>

      <!-- ====== 插件贡献的设置区块 ====== -->
      <section v-for="sec in pluginRegistry.settingsSections" :key="sec.id" class="mb-6">
        <h2 class="text-xs font-semibold text-[var(--color-text-secondary)] uppercase tracking-wider mb-3 px-1">{{ sec.title }}</h2>
        <div class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-xl p-6">
          <component :is="sec.component" />
        </div>
      </section>

      <section class="mb-6">
        <h2 class="text-xs font-semibold text-[var(--color-text-secondary)] uppercase tracking-wider mb-3 px-1">关于</h2>
        <div class="rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] p-4 text-left">
          <div class="text-sm font-medium">SekaiText Next by 雪莹ちゃん</div>
          <div class="text-xs text-[var(--color-text-secondary)] mt-1 font-mono">v{{ appVersion }}</div>
        </div>
      </section>

      <div class="flex justify-end gap-2 border-t border-[var(--color-border)] pt-4">
        <button @click="router.push('/')" class="btn btn-ghost btn-sm">取消</button>
        <button @click="saveAndBack()" class="btn btn-primary btn-sm">保存设置</button>
      </div>
    </main>
  </div>
</template>
