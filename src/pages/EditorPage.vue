<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, onActivated, onDeactivated, watch, nextTick } from 'vue'
import { useAppStore } from '../stores/app'
import { useEditorStore } from '../stores/editor'
import { useStoryStore } from '../stores/story'
import { EditorModeLabel } from '../types/translation'
import type { SaveMetadata } from '../types/api'
import { useSettingsStore } from '../stores/settings'
import { useToast } from '../composables/useToast'
import { useConfirm } from '../composables/useConfirm'
import { useFileDialog } from '../composables/useFileDialog'
import { useAutoSave } from '../composables/useAutoSave'
import { useUndo } from '../composables/useUndo'
import { matchEvent, resolveCombo, formatCombo } from '../constants/shortcuts'
import { api } from '../api/client'
import * as LucideIcons from 'lucide-vue-next'
import { Pencil, Check, CircleDot, ChevronLeft, ChevronRight, Cog, Download, Bug, Library, BookOpen, Store, Users, AlertTriangle, Info,
  FolderOpen, Save, Eraser, Eye, Languages, Link2, Search, Columns2, ListChecks, BarChart3, FileInput, CheckCircle2, Undo2, Redo2 } from 'lucide-vue-next'
import { getCurrentWindow } from '@tauri-apps/api/window'
import StoryNavigator from '../components/navigation/StoryNavigator.vue'
import EditorWorkspace from '../components/editor/EditorWorkspace.vue'
import Live2DDock from '../components/live2d/Live2DDock.vue'
import { useLive2dDockStore } from '../stores/live2dDock'
import SpeakerCountDialog from '../components/dialogs/SpeakerCountDialog.vue'
import SpeakerCheckDialog from '../components/dialogs/SpeakerCheckDialog.vue'
import { usePluginRegistry } from '../plugin-host/registry'
import { useTeamStore } from '../stores/team'
import { useGlossaryNotifyStore } from '../stores/glossaryNotify'

const app = useAppStore()
const editor = useEditorStore()
const story = useStoryStore()
const settings = useSettingsStore()
const toast = useToast()
const { confirm } = useConfirm()
const fileDialog = useFileDialog()
const autoSave = useAutoSave()
const undo = useUndo()
const pluginRegistry = usePluginRegistry()
const team = useTeamStore()
const glossaryNotify = useGlossaryNotifyStore()
const live2dDock = useLive2dDockStore()

// Which edge (if any) the Live2D dock occupies around the workspace. Shown only
// when the user picked a docked placement (not 独立窗口), the panel is toggled
// visible, and the Live2D plugin is actually loaded. 'left' is never returned —
// the left edge belongs to the story navigator.
const dockSide = computed<'top' | 'right' | 'bottom' | null>(() => {
  if (!live2dDock.visible) return null
  if (!pluginRegistry.isLoaded('live2d')) return null
  const p = live2dDock.placement()
  // 独立窗口 normally has no edge dock — UNLESS opening the window failed and the
  // store forced a fallback dock (forcedDock), in which case mount that side so the
  // jump isn't silently dropped.
  if (p === 'window') return live2dDock.forcedDock ?? null
  return p
})

// Resolve a lucide icon by name for plugin-contributed sidebar items; fall back
// to a generic puzzle icon if the name is unknown.
function pluginIcon(name: string) {
  return (LucideIcons as any)[name] || (LucideIcons as any).Puzzle
}

const isTauri = typeof window !== 'undefined' && !!(window as any).__TAURI_INTERNALS__

// Template ref to EditorWorkspace so structural mutations here can cancel its
// pending debounced edit first (the timer captured a row index that these
// reorder, so letting it fire would corrupt a shifted row).
const workspace = ref<{ cancelPendingEdit: () => void; flushPendingEdit: () => Promise<void> } | null>(null)

function doUndo() {
  workspace.value?.cancelPendingEdit()
  const snap = undo.undo(editor.talks, editor.dstTalks)
  if (snap) { editor.talks = snap.talks; editor.dstTalks = snap.dstTalks; editor.markUnsaved() }
}
function doRedo() {
  workspace.value?.cancelPendingEdit()
  const snap = undo.redo(editor.talks, editor.dstTalks)
  if (snap) { editor.talks = snap.talks; editor.dstTalks = snap.dstTalks; editor.markUnsaved() }
}
// Toolbar 撤销/重做 buttons: top-level computed refs so the template auto-unwraps
// them (refs nested in the plain `undo` object would not be).
const canUndo = undo.canUndo
const canRedo = undo.canRedo

