<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useAppStore } from '../stores/app'
import { useEditorStore } from '../stores/editor'
import { useStoryStore } from '../stores/story'
import { EditorModeLabel } from '../types/translation'
import type { SaveMetadata } from '../types/api'
import { useSettingsStore } from '../stores/settings'
import { useToast } from '../composables/useToast'
import { useFileDialog } from '../composables/useFileDialog'
import { useAutoSave } from '../composables/useAutoSave'
import { useUndo } from '../composables/useUndo'
import { api } from '../api/client'
import { Pencil, Check, CircleDot, ChevronLeft, ChevronRight, Cog, Download, Bug } from 'lucide-vue-next'
import { getCurrentWindow } from '@tauri-apps/api/window'
import StoryNavigator from '../components/navigation/StoryNavigator.vue'
import EditorWorkspace from '../components/editor/EditorWorkspace.vue'
import SpeakerCountDialog from '../components/dialogs/SpeakerCountDialog.vue'
import SpeakerCheckDialog from '../components/dialogs/SpeakerCheckDialog.vue'

const app = useAppStore()
const editor = useEditorStore()
const story = useStoryStore()
const settings = useSettingsStore()
const toast = useToast()
const fileDialog = useFileDialog()
const autoSave = useAutoSave()
const undo = useUndo()

const isTauri = typeof window !== 'undefined' && !!(window as any).__TAURI_INTERNALS__

function onKeyDown(e: KeyboardEvent) {
  // Let browser handle native undo/redo inside contenteditable
  const el = document.activeElement
  if (el instanceof HTMLElement && el.isContentEditable) return

  if (e.ctrlKey && e.key === 'z') {
    e.preventDefault()
    const snap = undo.undo(editor.talks, editor.dstTalks)
    if (snap) {
      editor.talks = snap.talks
      editor.dstTalks = snap.dstTalks
      editor.markUnsaved()
    }
  }
  if (e.ctrlKey && e.key === 'y') {
    e.preventDefault()
    const snap = undo.redo(editor.talks, editor.dstTalks)
    if (snap) {
      editor.talks = snap.talks
      editor.dstTalks = snap.dstTalks
      editor.markUnsaved()
    }
  }
}

onMounted(async () => {
  autoSave.start()
  window.addEventListener('keydown', onKeyDown)
  if (!isTauri) return
  try {
    const win = getCurrentWindow()
    await win.onCloseRequested(async (event) => {
      if (forceClose.value) return
      if (editor.hasUnsavedChanges) {
        event.preventDefault()
        await new Promise(r => setTimeout(r, 0))
        showCloseConfirm.value = true
      }
    })
  } catch (e: any) {
    tauriErr.value = `init: ${e.message || e}`
  }
})

async function handleCloseSave() {
  try {
    await handleSave()
    if (!editor.hasUnsavedChanges) {
      showCloseConfirm.value = false
      forceClose.value = true
      await closeWindow()
    }
  } catch { /* Save failed */ }
  showCloseConfirm.value = false
}

async function handleCloseDiscard() {
  showCloseConfirm.value = false
  forceClose.value = true
  await closeWindow()
}

async function closeWindow() {
  try { await getCurrentWindow().destroy() } catch {
    try { await getCurrentWindow().close() } catch {}
  }
}

function handleCloseCancel() { showCloseConfirm.value = false }

const showSpeakerCount = ref(false)
const tauriErr = ref('')
const showSpeakerCheck = ref(false)
const showCloseConfirm = ref(false)
const forceClose = ref(false)
const sidebarOpen = ref(true)

function setMode(key: number) {
  editor.switchMode(key as 0 | 1 | 2)
  app.setEditorMode(key as 0 | 1 | 2)
  // 校对/合意 default to compare-on (baseline rows visible); 翻译 has no compare.
  app.showCompare = key >= 1
}

const modes = [ { key: 0, label: '翻译' }, { key: 1, label: '校对' }, { key: 2, label: '合意' } ]
const modeIcons: Record<number, typeof Pencil> = { 0: Pencil, 1: Check, 2: CircleDot }

