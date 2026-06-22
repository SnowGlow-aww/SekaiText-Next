<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
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
import { matchEvent, resolveCombo, formatCombo } from '../constants/shortcuts'
import { api } from '../api/client'
import * as LucideIcons from 'lucide-vue-next'
import { Pencil, Check, CircleDot, ChevronLeft, ChevronRight, Cog, Download, Bug, Library, BookOpen, Store, Users } from 'lucide-vue-next'
import { getCurrentWindow } from '@tauri-apps/api/window'
import StoryNavigator from '../components/navigation/StoryNavigator.vue'
import EditorWorkspace from '../components/editor/EditorWorkspace.vue'
import SpeakerCountDialog from '../components/dialogs/SpeakerCountDialog.vue'
import SpeakerCheckDialog from '../components/dialogs/SpeakerCheckDialog.vue'
import { usePluginRegistry } from '../plugin-host/registry'
import { useTeamStore } from '../stores/team'

const app = useAppStore()
const editor = useEditorStore()
const story = useStoryStore()
const settings = useSettingsStore()
const toast = useToast()
const fileDialog = useFileDialog()
const autoSave = useAutoSave()
const undo = useUndo()
const pluginRegistry = usePluginRegistry()
const team = useTeamStore()

// Resolve a lucide icon by name for plugin-contributed sidebar items; fall back
// to a generic puzzle icon if the name is unknown.
function pluginIcon(name: string) {
  return (LucideIcons as any)[name] || (LucideIcons as any).Puzzle
}

const isTauri = typeof window !== 'undefined' && !!(window as any).__TAURI_INTERNALS__

function doUndo() {
  const snap = undo.undo(editor.talks, editor.dstTalks)
  if (snap) { editor.talks = snap.talks; editor.dstTalks = snap.dstTalks; editor.markUnsaved() }
}
function doRedo() {
  const snap = undo.redo(editor.talks, editor.dstTalks)
  if (snap) { editor.talks = snap.talks; editor.dstTalks = snap.dstTalks; editor.markUnsaved() }
}

function onKeyDown(e: KeyboardEvent) {
  const el = document.activeElement
  const inEditable = el instanceof HTMLElement &&
    (el.isContentEditable || el.tagName === 'INPUT' || el.tagName === 'TEXTAREA')
  // Inside contenteditable, let the browser handle native undo/redo.
  if (el instanceof HTMLElement && el.isContentEditable) return

  const sc = settings.settings.shortcuts
  const hit = (id: string) => matchEvent(e, resolveCombo(sc, id))

  if (hit('open')) { e.preventDefault(); handleOpen(); return }
  if (hit('save')) { e.preventDefault(); handleSave(); return }
  if (hit('search')) { e.preventDefault(); app.searchOpen = true; return }
  if (hit('importBaseline')) {
    if (app.editorMode === 2) { e.preventDefault(); handleImportBaseline() }
    return
  }
  if (hit('replaceAll')) {
    if (app.searchOpen) { e.preventDefault(); handleReplaceAll() }
    return
  }
  if (hit('prevMatch')) {
    if (app.searchOpen && !inEditable) { e.preventDefault(); searchPrev() }
    return
  }
  if (hit('nextMatch')) {
    if (app.searchOpen && !inEditable) { e.preventDefault(); searchNext() }
    return
  }
  if (hit('undo')) { e.preventDefault(); doUndo(); return }
  if (hit('redo')) { e.preventDefault(); doRedo(); return }
}