// ── 逐行落盘的明文保险（autosave.txt）─────────────────────────────────────────
// 每次内容变更（mutationSeq：行编辑/增删行/撤销重做/全部替换…）后短防抖，把当前
// 译文按正常保存的格式写到「正式输出同目录」的 autosave.txt——崩溃/断电后直接
// 打开就能用。与 30s 的恢复文件（dataDir，启动时提示恢复）互补，不替代。
let txtAutosaveTimer: ReturnType<typeof setTimeout> | null = null
let txtAutosaveEnsuredDir = ''
watch(() => editor.mutationSeq, () => {
  if (txtAutosaveTimer) clearTimeout(txtAutosaveTimer)
  txtAutosaveTimer = setTimeout(() => { txtAutosaveTimer = null; void writeTxtAutosave() }, 800)
})
// 输出目录 = 已保存/打开文件的所在目录；从未保存过则用正式保存的分层默认目录。
// 两者都不可知（如网页端全新文档）时跳过——恢复文件仍在兜底。
function txtAutosaveDir(): string | null {
  const p = editor.currentFilePath
  if (p && /[/\\]/.test(p)) return p.replace(/[/\\][^/\\]*$/, '')
  const base = settings.settings.saveBaseDir
  if (isTauri && base && story.selectedType && story.selectedIndexLabel) {
    const sep = (s: string) => s.replace(/[/\\]/g, '_')
    return `${base}/${sep(story.selectedType)}/${sep(story.selectedIndexLabel)}`
  }
  return null
}
async function writeTxtAutosave() {
  if (editor.talks.length === 0) return
  const dir = txtAutosaveDir()
  if (!dir) return
  const path = `${dir}/autosave.txt`
  const meta: SaveMetadata | undefined = story.selectedType ? {
    type: story.selectedType, sort: story.selectedSort, index: story.selectedIndex,
    chapter: story.selectedChapter, source: story.selectedSource, scenarioId: story.scenarioId,
    mode: app.editorMode,
  } : undefined
  try {
    if (txtAutosaveEnsuredDir !== dir) { await api.ensureDir(path); txtAutosaveEnsuredDir = dir }
    await api.translationSave(path, editor.dstTalks, app.saveN, meta)
  } catch (e) {
    console.warn('[Autosave] autosave.txt write failed', e) // 保险动作静默失败，不打扰编辑
  }
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
  // In INPUT/TEXTAREA (译文标题/搜索框) let the browser do native undo/redo
  // instead of hijacking the shortcut to roll back the whole document.
  if (hit('undo') && !inEditable) { e.preventDefault(); doUndo(); return }
  if (hit('redo') && !inEditable) { e.preventDefault(); doRedo(); return }
}

// The global keydown/resize listeners and the 30s autosave interval are bound
// per-activation, not per-mount: App.vue keeps every page alive, so onUnmounted
// never fires on navigation. Tying these to mount/unmount left the editor's
// keydown handler bound on every other page (firing open/save dialogs, undo/redo
// off-screen) and the autosave interval running forever after leaving the editor.
function activate() {
  autoSave.start()
  window.addEventListener('keydown', onKeyDown)
  window.addEventListener('resize', measureSearchAlign)
  nextTick(measureSearchAlign)
}
function deactivate() {
  window.removeEventListener('keydown', onKeyDown)
  window.removeEventListener('resize', measureSearchAlign)
  autoSave.stop()
}

onMounted(async () => {
  // One-time setup only (registering onCloseRequested on every activation would
  // stack duplicate handlers); the listeners/autosave live in activate().
  team.refreshStatus().catch(() => {})
  glossaryNotify.start() // 术语库侧栏呼吸灯：轮询待审提案/我的提案过审
  if (!isTauri) return
  try {
    const win = getCurrentWindow()
    await win.onCloseRequested(async (event) => {
      if (forceClose.value) return
      // Any-mode dirty check: edits cached in a non-current mode slot must also
      // block a silent close (hasUnsavedChanges is per-mode).
      if (editor.hasAnyUnsaved()) {
        event.preventDefault()
        await new Promise(r => setTimeout(r, 0))
        showCloseConfirm.value = true
      }
    })
  } catch (e: any) {
    tauriErr.value = `init: ${e.message || e}`
  }
})