async function handleOpen() {
  try {
    const result = await fileDialog.openTranslation()
    if (!result) return
    console.log('[Open] loaded file', { path: result.filePath || result.fileName, talkCount: result.talks.length, hasMeta: !!result.meta, mode: app.editorMode, fileMode: result.meta?.mode })
    editor.setTalks(result.talks, result.talks, [])

    // Mode isolation: a file whose saved mode differs from the current editor
    // mode is treated as a *baseline to derive from*, not a file to edit in
    // place. Clearing currentFilePath forces "save as" with the current mode's
    // 【…】 name, so the original (e.g. 翻译) file is never overwritten.
    const fileMode = (result.meta?.mode ?? 0) as 0 | 1 | 2
    const deriving = fileMode !== app.editorMode
    editor.currentFilePath = deriving ? '' : (result.filePath || result.fileName || '')
    editor.markSaved()
    undo.clear()
    if (result.meta) {
      try {
        const m = result.meta
        story.selectedType = m.type
        story.selectedSort = m.sort || ''
        story.selectedIndex = m.index
        story.selectedChapter = m.chapter
        story.selectedSource = m.source
        await story.loadStory()
        if (story.sourceTalks.length > 0) {
          const aligned = await api.checkLines({ sourceTalks: story.sourceTalks, loadedTalks: result.talks })
          if (deriving) {
            // Build refer(baseline, read-only) + check(editable copy) for diff.
            const compared = await api.compareText({ referTalks: aligned, checkTalks: aligned, editorMode: app.editorMode })
            editor.setTalks(compared.talks, compared.dstTalks, aligned)
          } else {
            // Resuming a 校对/合意 file in its own mode: seed each row's baseline
            // to its current text so compare shows nothing changed until edited,
            // and edits diff against the saved-on-open text. Restoring the diff
            // vs the original translation is a separate explicit action.
            if (app.editorMode >= 1) {
              for (const t of aligned) t.baseline = t.text
            }
            editor.setTalks(aligned, aligned, [])
          }
        }
      } catch { /* skip */ }
    }
    toast.show(deriving ? '已载入为' + EditorModeLabel[app.editorMode as 0|1|2] + '基线（请另存为新文件）' : '已打开: ' + editor.currentFilePath, 'success')
  } catch (e: any) { toast.show('Open failed: ' + (e.message || String(e)), 'error') }
}

async function handleSave() {
  if (editor.talks.length === 0) return
  const modeLabel = EditorModeLabel[app.editorMode as 0 | 1 | 2]
  // Always derive the suggested name from the CURRENT mode. Reusing
  // currentFilePath verbatim would keep a stale 【翻译】 prefix when saving
  // from 校对/合意 mode, so only reuse it when its prefix already matches.
  let defaultName = editor.currentFilePath
  const matchesMode = defaultName.includes('【' + modeLabel + '】')
  if (!defaultName || !matchesMode) {
    let fileName = '【' + modeLabel + '】' + (story.saveTitle || 'untitled')
    if (story.chapterTitle) fileName += ' ' + story.chapterTitle
    fileName += '.txt'
    // Layered output: <saveBaseDir>/<故事类型>/<索引名>/<【模式】标题.txt>
    const base = settings.settings.saveBaseDir
    if (isTauri && base && story.selectedType && story.selectedIndexLabel) {
      const sep = (s: string) => s.replace(/[/\\]/g, '_')
      defaultName = `${base}/${sep(story.selectedType)}/${sep(story.selectedIndexLabel)}/${fileName}`
    } else {
      defaultName = fileName
    }
  }
  const meta: SaveMetadata | undefined = story.selectedType ? {
    type: story.selectedType, sort: story.selectedSort, index: story.selectedIndex,
    chapter: story.selectedChapter, source: story.selectedSource, scenarioId: story.scenarioId,
    mode: app.editorMode,
  } : undefined
  console.log('[Save] starting save', { defaultName, talkCount: editor.talks.length, dstCount: editor.dstTalks.length, saveN: app.saveN, hasMeta: !!meta, isTauri: isTauri })
  try {
    const path = await fileDialog.saveTranslation(defaultName, editor.dstTalks, app.saveN, meta)
    if (!path) { console.log('[Save] cancelled by user'); return }
    editor.currentFilePath = path
    editor.markSaved()
    api.recoveryClear().catch(() => {})
    console.log('[Save] saved successfully', { path })
    toast.show('已保存', 'success')
  } catch (e: any) {
    const detail = e.status ? `${e.status}: ${e.message}` : (e.message || String(e))
    console.error('[Save] failed', { error: detail, defaultName, talkCount: editor.talks.length, isTauri })
    toast.show('Save failed: ' + detail, 'error')
  }
}