onMounted(async () => {
  autoSave.start()
  window.addEventListener('keydown', onKeyDown)
  window.addEventListener('resize', measureSearchAlign)
  nextTick(measureSearchAlign)
  team.refreshStatus().catch(() => {})
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

// Align the search bar's divider directly under the toolbar's "搜索"-right
// divider. Measured at runtime (not a hardcoded px) so it stays correct across
// fonts/themes/locales. searchLeftWidth = toolbar divider's left offset minus
// the shared container's left edge.
const toolbarSearchSep = ref<HTMLElement | null>(null)
const searchBarRow = ref<HTMLElement | null>(null)
const searchLeftWidth = ref(360)
function measureSearchAlign() {
  const sep = toolbarSearchSep.value
  const row = searchBarRow.value
  if (!sep || !row) return
  // The divider sits AFTER the left group, separated by the row's flex gap
  // (gap-2 = 8px). So the left group width must be the toolbar divider's offset
  // minus that gap for the search divider to land exactly under it.
  const gap = 8
  const w = sep.getBoundingClientRect().left - row.getBoundingClientRect().left - gap
  if (w > 80) searchLeftWidth.value = Math.round(w)
}
watch(() => app.searchOpen, (open) => {
  if (open) nextTick(measureSearchAlign)
})

function setMode(key: number) {
  editor.switchMode(key as 0 | 1 | 2)
  app.setEditorMode(key as 0 | 1 | 2)
  // 校对/合意 default to compare-on (baseline rows visible); 翻译 has no compare.
  app.showCompare = key >= 1
  // Entering 合意: remind the workflow (translation first, then proofread draft).
  if (key === 2 && !settings.settings.hideAgreementImportHint) {
    agreementHintDontShow.value = false
    showAgreementHint.value = true
  }
}

const showAgreementHint = ref(false)
const agreementHintDontShow = ref(false)
function confirmAgreementHint() {
  if (agreementHintDontShow.value) {
    settings.settings.hideAgreementImportHint = true
    settings.saveSettings().catch(() => {})
  }
  showAgreementHint.value = false
}

const modes = [ { key: 0, label: '翻译' }, { key: 1, label: '校对' }, { key: 2, label: '合意' } ]
const modeIcons: Record<number, typeof Pencil> = { 0: Pencil, 1: Check, 2: CircleDot }

async function handleOpen() {
  try {
    const result = await fileDialog.openTranslation()
    if (!result) return
    console.log('[Open] loaded file', { path: result.filePath || result.fileName, talkCount: result.talks.length, hasMeta: !!result.meta, mode: app.editorMode, fileMode: result.meta?.mode })
    // Baseline fallback: in 校对/合意 modes, seed every row's baseline to its
    // current text up front. The .txt format does not persist baseline, and the
    // story-load block below only sets it when the source story resolves — which
    // silently fails for files with an empty index (caught + skipped). Without a
    // baseline, editing a line computes no diff, so the compare view shows
    // nothing ("我改了没反应"). Seeding here guarantees edits always diff against
    // the text as it was on open, independent of whether the source story loads.
    if (app.editorMode >= 1) {
      for (const t of result.talks) if (t.baseline === undefined || t.baseline === '') t.baseline = t.text
    }
    editor.setTalks(result.talks, result.talks, [])
    // Pre-fill the 译文 header title input from the filename. The name looks like
    // "【翻译】3rd-group3-01 思いがけない出会い.txt": strip the 【…】 prefix and
    // .txt, the first token is the label (story id), the rest is the (already
    // translated) chapter title — show it as the actual input value, not just a
    // placeholder, so the translator sees/keeps their title.
    const rawName = (result.filePath || result.fileName || '').split(/[/\\]/).pop() || ''
    const baseName = rawName.replace(/\.txt$/i, '').replace(/^【[^】]*】/, '').trim()
    const label = baseName.split(/\s+/)[0] || ''
    const titlePart = baseName.slice(label.length).trim()
    editor.titleOverride = titlePart

    // Mode isolation: a file whose saved mode differs from the current editor
    // mode is treated as a *baseline to derive from*, not a file to edit in
    // place. Clearing currentFilePath forces "save as" with the current mode's
    // 【…】 name, so the original (e.g. 翻译) file is never overwritten.
    const fileMode = (result.meta?.mode ?? 0) as 0 | 1 | 2
    const deriving = fileMode !== app.editorMode
    editor.currentFilePath = deriving ? '' : (result.filePath || result.fileName || '')
    editor.markSaved()
    undo.clear()

    // Auto-load the source scenario from the filename label (see above). Resolve
    // it to story coordinates and load + align the source (fixed Haruki Neo
    // source). On any failure we silently keep manual selection.
    if (label) {
      try {
        const r = await api.resolveLabel(label)
        if (r.ok) {
          story.selectedSource = 'haruki'
          // Populate the navigator dropdowns so they SHOW the resolved story,
          // not just load it. Setting selectedType triggers a watcher that
          // resets the child selections and refetches lists, so we sequence:
          // set type -> let its cascade settle -> fetch the index/chapter lists
          // -> set index/chapter LAST so they stick and the <select>s display.
          story.selectedType = r.storyType
          story.selectedSort = ''
          await story.fetchSorts(r.storyType)
          await story.fetchIndex(r.storyType, '')
          await nextTick()
          story.selectedIndex = r.index
          story.selectedIndexLabel = story.indices.find(i => i.value === r.index)?.label || r.index
          await story.fetchChapters(r.storyType, '', r.index)
          await nextTick()
          story.selectedChapter = r.chapter
          await story.loadStory()
          if (story.sourceTalks.length > 0) {
            const aligned = await api.checkLines({ sourceTalks: story.sourceTalks, loadedTalks: result.talks })
            if (app.editorMode >= 1) {
              // Derive baseline rows for compare (校对/合意).
              const compared = await api.compareText({ referTalks: aligned, checkTalks: aligned, editorMode: app.editorMode })
              editor.setTalks(compared.talks, compared.dstTalks, aligned)
            } else {
              editor.setTalks(aligned, aligned, [])
            }
          }
        }
      } catch { /* keep manual selection */ }
    }
    toast.show('已打开: ' + (label || rawName), 'success')
  } catch (e: any) { toast.show('Open failed: ' + (e.message || String(e)), 'error') }
}

async function handleSave() {
  if (editor.talks.length === 0) return
  const modeLabel = EditorModeLabel[app.editorMode as 0 | 1 | 2]
  // The 译文 header input (editor.titleOverride) owns the title segment. The
  // filename is always rebuilt as 【模式】<saveTitle> <title>.txt — the prefix
  // and .txt suffix are fixed, only the title part is user-editable. Empty
  // override falls back to the story's chapterTitle.
  const title = (editor.titleOverride || story.chapterTitle || '').trim()
  let fileName = '【' + modeLabel + '】' + (story.saveTitle || 'untitled')
  if (title) fileName += ' ' + title
  fileName += '.txt'
  // Layered output: <saveBaseDir>/<故事类型>/<索引名>/<【模式】标题.txt>
  let defaultName: string
  const base = settings.settings.saveBaseDir
  if (isTauri && base && story.selectedType && story.selectedIndexLabel) {
    const sep = (s: string) => s.replace(/[/\\]/g, '_')
    defaultName = `${base}/${sep(story.selectedType)}/${sep(story.selectedIndexLabel)}/${fileName}`
  } else {
    defaultName = fileName
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

// 合意: import a 校对稿 as the editable text, comparing it against the already
// loaded 翻译稿 (baseline). compareText pairs them by idx + sub-line position;
// the baseline row (yellow) shows the 翻译稿, the editable row (green) shows the
// 校对稿 — so the agreed edits are made on top of the proofread draft.
async function handleImportBaseline() {
  if (editor.talks.length === 0) { toast.show('请先载入翻译稿', 'warn'); return }
  try {
    const result = await fileDialog.openTranslation()
    if (!result) return
    undo.pushSnapshot(editor.talks, editor.dstTalks)
    // Align the imported 校对稿 to the source story BEFORE comparing. The current
    // 翻译稿 rows were aligned to the source on open (idx = source line). A freshly
    // parsed .txt instead carries positional idx, so when the two files differ in
    // line count (e.g. the 校对稿 has an extra intro line) every row pairs against
    // the wrong counterpart — the whole compare view shifts by one. Re-aligning the
    // imported file to the same source restores a stable idx. Fall back to raw
    // talks only if no source is loaded.
    let checkTalks = result.talks
    if (story.sourceTalks.length > 0) {
      checkTalks = await api.checkLines({ sourceTalks: story.sourceTalks, loadedTalks: result.talks })
    } else {
      toast.show('未加载原文，对比可能错位，请先选择剧情', 'warn')
    }
    // Baseline (yellow) = 翻译稿 (current editor.talks); editable (green) = 校对稿.
    const compared = await api.compareText({
      referTalks: editor.talks,
      checkTalks,
      editorMode: 2,
    })
    editor.setTalks(compared.talks, compared.dstTalks, editor.referTalks)
    app.showCompare = true
    // Adopt the imported 校对稿's title as the displayed 译文 title: in 合意 the
    // agreed text derives from the proofread draft, so its title is the relevant
    // version. Parse it from the filename (strip 【…】 prefix + .txt + label).
    const impName = (result.filePath || result.fileName || '').split(/[/\\]/).pop() || ''
    const impBase = impName.replace(/\.txt$/i, '').replace(/^【[^】]*】/, '').trim()
    const impTitle = impBase.slice((impBase.split(/\s+/)[0] || '').length).trim()
    if (impTitle) editor.titleOverride = impTitle
    editor.markUnsaved()
    toast.show('已导入校对稿', 'success')
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
  window.removeEventListener('resize', measureSearchAlign)
  autoSave.stop() // stop the recovery interval when leaving the editor
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
          <router-link to="/glossary" class="flex items-center gap-2.5 h-9 w-full px-2 rounded-lg transition-colors text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><Library :size="18"/><span v-if="sidebarOpen" class="whitespace-nowrap">术语库</span></router-link>
          <router-link to="/grammar" class="flex items-center gap-2.5 h-9 w-full px-2 rounded-lg transition-colors text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><BookOpen :size="18"/><span v-if="sidebarOpen" class="whitespace-nowrap">语法用例</span></router-link>
          <!-- Plugin-contributed sidebar items (Live2D, etc.) -->
          <router-link v-for="item in pluginRegistry.sidebarItems" :key="item.id" :to="item.to" class="flex items-center gap-2.5 h-9 w-full px-2 rounded-lg transition-colors text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><component :is="pluginIcon(item.icon)" :size="18"/><span v-if="sidebarOpen" class="whitespace-nowrap">{{ item.label }}</span></router-link>
          <router-link to="/market" class="flex items-center gap-2.5 h-9 w-full px-2 rounded-lg transition-colors text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><Store :size="18"/><span v-if="sidebarOpen" class="whitespace-nowrap">插件市场</span></router-link>
          <router-link v-if="settings.settings.debugEnabled" to="/debug" class="flex items-center gap-2.5 h-9 w-full px-2 rounded-lg transition-colors text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><Bug :size="18"/><span v-if="sidebarOpen" class="whitespace-nowrap">调试</span></router-link>
          <router-link to="/account" class="flex items-center gap-2.5 h-9 w-full px-2 rounded-lg transition-colors text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><Users :size="18"/><span v-if="sidebarOpen" class="whitespace-nowrap">账号中心</span></router-link>
          <router-link to="/settings" class="flex items-center gap-2.5 h-9 w-full px-2 rounded-lg transition-colors text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><Cog :size="18"/><span v-if="sidebarOpen" class="whitespace-nowrap">设置</span></router-link>
        </div>
      </aside>
      <div class="flex-1 flex flex-col min-w-0">
        <header class="border-b border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-2"><StoryNavigator :auto-pull="true"/></header>
        <div class="border-b border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-1.5">
          <div class="flex items-center gap-2 flex-wrap text-sm">
            <button @click="handleOpen" class="px-2.5 py-1 rounded text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors">{{ app.editorMode === 2 ? '导入翻译稿' : '打开' }}</button>
            <button @click="handleSave" class="px-2.5 py-1 rounded text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors">保存</button>
            <button @click="handleClear" class="px-2.5 py-1 rounded text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors">清空</button>
            <div class="w-px h-4 bg-[var(--color-border)]" />
            <label class="flex items-center gap-1 cursor-pointer text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><input v-model="app.showFlashback" type="checkbox" class="accent-[var(--color-primary)] w-3 h-3" />闪回</label>
            <label class="flex items-center gap-1 cursor-pointer text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><input v-model="app.showGlossary" type="checkbox" class="accent-[var(--color-primary)] w-3 h-3" />术语</label>
            <label class="flex items-center gap-1 cursor-pointer text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><input v-model="app.syncScroll" type="checkbox" class="accent-[var(--color-primary)] w-3 h-3" />同步</label>
            <button @click="app.searchOpen = !app.searchOpen" :class="['px-2.5 py-1 rounded transition-colors', app.searchOpen ? 'text-[var(--color-primary)]' : 'text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]']">搜索</button>
            <div ref="toolbarSearchSep" class="w-px h-4 bg-[var(--color-border)]" />
            <button @click="showSpeakerCheck = true" class="px-2.5 py-1 rounded text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors">说话人</button>
            <button @click="handleFullCheck" class="px-2.5 py-1 rounded text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors">检查</button>
            <button @click="showSpeakerCount = true" class="px-2.5 py-1 rounded text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors">统计</button>
            <template v-if="app.editorMode >= 1">
              <div class="w-px h-4 bg-[var(--color-border)]" />
              <label class="flex items-center gap-1 cursor-pointer text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><input v-model="app.showCompare" type="checkbox" class="accent-[var(--color-primary)] w-3 h-3" />对比</label>
              <button v-if="app.editorMode === 2" @click="handleImportBaseline" :title="'导入校对稿 (' + formatCombo(resolveCombo(settings.settings.shortcuts, 'importBaseline')) + ')'" class="px-2.5 py-1 rounded text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors font-medium">导入校对稿</button>
              <button v-if="app.editorMode === 2" @click="handleConfirm" class="px-2.5 py-1 rounded text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors font-medium">确认</button>
            </template>
          </div>
          <!-- Search / replace bar. The left group is width-matched to the
               toolbar so the divider sits directly under the toolbar's
               "搜索"-right divider. -->
          <div v-if="app.searchOpen" ref="searchBarRow" class="flex items-center gap-2 mt-1.5 text-sm">
            <div class="flex items-center gap-2" :style="{ width: searchLeftWidth + 'px' }">
              <input v-model="app.searchQuery" type="text" placeholder="查找(原文/译文/说话人)" class="flex-1 min-w-0 px-2 py-1 rounded border border-[var(--color-border)] bg-[var(--color-bg)] text-xs" @keydown.enter="searchNext" />
              <span class="text-xs text-[var(--color-text-secondary)]">{{ searchCount }}</span>
              <button @click="searchPrev" class="px-2 py-1 rounded text-xs text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]">上一个</button>
              <button @click="searchNext" class="px-2 py-1 rounded text-xs text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]">下一个</button>
            </div>
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

    <div v-if="showAgreementHint" class="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-xl shadow-2xl shadow-black/60 w-96 max-w-[90vw] p-6">
        <h3 class="font-semibold text-[var(--color-text)] text-center mb-3" style="font-size: 25px">注意</h3>
        <p class="text-[var(--color-text)] text-center mb-6" style="font-size: 15px">请先导入翻译稿再导入校对稿</p>
        <div class="flex items-center justify-between gap-3">
          <label class="flex items-center gap-2 cursor-pointer select-none" style="color: #FFFFFF80; font-size: 12px">
            <input v-model="agreementHintDontShow" type="checkbox" class="accent-[var(--color-primary)] w-3.5 h-3.5 cursor-pointer opacity-80" />
            不再弹出此窗口（可随时在设置里调整）
          </label>
          <button @click="confirmAgreementHint" class="px-5 py-1.5 rounded-lg text-sm bg-[var(--color-primary)] text-white hover:opacity-90 transition-opacity flex-shrink-0">确认</button>
        </div>
      </div>
    </div>
  </div>
</template>