// onActivated also runs immediately after the initial onMounted.
onActivated(activate)
onDeactivated(deactivate)

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
  const changed = key !== editor.currentMode
  // Drop any pending debounced edit BEFORE swapping mode state: its timer
  // captured a row index for the old mode's arrays, so letting it fire against
  // the new mode's talks would write the old mode's text into an unrelated row.
  // (onBlur already committed the text to both arrays, so nothing is lost.)
  if (changed) workspace.value?.cancelPendingEdit()
  editor.switchMode(key as 0 | 1 | 2)
  app.setEditorMode(key as 0 | 1 | 2)
  // The undo/redo stacks are a module-level singleton shared across all modes,
  // but switchMode swaps the live talks for a different mode's content. Replaying
  // an old mode's snapshot would overwrite the current mode's text, so clear the
  // history whenever the mode actually changes (switchMode no-ops on same mode).
  if (changed) undo.clear()
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
  // Opening a file replaces the current document and clears the undo stack, so
  // unsaved work would be gone with no way back (and the next autosave tick
  // would overwrite the recovery file with the new document too). Confirm first.
  if (editor.hasUnsavedChanges) {
    if (!(await confirm({ title: '打开文件', message: '有未保存的更改，打开新文件将丢弃它们。确定继续吗？', tone: 'danger', confirmText: '不保存并打开' }))) return
  }
  try {
    const result = await fileDialog.openTranslation()
    if (!result) return
    workspace.value?.cancelPendingEdit()
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
  // Flush (not cancel) the pending debounced edit so the file is serialized
  // from a dstTalks that carries the last blurred edit in its fully processed
  // form. Clicking 保存 within 300ms of leaving a field used to race this.
  try { await workspace.value?.flushPendingEdit() } catch { /* raw commit already in arrays */ }
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
    // Awaited: 保存并退出 destroys the window right after handleSave returns, so a
    // fire-and-forget clear could be cut off — leaving a STALE autosave that the
    // next launch offers as "recovery" over the newer just-saved file.
    await api.recoveryClear().catch(() => {})
    console.log('[Save] saved successfully', { path })
    toast.show('已保存', 'success')
  } catch (e: any) {
    const detail = e.status ? `${e.status}: ${e.message}` : (e.message || String(e))
    console.error('[Save] failed', { error: detail, defaultName, talkCount: editor.talks.length, isTauri })
    toast.show('Save failed: ' + detail, 'error')
  }
}

async function handleClear() {
  // Always confirm — 清空 wipes the document AND the undo stack, so a stray
  // click is unrecoverable even with no unsaved changes.
  const detail = editor.hasUnsavedChanges ? '有未保存的更改，清空后无法找回。' : '清空后无法撤销。'
  if (!(await confirm({ title: '清空内容', message: '确定清空当前全部内容吗？', detail, tone: 'danger', confirmText: '清空' }))) return
  editor.clearAll()
  undo.clear()
  toast.show('已清空', 'info')
}