function handleClear() {
  if (editor.hasUnsavedChanges) { if (!confirm('有未保存的更改，确定清空吗？')) return }
  editor.clearAll()
  undo.clear()
  toast.show('已清空', 'info')
}

function handleConfirm() {
  if (editor.talks.length === 0) return
  if (!confirm('确认合意完成？所有差异将以当前译文为准。')) return
  undo.pushSnapshot(editor.talks, editor.dstTalks)
  // New model: confirming accepts every current text as the agreed baseline,
  // clearing all diffs. No row removal — row count is fixed.
  for (const talk of editor.talks) {
    talk.checked = true
    talk.baseline = talk.text
    talk.diff = undefined
  }
  for (const talk of editor.dstTalks) {
    talk.checked = true
    talk.baseline = talk.text
    talk.diff = undefined
  }
  editor.markUnsaved()
  toast.show('合意已确认', 'success')
}

// 合意: import a 校对稿 as the comparison baseline. compareText pairs the
// proofread rows (refer/baseline) with the current 合意 rows (check) by idx +
// sub-line position, sets each row's baseline and recomputes the diff so the
// comparison shows 校对稿(baseline, red deletions) vs current text(green adds).
async function handleImportBaseline() {
  if (editor.talks.length === 0) { toast.show('请先载入合意稿', 'warn'); return }
  try {
    const result = await fileDialog.openTranslation()
    if (!result) return
    undo.pushSnapshot(editor.talks, editor.dstTalks)
    const compared = await api.compareText({
      referTalks: result.talks,
      checkTalks: editor.talks,
      editorMode: 2,
    })
    editor.setTalks(compared.talks, compared.dstTalks, editor.referTalks)
    app.showCompare = true
    editor.markUnsaved()
    toast.show('已导入校对稿作为对比基准', 'success')
  } catch (e: any) {
    toast.show('导入校对稿失败: ' + (e.message || '未知错误'), 'error')
  }
}

// ---- Search / replace ----
// Match list + scrolling live in EditorWorkspace (it owns talkGroups); here we
// only show the counter, step the active index, and run replace-all on dest text.
const searchCount = computed(() => app.searchTotal === 0 ? (app.searchQuery ? '0/0' : '') : `${app.searchActiveIndex + 1}/${app.searchTotal}`)

function searchNext() {
  if (app.searchTotal === 0) return
  app.searchActiveIndex = (app.searchActiveIndex + 1) % app.searchTotal
}

function searchPrev() {
  if (app.searchTotal === 0) return
  app.searchActiveIndex = (app.searchActiveIndex - 1 + app.searchTotal) % app.searchTotal
}

async function handleReplaceAll() {
  const q = app.searchQuery.trim()
  if (!q) return
  const repl = app.searchReplace
  let changed = 0
  undo.pushSnapshot(editor.talks, editor.dstTalks)
  // Replace only in dest text (source & speaker are read-only). Route each
  // affected row through the backend so diff/baseline stay consistent.
  for (let i = 0; i < editor.talks.length; i++) {
    const t = editor.talks[i]
    if (t.text && t.text.includes(q) && t.save) {
      const newText = t.text.split(q).join(repl)
      try {
        const result = await api.changeText({
          row: i, text: newText, editorMode: app.editorMode,
          talks: editor.talks, dstTalks: editor.dstTalks, referTalks: editor.referTalks,
        })
        editor.setTalks(result.talks, result.dstTalks, editor.referTalks)
        changed++
      } catch { /* skip row */ }
    }
  }
  if (changed > 0) { editor.markUnsaved(); toast.show(`已替换 ${changed} 行`, 'success') }
  else toast.show('没有可替换的译文', 'warn')
}

function handleSpeakerBatchSave(speakers: { japanese: string; chinese: string }[]) {
  undo.pushSnapshot(editor.talks, editor.dstTalks)
  const map = new Map<string, string>()
  for (const s of speakers) {
    if (s.chinese && s.chinese !== s.japanese) map.set(s.japanese, s.chinese)
  }
  if (map.size === 0) return
  for (const talk of editor.talks) {
    if (map.has(talk.speaker)) talk.speaker = map.get(talk.speaker)!
  }
  for (const talk of editor.dstTalks) {
    if (map.has(talk.speaker)) talk.speaker = map.get(talk.speaker)!
  }
  editor.markUnsaved()
  toast.show('已批量修改 ' + map.size + ' 个说话人', 'success')
  showSpeakerCheck.value = false
}

async function handleFullCheck() {
  if (editor.talks.length === 0) return
  let hasIssues = false, msgs: string[] = []
  for (const talk of editor.talks) {
    if (!talk.checked && talk.save) { hasIssues = true; msgs.push(`行 ${talk.idx}: ${talk.text.split('\n')[0]}`) }
  }
  if (hasIssues) { toast.show('发现 ' + msgs.length + ' 个问题', 'error') }
  else { toast.show('全文检查通过', 'success') }
}
onUnmounted(() => {
  window.removeEventListener('keydown', onKeyDown)
})
</script>