async function handleConfirm() {
  if (editor.talks.length === 0) return
  if (!(await confirm({ title: '确认合意完成', message: '确认合意完成？所有差异将以当前译文为准。', tone: 'primary', confirmText: '确认' }))) return
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
  // Drop any pending debounced edit before re-routing rows through changeText:
  // its captured row index could land on a row this loop has already rewritten.
  workspace.value?.cancelPendingEdit()
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
onUnmounted(deactivate) // safety net; under keep-alive onDeactivated does the real work
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
        <div class="flex flex-col gap-0.5 p-1.5" data-tour="modes">
          <button v-for="m in modes" :key="m.key" @click="setMode(m.key)" class="flex items-center gap-2.5 h-9 px-2 rounded-lg transition-colors text-sm flex-shrink-0" :class="app.editorMode === m.key ? 'bg-[var(--color-primary)]/10 text-[var(--color-primary)] font-medium' : 'text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]'">
            <component :is="modeIcons[m.key]" :size="18" /><span v-if="sidebarOpen" class="whitespace-nowrap">{{ m.label }}</span>
          </button>
        </div>
        <div class="flex-1" />
        <div class="border-t border-[var(--color-border)] p-1.5 space-y-0.5">
          <router-link to="/download" class="flex items-center gap-2.5 h-9 w-full px-2 rounded-lg transition-colors text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><Download :size="18"/><span v-if="sidebarOpen" class="whitespace-nowrap">下载</span></router-link>
          <router-link to="/glossary" data-tour="nav-glossary" :class="{ 'notify-breathe': glossaryNotify.active }" :title="glossaryNotify.tooltip || undefined" class="flex items-center gap-2.5 h-9 w-full px-2 rounded-lg transition-colors text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><Library :size="18"/><span v-if="sidebarOpen" class="whitespace-nowrap">术语库</span></router-link>
          <router-link to="/grammar" class="flex items-center gap-2.5 h-9 w-full px-2 rounded-lg transition-colors text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><BookOpen :size="18"/><span v-if="sidebarOpen" class="whitespace-nowrap">语法用例</span></router-link>
          <!-- Plugin-contributed sidebar items (Live2D, etc.) -->
          <router-link v-for="item in pluginRegistry.sidebarItems" :key="`${item.pluginId}:${item.id}`" :to="item.to" class="flex items-center gap-2.5 h-9 w-full px-2 rounded-lg transition-colors text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><component :is="pluginIcon(item.icon)" :size="18"/><span v-if="sidebarOpen" class="whitespace-nowrap">{{ item.label }}</span></router-link>
          <router-link to="/market" data-tour="nav-market" class="flex items-center gap-2.5 h-9 w-full px-2 rounded-lg transition-colors text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><Store :size="18"/><span v-if="sidebarOpen" class="whitespace-nowrap">插件市场</span></router-link>
          <router-link v-if="settings.settings.debugEnabled" to="/debug" class="flex items-center gap-2.5 h-9 w-full px-2 rounded-lg transition-colors text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><Bug :size="18"/><span v-if="sidebarOpen" class="whitespace-nowrap">调试</span></router-link>
          <router-link to="/account" class="flex items-center gap-2.5 h-9 w-full px-2 rounded-lg transition-colors text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><Users :size="18"/><span v-if="sidebarOpen" class="whitespace-nowrap">账号中心</span></router-link>
          <router-link to="/settings" data-tour="nav-settings" class="flex items-center gap-2.5 h-9 w-full px-2 rounded-lg transition-colors text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"><Cog :size="18"/><span v-if="sidebarOpen" class="whitespace-nowrap">设置</span></router-link>
        </div>
      </aside>
      <div class="flex-1 flex flex-col min-w-0">
        <header class="border-b border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-2" data-tour="story-nav"><StoryNavigator :auto-pull="true"/></header>
        <div class="border-b border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-1.5">
          <div class="flex items-center gap-1 flex-wrap" data-tour="toolbar">
            <button @click="handleOpen" class="btn btn-sm btn-ghost gap-1.5"><FolderOpen :size="15" />{{ app.editorMode === 2 ? '导入翻译稿' : '打开' }}</button>
            <button @click="handleSave" class="btn btn-sm btn-ghost gap-1.5"><Save :size="15" />保存</button>
            <button @click="handleClear" class="btn btn-sm btn-ghost gap-1.5"><Eraser :size="15" />清空</button>
            <button @click="doUndo" :disabled="!canUndo" :title="'撤销最近一次修改 (' + formatCombo(resolveCombo(settings.settings.shortcuts, 'undo')) + ')'" class="btn btn-sm btn-ghost gap-1.5"><Undo2 :size="15" />撤销</button>
            <button @click="doRedo" :disabled="!canRedo" :title="'重做 (' + formatCombo(resolveCombo(settings.settings.shortcuts, 'redo')) + ')'" class="btn btn-sm btn-ghost gap-1.5"><Redo2 :size="15" />重做</button>
            <div class="w-px h-5 bg-[var(--color-border)] mx-1" />
            <button class="tbar-toggle" :aria-pressed="app.showFlashback" @click="app.showFlashback = !app.showFlashback"><Eye :size="15" />闪回</button>
            <button class="tbar-toggle" :aria-pressed="app.showGlossary" @click="app.showGlossary = !app.showGlossary"><Languages :size="15" />术语</button>
            <button class="tbar-toggle" :aria-pressed="app.syncScroll" @click="app.syncScroll = !app.syncScroll"><Link2 :size="15" />同步</button>
            <button class="tbar-toggle" :aria-pressed="app.searchOpen" @click="app.searchOpen = !app.searchOpen"><Search :size="15" />搜索</button>
            <div ref="toolbarSearchSep" class="w-px h-5 bg-[var(--color-border)] mx-1" />
            <button @click="showSpeakerCheck = true" class="btn btn-sm btn-ghost gap-1.5"><Users :size="15" />说话人</button>
            <button @click="handleFullCheck" class="btn btn-sm btn-ghost gap-1.5"><ListChecks :size="15" />检查</button>
            <button @click="showSpeakerCount = true" class="btn btn-sm btn-ghost gap-1.5"><BarChart3 :size="15" />统计</button>
            <template v-if="app.editorMode >= 1">
              <div class="w-px h-5 bg-[var(--color-border)] mx-1" />
              <button class="tbar-toggle" :aria-pressed="app.showCompare" @click="app.showCompare = !app.showCompare"><Columns2 :size="15" />对比</button>
              <button v-if="app.editorMode === 2" @click="handleImportBaseline" :title="'导入校对稿 (' + formatCombo(resolveCombo(settings.settings.shortcuts, 'importBaseline')) + ')'" class="btn btn-sm btn-ghost border border-[var(--color-border)] gap-1.5"><FileInput :size="15" />导入校对稿</button>
              <button v-if="app.editorMode === 2" @click="handleConfirm" class="btn btn-sm btn-brand gap-1.5"><CheckCircle2 :size="15" />确认</button>
            </template>
          </div>
          <!-- Search / replace bar. The left group is width-matched to the
               toolbar so the divider sits directly under the toolbar's
               "搜索"-right divider. -->
          <div v-if="app.searchOpen" ref="searchBarRow" class="flex items-center gap-2 mt-2">
            <div class="flex items-center gap-2" :style="{ width: searchLeftWidth + 'px' }">
              <input v-model="app.searchQuery" type="text" placeholder="查找(原文/译文/说话人)" class="app-input flex-1 min-w-0" @keydown.enter="searchNext" />
              <span class="text-xs text-[var(--color-text-secondary)] tabular-nums flex-shrink-0">{{ searchCount }}</span>
              <button @click="searchPrev" class="btn btn-xs btn-ghost">上一个</button>
              <button @click="searchNext" class="btn btn-xs btn-ghost">下一个</button>
            </div>
            <div class="w-px h-5 bg-[var(--color-border)]" />
            <input v-model="app.searchReplace" type="text" placeholder="替换为(仅译文)" class="app-input w-56" />
            <button @click="handleReplaceAll" class="btn btn-sm btn-ghost border border-[var(--color-border)]">全部替换</button>
          </div>
        </div>
        <!-- With a wallpaper on, let the editor card float on a thin gutter of
             wallpaper instead of butting a bordered rounded card straight into
             the square toolbar/sidebar (which left a hard full-width seam + cut
             corner notches). Off → unchanged full-bleed layout. -->
        <main
          class="flex-1 min-h-0 flex"
          :class="[{ 'p-2.5': app.bgEnabled }, dockSide === 'top' || dockSide === 'bottom' ? 'flex-col' : 'flex-row']"
        >
          <Live2DDock v-if="dockSide === 'top'" placement="top" />
          <div class="flex-1 min-w-0 min-h-0" data-tour="workspace"><EditorWorkspace ref="workspace"/></div>
          <Live2DDock v-if="dockSide === 'right'" placement="right" />
          <Live2DDock v-if="dockSide === 'bottom'" placement="bottom" />
        </main>
      </div>
    </div>
    <SpeakerCountDialog v-if="showSpeakerCount" @close="showSpeakerCount = false"/>
    <SpeakerCheckDialog v-if="showSpeakerCheck" @close="showSpeakerCheck = false" @save="handleSpeakerBatchSave" />
    <Transition name="confirm-fade">
      <div v-if="showCloseConfirm" class="fixed inset-0 flex items-center justify-center p-4 z-[var(--z-modal)]">
        <div class="absolute inset-0 bg-black/45 backdrop-blur-[2px]" @click="handleCloseCancel" />
        <div class="app-card app-glass relative w-full max-w-sm p-5" style="box-shadow: var(--shadow-lg)">
          <div class="flex items-start gap-3">
            <div class="grid place-items-center w-9 h-9 rounded-full shrink-0 bg-warning/15 text-warning"><AlertTriangle :size="18" /></div>
            <div class="min-w-0 flex-1">
              <h3 class="section-title mb-1">有未保存的更改</h3>
              <p class="text-sm text-[var(--color-text-secondary)] leading-relaxed">关闭前是否保存当前的工作内容？如果不保存，更改将丢失。</p>
            </div>
          </div>
          <div class="flex justify-end gap-2 mt-5">
            <button @click="handleCloseCancel" class="btn btn-sm btn-ghost border border-[var(--color-border)]">取消</button>
            <button @click="handleCloseDiscard" class="btn btn-sm btn-ghost text-error hover:bg-error/10">不保存</button>
            <button @click="handleCloseSave" class="btn btn-sm btn-brand">保存并退出</button>
          </div>
        </div>
      </div>
    </Transition>

    <Transition name="confirm-fade">
      <div v-if="showAgreementHint" class="fixed inset-0 flex items-center justify-center p-4 z-[var(--z-modal)]">
        <div class="absolute inset-0 bg-black/45 backdrop-blur-[2px]" />
        <div class="app-card app-glass relative w-full max-w-sm p-5" style="box-shadow: var(--shadow-lg)">
          <div class="flex items-start gap-3">
            <div class="grid place-items-center w-9 h-9 rounded-full shrink-0 bg-info/15 text-info"><Info :size="18" /></div>
            <div class="min-w-0 flex-1">
              <h3 class="section-title mb-1">注意</h3>
              <p class="text-sm text-[var(--color-text)] leading-relaxed">请先导入翻译稿再导入校对稿</p>
            </div>
          </div>
          <div class="flex items-center justify-between gap-3 mt-5">
            <label class="flex items-center gap-2 cursor-pointer select-none text-xs text-[var(--color-text-secondary)]">
              <input v-model="agreementHintDontShow" type="checkbox" class="accent-[var(--color-primary)] w-3.5 h-3.5 cursor-pointer" />
              不再弹出此窗口（可随时在设置里调整）
            </label>
            <button @click="confirmAgreementHint" class="btn btn-sm btn-brand flex-shrink-0">确认</button>
          </div>
        </div>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
/* 术语库呼吸灯：有提案待审核 / 自己的提案新通过时，侧栏「术语库」按主题色
   呼吸发光（动画期间 animation 的 color 优先级高于普通声明，会盖过 hover）。 */
@keyframes notify-breathe {
  0%, 100% { color: var(--color-text-secondary); filter: none; }
  50% {
    color: var(--color-primary);
    filter: drop-shadow(0 0 6px color-mix(in oklch, var(--color-primary) 55%, transparent));
  }
}
.notify-breathe { animation: notify-breathe 2.2s ease-in-out infinite; }
@media (prefers-reduced-motion: reduce) {
  .notify-breathe { animation: none; color: var(--color-primary); }
}

/* Toolbar toggle chip — on/off view options (闪回/术语/同步/搜索/对比) */
.tbar-toggle {
  display: inline-flex;
  align-items: center;
  gap: 0.375rem;
  height: 2rem;
  padding: 0 0.625rem;
  border-radius: var(--radius-control);
  font-size: 0.8125rem;
  font-weight: 500;
  white-space: nowrap;
  color: var(--color-text-secondary);
  transition: background-color var(--dur-fast) var(--ease-out), color var(--dur-fast) var(--ease-out);
}
.tbar-toggle:hover {
  background: color-mix(in oklch, var(--color-base-content) 8%, transparent);
  color: var(--color-text);
}
.tbar-toggle[aria-pressed="true"] {
  background: color-mix(in oklch, var(--accent, var(--color-primary)) 14%, transparent);
  color: var(--accent, var(--color-primary));
}

.confirm-fade-enter-active,
.confirm-fade-leave-active {
  transition: opacity var(--dur) var(--ease-out);
}
.confirm-fade-enter-from,
.confirm-fade-leave-to {
  opacity: 0;
}
.confirm-fade-enter-active .app-card,
.confirm-fade-leave-active .app-card {
  transition: transform var(--dur) var(--ease-out);
}
.confirm-fade-enter-from .app-card {
  transform: translateY(8px) scale(0.97);
}
</style>