<template>
  <div class="h-screen bg-[var(--color-bg)] flex flex-col">
    <div class="flex flex-1 min-h-0">
      <aside class="flex flex-col border-r border-[var(--color-border)] bg-[var(--color-surface)] flex-shrink-0 transition-all duration-200 overflow-hidden" :class="sidebarOpen ? 'w-36' : 'w-12'">
        <button @click="sidebarOpen = !sidebarOpen" class="flex items-center gap-2 h-10 px-3 text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors flex-shrink-0">
          <ChevronLeft v-if="sidebarOpen" :size="18"/><ChevronRight v-else :size="18"/>
          <span v-if="sidebarOpen" class="text-xs font-medium">模式</span>
        </button>
        <div class="border-b border-[var(--color-border)]" />
        <div class="flex flex-col gap-0.5 p-1.5">
          <button v-for="m in modes" :key="m.key" @click="setMode(m.key)" class="flex items-center gap-2.5 h-9 px-2 rounded-lg transition-colors text-sm flex-shrink-0" :class="app.editorMode === m.key ? 'bg-[var(--color-primary)]/10 text-[var(--color-primary)] font-medium' : 'text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]'">
            <component :is="modeIcons[m.key]" :size="18" /><span v-if="sidebarOpen" class="whitespace-nowrap">{{ m.label }}</span>
          </button>
        </div>
        <div class="flex-1" />
        <div class="border-t border-[var(--color-border)] p-1.5 space-y-0.5">
          <router-link to="/download" class="flex items-center gap-2.5 h-9 w-full px-2 rounded-lg transition-colors text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><Download :size="18"/><span v-if="sidebarOpen" class="whitespace-nowrap">下载</span></router-link>
          <router-link v-if="settings.settings.debugEnabled" to="/debug" class="flex items-center gap-2.5 h-9 w-full px-2 rounded-lg transition-colors text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><Bug :size="18"/><span v-if="sidebarOpen" class="whitespace-nowrap">调试</span></router-link>
          <router-link to="/settings" class="flex items-center gap-2.5 h-9 w-full px-2 rounded-lg transition-colors text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><Cog :size="18"/><span v-if="sidebarOpen" class="whitespace-nowrap">设置</span></router-link>
        </div>
      </aside>
      <div class="flex-1 flex flex-col min-w-0">
        <header class="border-b border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-2"><StoryNavigator/></header>
        <div class="border-b border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-1.5">
          <div class="flex items-center gap-2 flex-wrap text-sm">
            <button @click="handleOpen" class="px-2.5 py-1 rounded text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors">打开</button>
            <button @click="handleSave" class="px-2.5 py-1 rounded text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors">保存</button>
            <button @click="handleClear" class="px-2.5 py-1 rounded text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors">清空</button>
            <div class="w-px h-4 bg-[var(--color-border)]" />
            <label class="flex items-center gap-1 cursor-pointer text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><input v-model="app.showFlashback" type="checkbox" class="accent-[var(--color-primary)] w-3 h-3" />闪回</label>
            <label class="flex items-center gap-1 cursor-pointer text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><input v-model="app.syncScroll" type="checkbox" class="accent-[var(--color-primary)] w-3 h-3" />同步</label>
            <button @click="app.searchOpen = !app.searchOpen" :class="['px-2.5 py-1 rounded transition-colors', app.searchOpen ? 'text-[var(--color-primary)]' : 'text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]']">搜索</button>
            <div class="w-px h-4 bg-[var(--color-border)]" />
            <button @click="showSpeakerCheck = true" class="px-2.5 py-1 rounded text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors">说话人</button>
            <button @click="handleFullCheck" class="px-2.5 py-1 rounded text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors">检查</button>
            <button @click="showSpeakerCount = true" class="px-2.5 py-1 rounded text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors">统计</button>
            <template v-if="app.editorMode >= 1">
              <div class="w-px h-4 bg-[var(--color-border)]" />
              <label class="flex items-center gap-1 cursor-pointer text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><input v-model="app.showCompare" type="checkbox" class="accent-[var(--color-primary)] w-3 h-3" />对比</label>
              <button v-if="app.editorMode === 2" @click="handleImportBaseline" class="px-2.5 py-1 rounded text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors font-medium">导入校对稿</button>
              <button v-if="app.editorMode === 2" @click="handleConfirm" class="px-2.5 py-1 rounded text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors font-medium">确认</button>
            </template>
          </div>
          <!-- Search / replace bar -->
          <div v-if="app.searchOpen" class="flex items-center gap-2 mt-1.5 text-sm">
            <input v-model="app.searchQuery" type="text" placeholder="查找(原文/译文/说话人)" class="px-2 py-1 rounded border border-[var(--color-border)] bg-[var(--color-bg)] text-xs w-56" @keydown.enter="searchNext" />
            <span class="text-xs text-[var(--color-text-secondary)] w-16">{{ searchCount }}</span>
            <button @click="searchPrev" class="px-2 py-1 rounded text-xs text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]">上一个</button>
            <button @click="searchNext" class="px-2 py-1 rounded text-xs text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]">下一个</button>
            <div class="w-px h-4 bg-[var(--color-border)]" />
            <input v-model="app.searchReplace" type="text" placeholder="替换为(仅译文)" class="px-2 py-1 rounded border border-[var(--color-border)] bg-[var(--color-bg)] text-xs w-56" />
            <button @click="handleReplaceAll" class="px-2 py-1 rounded text-xs text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]">全部替换</button>
          </div>
        </div>
        <main class="flex-1 min-h-0"><EditorWorkspace/></main>
      </div>
    </div>
    <SpeakerCountDialog v-if="showSpeakerCount" @close="showSpeakerCount = false"/>
    <SpeakerCheckDialog v-if="showSpeakerCheck" @close="showSpeakerCheck = false" @save="handleSpeakerBatchSave" />
    <div v-if="showCloseConfirm" class="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-xl shadow-xl w-96 max-w-[90vw] p-6">
        <h3 class="font-semibold text-sm text-[var(--color-text)] mb-2">有未保存的更改</h3>
        <p class="text-xs text-[var(--color-text-secondary)] mb-5">关闭前是否保存当前的工作内容？如果不保存，更改将丢失。</p>
        <div class="flex justify-end gap-2">
          <button @click="handleCloseCancel" class="px-4 py-2 text-sm rounded-lg border border-[var(--color-border)] text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors">取消</button>
          <button @click="handleCloseDiscard" class="px-4 py-2 text-sm rounded-lg border border-red-400 text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20 transition-colors">不保存</button>
          <button @click="handleCloseSave" class="px-4 py-2 text-sm rounded-lg bg-[var(--color-primary)] text-white hover:opacity-90 transition-opacity">保存并退出</button>
        </div>
      </div>
    </div>
  </div>
</template>