<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, onActivated, onDeactivated, watch, nextTick } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
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
import { matchEvent, resolveCombo, formatCombo, isMac } from '../constants/shortcuts'
import { api } from '../api/client'
import { Users, AlertTriangle, Info,
  FolderOpen, Save, Eraser, Eye, Languages, Search, Columns2, ListChecks, BarChart3, FileInput, Undo2, Redo2, X } from 'lucide-vue-next'
import { getCurrentWindow } from '@tauri-apps/api/window'
import StoryNavigator from '../components/navigation/StoryNavigator.vue'
import EditorWorkspace from '../components/editor/EditorWorkspace.vue'
import Live2DDock from '../components/live2d/Live2DDock.vue'
import { useLive2dDockStore } from '../stores/live2dDock'
import SpeakerCountDialog from '../components/dialogs/SpeakerCountDialog.vue'
import SpeakerCheckDialog from '../components/dialogs/SpeakerCheckDialog.vue'
import { usePluginRegistry } from '../plugin-host/registry'
import { BaselineImportError, runBaselineImportTransaction } from '../editor/baselineImport'
import { commitDocumentMutation } from '../editor/documentMutation'
import {
  OpenDocumentError,
  cloneOpenDocument,
  runOpenDocumentTransaction,
} from '../editor/openDocument'
import { saveDirectoryCoordinator } from '../editor/saveDirectoryCoordinator'
import { clearRecovery } from '../editor/recoveryCoordinator'
import { formatManagedDocumentFileName } from '../editor/documentFileName'
import { completeManualSave, manualSaveFeedback, type ManualSaveOutcome } from '../editor/saveCoordinator'
import { capabilities } from '../platform/capabilities'

const app = useAppStore()
const editor = useEditorStore()
const story = useStoryStore()
const settings = useSettingsStore()
const toast = useToast()
const { confirm } = useConfirm()
const fileDialog = useFileDialog()
const undo = useUndo()
const pluginRegistry = usePluginRegistry()
const live2dDock = useLive2dDockStore()

// Template ref to EditorWorkspace so every file/recovery snapshot can first
// materialize text that still lives in the focused contenteditable.
const workspace = ref<{
  cancelPendingEdit: () => void
  flushPendingEdit: () => Promise<void>
  flushPendingEditForDeactivation: () => Promise<void>
} | null>(null)
const autoSave = useAutoSave(
  30000,
  () => workspace.value?.flushPendingEdit(),
  error => {
    console.error('[Recovery] background snapshot failed', error)
    toast.show('自动恢复快照写入失败；请尽快保存文档并检查可用存储空间', 'error', 8000)
  },
)

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

const isTauri = capabilities.isTauri
const isDesktop = capabilities.isDesktop

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

// Real translation files are written only after an explicit Save action. Every
// edit still schedules the crash-recovery snapshot, which is deliberately kept
// separate from the user-selected document path.
const txtSaveCoordinator = saveDirectoryCoordinator
watch(() => editor.mutationSeq, () => {
  autoSave.schedule()
})
// 保存命名/元数据一律以载入时的文档快照（editor.docMeta）为准；快照缺失时
// （网页端本地打开、标签解析失败等）才回退全局 story 状态。story 是全局单例，
// 载入后再拉别的剧情就会被改走——按保存时的全局状态命名，会把当前文档存成
// 别的剧情的文件（用户反馈：编辑前篇存成了后篇的文件名）。
function buildSaveMeta(): SaveMetadata | undefined {
  const d = editor.docMeta
  if (d?.type) {
    return {
      type: d.type, sort: d.sort, index: d.index,
      chapter: d.chapter, source: d.source, scenarioId: d.scenarioId,
      mode: app.editorMode,
    }
  }
  return story.selectedType ? {
    type: story.selectedType, sort: story.selectedSort, index: story.selectedIndex,
    chapter: story.selectedChapter, source: story.selectedSource, scenarioId: story.scenarioId,
    mode: app.editorMode,
  } : undefined
}
// 规范文件名：【模式】<SaveTitle> <ChapterTitle>【标题】<翻译标题>.txt。
// canonical 身份永远保留在文件名主体，用户可编辑标题单独放在明确后缀里，
// 这样保存时不会把翻译标题误当成剧情身份，冷启动也能通用反解。
function canonicalFileName(): string {
  const modeLabel = EditorModeLabel[app.editorMode as 0 | 1 | 2]
  const d = editor.docMeta
  return formatManagedDocumentFileName({
    modeLabel,
    saveTitle: d ? d.saveTitle : story.saveTitle,
    chapterTitle: d ? d.chapterTitle : story.chapterTitle,
    titleOverride: editor.titleOverride,
  })
}
// 分层规范路径：<saveBaseDir>/<故事类型>/<索引名>/<规范文件名>。缺少根目录或
// 未选剧情（网页端/全新空文档）时返回 null——此时只有恢复文件兜底。
function canonicalSavePath(): string | null {
  const base = settings.settings.saveBaseDir
  const d = editor.docMeta
  const type = d ? d.type : story.selectedType
  const indexLabel = d ? d.indexLabel : story.selectedIndexLabel
  if (isDesktop && base && type && indexLabel) {
    const sep = (s: string) => s.replace(/[/\\]/g, '_')
    return `${base}/${sep(type)}/${sep(indexLabel)}/${canonicalFileName()}`
  }
  return null
}

const searchInputRef = ref<HTMLInputElement | null>(null)
function focusSearchInput() {
  nextTick(() => {
    searchInputRef.value?.focus()
    searchInputRef.value?.select()
  })
}

function onKeyDown(e: KeyboardEvent) {
  const el = document.activeElement
  const inTextInput = el instanceof HTMLElement &&
    (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA')
  const inContentEditable = el instanceof HTMLElement && el.isContentEditable
  const inEditable = inTextInput || inContentEditable

  const sc = settings.settings.shortcuts
  const hit = (id: string) => {
    if (id === 'redo' && isMac && e.metaKey && e.shiftKey && (e.key === 'z' || e.key === 'Z')) return true
    return matchEvent(e, resolveCombo(sc, id))
  }

  // Inside editable fields (contenteditable or inputs), let native text editing handle
  // intra-field undo/redo so typing doesn't revert whole document steps.
  if (hit('undo')) {
    if (!inEditable) { e.preventDefault(); doUndo() }
    return
  }
  if (hit('redo')) {
    if (!inEditable) { e.preventDefault(); doRedo() }
    return
  }

  if (hit('open')) { e.preventDefault(); handleOpen(); return }
  if (hit('save')) { e.preventDefault(); handleSave(); return }
  if (hit('search')) {
    e.preventDefault()
    app.searchOpen = !app.searchOpen
    if (app.searchOpen) focusSearchInput()
    return
  }
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
let leaveRecoverySync: Promise<void> | null = null

function syncRecoveryBeforeDeactivation(): Promise<void> {
  autoSave.stop()
  if (!leaveRecoverySync) {
    leaveRecoverySync = autoSave
      .syncNow(() => workspace.value?.flushPendingEditForDeactivation())
      .finally(() => { leaveRecoverySync = null })
  }
  return leaveRecoverySync
}

function withTimeout<T>(promise: Promise<T>, timeoutMs: number): Promise<T> {
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error('recovery sync timed out')), timeoutMs)
    promise.then(
      value => { clearTimeout(timeout); resolve(value) },
      error => { clearTimeout(timeout); reject(error) },
    )
  })
}

onBeforeRouteLeave(async () => {
  if (!editor.hasAnyUnsaved()) return true
  try {
    // The local backend normally finishes in milliseconds. Bound the wait so a
    // broken transport cancels navigation with feedback instead of hanging it.
    await withTimeout(syncRecoveryBeforeDeactivation(), 5_000)
    // If a later router guard cancels this navigation, recovery scheduling must
    // remain active; the actual deactivation below stops it again immediately.
    autoSave.start()
    return true
  } catch (error) {
    autoSave.start()
    toast.show('自动恢复快照写入失败，已留在编辑器，请重试', 'error')
    console.error('[Recovery] route-leave sync failed', error)
    return false
  }
})

function deactivate() {
  window.removeEventListener('keydown', onKeyDown)
  window.removeEventListener('resize', measureSearchAlign)
  // Fallback for non-router deactivation: keep navigation non-blocking while the
  // kept-alive editor finishes its durable snapshot in the background. Router
  // leaves already have one durable snapshot; this closes the final detach gap.
  void autoSave.stopAndSync(() => workspace.value?.flushPendingEditForDeactivation()).catch(() => {})
}

onMounted(async () => {
  // One-time setup only (registering onCloseRequested on every activation would
  // stack duplicate handlers); the listeners/autosave live in activate().
  // Mobile uses the Android activity/back lifecycle; desktop-only window close
  // interception would require permissions Android intentionally does not grant.
  if (!isDesktop) return
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
    if (!editor.hasAnyUnsaved()) {
      showCloseConfirm.value = false
      forceClose.value = true
      await closeWindow()
    } else {
      toast.show('其他模式仍有未保存的更改，请逐一保存后再退出', 'warn')
    }
  } catch { /* Save failed */ }
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
  if (open) {
    nextTick(measureSearchAlign)
    focusSearchInput()
  }
})

async function setMode(key: number) {
  const changed = key !== editor.currentMode
  if (changed && editor.documentBusy) return
  // Lock immediately, but advance the document revision only after the old
  // mode's save has finished. Otherwise a successful in-flight rename cannot
  // publish its new binding before saveModeState snapshots the old path.
  const operation = changed ? editor.beginDocumentOperation(false) : null
  if (changed && operation === null) return
  if (changed) story.loading = true
  try {
  // Flush pending row edits before swapping mode state. changeText may be the
  // only operation that materializes a missing dstTalks row, so cancelling here
  // can make visible text impossible to save even though onBlur updated talks.
  if (changed) await workspace.value?.flushPendingEdit()
  // Switching modes is not save intent. Persist only the crash-recovery snapshot;
  // the user-selected translation file is never changed until Save is pressed.
  if (changed && editor.hasUnsavedChanges) {
    await autoSave.syncNow().catch(error => {
      console.warn('[Recovery] mode-switch snapshot failed', error)
    })
  }
  if (operation !== null && !editor.advanceDocumentOperation(operation)) return
  editor.switchMode(key as 0 | 1 | 2)
  story.sourceTalks = JSON.parse(JSON.stringify(editor.sourceTalks))
  story.scenarioId = editor.docMeta?.scenarioId || ''
  story.saveTitle = editor.docMeta?.saveTitle || ''
  story.chapterTitle = editor.docMeta?.chapterTitle || ''
  if (editor.docMeta?.source) story.selectedSource = editor.docMeta.source
  app.setEditorMode(key as 0 | 1 | 2)
  // Switch undo/redo history to this mode's dedicated stack so edits made in
  // other modes remain recoverable when returning.
  if (changed) undo.switchMode(key)
  // 校对/合意 default to compare-on (baseline rows visible); 翻译 has no compare.
  app.showCompare = key >= 1
  // Entering 合意: remind the workflow (translation first, then proofread draft).
  if (key === 2 && !settings.settings.hideAgreementImportHint) {
    agreementHintDontShow.value = false
    showAgreementHint.value = true
  }
  } finally {
    if (changed) story.loading = false
    if (operation !== null) editor.finishDocumentOperation(operation)
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

async function handleOpen() {
  // Opening a file replaces the current document and clears the undo stack, so
  // unsaved work would be gone with no way back (and the next autosave tick
  // would overwrite the recovery file with the new document too). Confirm first.
  if (editor.documentBusy) return
  if (editor.hasAnyUnsaved()) {
    if (!(await confirm({ title: '打开文件', message: '有未保存的更改，打开新文件将丢弃它们。确定继续吗？', tone: 'danger', confirmText: '不保存并打开' }))) return
  }

  // Do not advance documentRevision or clear any live state while the picker,
  // resolver, source loader, aligner, or comparer is still working. The token
  // only prevents other document actions from starting; the candidate remains
  // entirely local until the final commit callback below.
  const operation = editor.beginDocumentOperation(false)
  if (operation === null) return
  try {
    const result = await fileDialog.openTranslation()
    if (!result) return

    const currentStory = story.scenarioId && story.sourceTalks.length > 0
      ? {
        story: {
          scenarioId: story.scenarioId,
          sourceTalks: cloneOpenDocument(story.sourceTalks),
          saveTitle: story.saveTitle,
          chapterTitle: story.chapterTitle,
          // The document snapshot is canonical; the navigator label is mutable
          // and may already point at another story.
          indexLabel: editor.docMeta?.indexLabel || story.selectedIndexLabel,
        },
        docMeta: editor.docMeta ? { ...editor.docMeta } : null,
      }
      : undefined

    const status = await runOpenDocumentTransaction({
      result,
      editorMode: app.editorMode,
      isAndroid: capabilities.isAndroid,
      currentStory,
      // Recovery is part of the same replacement transaction. If its queued
      // cleanup fails or this operation becomes stale, runOpenDocumentTransaction
      // never invokes the in-memory commit below and the old document stays live.
      beforeCommit: () => clearRecovery(),
      deps: {
        resolveLabel: label => api.resolveLabel(label),
        loadStory: request => api.storyLoad(request),
        checkLines: data => api.checkLines(data),
        compareText: data => api.compareText(data),
        isCurrent: () => editor.isCurrentDocumentOperation(operation),
      },
    }, candidate => {
      if (!editor.advanceDocumentOperation(operation)) return false
      // This is the first point at which it is safe to cancel the focused old
      // document edit: every replacement payload has already been validated.
      workspace.value?.cancelPendingEdit()
      editor.clearAll()
      editor.setTalks(candidate.talks, candidate.dstTalks, candidate.referTalks)
      editor.setSourceTalks(cloneOpenDocument(candidate.sourceTalks))
      editor.currentFilePath = candidate.currentFilePath
      editor.titleOverride = candidate.titleOverride
      editor.docMeta = { ...candidate.docMeta }
      editor.markSaved()

      // Story and editor identity are published as one synchronous state swap;
      // no intermediate empty/start-page state can be observed by a success UI.
      story.selectedSource = candidate.docMeta.source
      story.selectedType = candidate.docMeta.type
      story.selectedSort = candidate.docMeta.sort
      story.selectedIndex = candidate.docMeta.index
      story.selectedChapter = candidate.docMeta.chapter
      story.applyStory({
        ...candidate.story,
        // Commit the canonical document label, never a stale label captured
        // from mutable navigator state during current-story reuse.
        indexLabel: candidate.docMeta.indexLabel,
        sourceTalks: cloneOpenDocument(candidate.story.sourceTalks),
      })
      story.selectedIndexLabel = candidate.docMeta.indexLabel
      undo.clear()
    })
    if (status === 'stale') return

    if (editor.talks.length === 0 || editor.sourceTalks.length === 0 || !story.scenarioId) {
      throw new Error('opened document is not usable')
    }
    // Recovery cleanup already succeeded in beforeCommit, so the destination,
    // source, story identity, path/title, undo stack and recovery state now refer
    // to one committed document before success becomes visible.
    const identity = result.filePath || result.fileName || ''
    console.log('[Open] committed file', { path: identity, talkCount: editor.talks.length, hasMeta: !!result.meta, mode: app.editorMode })
    toast.show('已打开: ' + (identity.split(/[/\\]/).pop() || identity), 'success')
  } catch (e: any) {
    if (e instanceof OpenDocumentError && e.code === 'stale') return
    const detail = e instanceof OpenDocumentError && e.code === 'destination-only'
      ? '文件没有可用的剧情标识，未打开；请先载入对应剧情后再重试'
      : e instanceof OpenDocumentError && e.code === 'commit-preparation-failed'
        ? '自动恢复快照清理失败，未打开；当前文档保持不变'
        : (e.message || String(e))
    toast.show('打开失败: ' + detail, 'error')
  } finally {
    editor.finishDocumentOperation(operation)
  }
}

async function handleSave() {
  if (editor.documentBusy) return
  if (editor.talks.length === 0) return
  // Lock the document before enqueueing the entire flush + explicit save. This
  // keeps mode switches and save-directory migration from overtaking the picker
  // transaction while the focused row is being materialized.
  const operation = editor.beginDocumentOperation(false)
  if (operation === null) return
  try {
    await txtSaveCoordinator.run(async () => {
      await workspace.value?.flushPendingEdit()
      await saveCurrentMode()
    })
  } finally {
    editor.finishDocumentOperation(operation)
  }
}

async function finishCurrentSave(version: ReturnType<typeof editor.captureSaveVersion>): Promise<ManualSaveOutcome> {
  return completeManualSave({
    markSavedIfUnchanged: () => editor.markSavedIfUnchanged(version),
    // A save can clean only one mode. Rewrite immediately so a now-clean slot is
    // removed from Recovery V2 instead of waiting for the next interval. A file
    // write without this cleanup is not a fully successful save: the old recovery
    // snapshot could still reopen over the newly written file.
    syncRecovery: () => autoSave.syncNow(),
    restoreUnsavedIfUnchanged: () => {
      // Keep the document visibly dirty so the normal autosave loop retries the
      // recovery write/clear instead of claiming a clean document with stale
      // recovery state. Do not dirty a newer document or a document that was
      // already edited while recovery cleanup was in flight.
      const current = editor.captureSaveVersion()
      if (current.mode === version.mode
        && current.documentRevision === version.documentRevision
        && current.mutationSeq === version.mutationSeq) {
        editor.markUnsaved()
      }
    },
    onRecoveryError: error => console.warn('[Save] recovery cleanup failed after file write', error),
  })
}

function showManualSaveFeedback(outcome: ManualSaveOutcome) {
  const feedback = manualSaveFeedback(outcome)
  toast.show(feedback.message, feedback.tone)
}

function isCurrentSaveDocument(version: ReturnType<typeof editor.captureSaveVersion>): boolean {
  return editor.currentMode === version.mode && editor.documentRevision === version.documentRevision
}

async function saveCurrentMode() {
  if (editor.talks.length === 0) return
  const version = editor.captureSaveVersion()
  const dstTalks = JSON.parse(JSON.stringify(editor.dstTalks)) as typeof editor.dstTalks
  const meta = buildSaveMeta()
  const confirmOverwrite = (target: string, stale: boolean) => confirm({
    title: stale ? '文件已再次变化' : '覆盖现有文件',
    message: '目标位置已存在内容不同的文件。此操作会覆盖原有文件。',
    detail: `${target}${stale ? '\n该文件在上次确认后又被修改，请重新确认。' : ''}`,
    tone: 'danger',
    confirmText: '覆盖原有文件',
  })

  // Mode: confirmSavePath (true = 确认保存，每次弹窗; false = 静默保存，直接写入)
  const confirmSave = !!settings.settings.confirmSavePath
  const directPath = editor.currentFilePath || canonicalSavePath()
  const defaultName = directPath || canonicalFileName()

  console.log('[Save] starting save', { confirmSave, directPath, defaultName, talkCount: editor.talks.length, dstCount: editor.dstTalks.length, saveN: app.saveN, hasMeta: !!meta, isTauri })
  try {
    let path: string | null = null
    if (!confirmSave && directPath && isDesktop) {
      // 静默保存：直接保存到已有路径或规范分层路径，无需每次打开文件选择框
      if (capabilities.isAndroid) {
        path = await fileDialog.saveTranslation(directPath, dstTalks, app.saveN, meta, confirmOverwrite)
      } else {
        let expectedExistingDigest = ''
        for (let attempt = 0; attempt < 5; attempt++) {
          const result = await api.translationSave(directPath, dstTalks, app.saveN, meta, expectedExistingDigest)
          if (result.status === 'saved' || result.status === 'unchanged') {
            path = directPath
            break
          }
          if (!result.existingDigest) throw new Error('保存目标检查返回了无效结果')
          if (!(await confirmOverwrite(directPath, result.status === 'overwrite-stale'))) {
            path = null
            break
          }
          expectedExistingDigest = result.existingDigest
        }
      }
    } else {
      // 确认保存（或无有效目标路径）：打开系统保存文件对话框
      path = await fileDialog.saveTranslation(
        defaultName,
        dstTalks,
        app.saveN,
        meta,
        confirmOverwrite,
      )
    }

    if (!path) { console.log('[Save] cancelled by user'); return }
    if (isCurrentSaveDocument(version)) editor.currentFilePath = path
    if (isCurrentSaveDocument(version)) editor.syncTitleFromPath(path)
    const saveOutcome = await finishCurrentSave(version)
    console.log('[Save] saved successfully', { path, saveOutcome })
    showManualSaveFeedback(saveOutcome)
  } catch (e: any) {
    const detail = e.status ? `${e.status}: ${e.message}` : (e.message || String(e))
    console.error('[Save] failed', { error: detail, defaultName, talkCount: editor.talks.length, isTauri })
    toast.show('Save failed: ' + detail, 'error')
  }
}

async function handleClear() {
  if (editor.documentBusy) return
  // Always confirm — 清空 wipes the document AND the undo stack, so a stray
  // click is unrecoverable even with no unsaved changes.
  const detail = editor.hasUnsavedChanges ? '有未保存的更改，清空后无法找回。' : '清空后无法撤销。'
  if (!(await confirm({ title: '清空内容', message: '确定清空当前全部内容吗？', detail, tone: 'danger', confirmText: '清空' }))) return
  const operation = editor.beginDocumentOperation()
  if (operation === null) return
  try {
    editor.clearAll()
    story.clearLoadedStory()
    undo.clear()
    await autoSave.syncNow().catch(() => {})
    toast.show('已清空', 'info')
  } finally {
    editor.finishDocumentOperation(operation)
  }
}

// 合意: import a 校对稿 as the editable text, comparing it against the already
// loaded 翻译稿 (baseline). compareText pairs them by idx + sub-line position;
// the baseline row (yellow) shows the 翻译稿, the editable row (green) shows the
// 校对稿 — so the agreed edits are made on top of the proofread draft.
async function handleImportBaseline() {
  if (editor.documentBusy) return
  if (editor.talks.length === 0) { toast.show('请先载入翻译稿', 'warn'); return }
  const operation = editor.beginDocumentOperation(false)
  if (operation === null) return
  try {
    await workspace.value?.flushPendingEdit()
    if (!editor.isCurrentDocumentOperation(operation)) return
    const result = await fileDialog.openTranslation()
    if (!result) return

    const status = await runBaselineImportTransaction({
      result,
      currentTalks: cloneOpenDocument(editor.talks),
      currentReferTalks: cloneOpenDocument(editor.referTalks),
      sourceTalks: cloneOpenDocument(editor.sourceTalks),
      docMeta: editor.docMeta ? { ...editor.docMeta } : null,
      deps: {
        resolveLabel: label => api.resolveLabel(label),
        checkLines: data => api.checkLines(data),
        compareText: data => api.compareText(data),
        isCurrent: () => editor.isCurrentDocumentOperation(operation),
      },
    }, candidate => {
      if (!editor.advanceDocumentOperation(operation)) return false
      // Undo history changes only at the final synchronous commit. Empty,
      // mismatched, failed, or stale imports leave the current document and
      // its undo/redo stack exactly as they were.
      undo.pushSnapshot(editor.talks, editor.dstTalks)
      editor.setTalks(candidate.talks, candidate.dstTalks, candidate.referTalks)
      app.showCompare = true
      editor.titleOverride = candidate.titleOverride
      editor.markUnsaved()
    })
    if (status === 'committed') toast.show('已导入校对稿', 'success')
  } catch (e: any) {
    if (e instanceof BaselineImportError && e.code === 'stale') return
    toast.show('导入校对稿失败: ' + (e.message || '未知错误'), 'error')
  } finally {
    editor.finishDocumentOperation(operation)
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

async function handleReplaceCurrent() {
  if (editor.documentBusy) return
  const q = app.searchQuery.trim()
  if (!q) return
  await workspace.value?.flushPendingEdit()
  const repl = app.searchReplace
  let targetRow = -1
  for (let i = 0; i < editor.talks.length; i++) {
    const t = editor.talks[i]
    if (t.save && t.text && t.text.includes(q)) {
      targetRow = i
      break
    }
  }
  if (targetRow < 0) {
    toast.show('没有可替换的译文', 'warn')
    return
  }
  undo.pushSnapshot(editor.talks, editor.dstTalks)
  const currentText = editor.talks[targetRow].text
  const newText = currentText.replace(q, repl)
  try {
    const result = await api.changeText({
      row: targetRow,
      text: newText,
      editorMode: app.editorMode,
      talks: editor.talks,
      dstTalks: editor.dstTalks,
      referTalks: editor.referTalks,
    })
    editor.setTalks(result.talks, result.dstTalks, editor.referTalks)
    editor.markUnsaved()
    toast.show('已替换 1 处', 'success')
  } catch (e: any) {
    toast.show('替换失败: ' + (e?.message || '未知错误'), 'error')
  }
}

async function handleReplaceAll() {
  if (editor.documentBusy) return
  const q = app.searchQuery.trim()
  if (!q) return
  // Commit pending edits before taking the full-document snapshot. Cancelling can
  // drop a backend-materialized dstTalks row from the replacement input.
  await workspace.value?.flushPendingEdit()
  const repl = app.searchReplace
  let changed = 0
  const hasMatch = editor.talks.some(t => t.text && t.text.includes(q) && t.save)
  if (!hasMatch) { toast.show('没有可替换的译文', 'warn'); return }
  undo.pushSnapshot(editor.talks, editor.dstTalks)
  editor.markUnsaved()
  const committed = await commitDocumentMutation(
    () => ({ documentRevision: editor.documentRevision, mutationSeq: editor.mutationSeq }),
    async () => {
      const talks = JSON.parse(JSON.stringify(editor.talks)) as typeof editor.talks
      const dstTalks = JSON.parse(JSON.stringify(editor.dstTalks)) as typeof editor.dstTalks
      const referTalks = JSON.parse(JSON.stringify(editor.referTalks)) as typeof editor.referTalks

      for (let i = 0; i < talks.length; i++) {
        const talk = talks[i]
        if (talk.text && talk.text.includes(q) && talk.save) {
          const newText = talk.text.split(q).join(repl)
          talks[i].text = newText
          const di = talk.dstidx
          if (di >= 0 && di < dstTalks.length) {
            dstTalks[di].text = newText
          }
          changed++
        }
      }

      if (app.editorMode >= 1) {
        try {
          const res = await api.compare({
            referTalks,
            checkTalks: dstTalks,
            editorMode: app.editorMode,
          })
          return { talks: res.talks, dstTalks: res.dstTalks }
        } catch {
          return { talks, dstTalks }
        }
      } else {
        try {
          const checked = await api.checkLines({
            sourceTalks: editor.sourceTalks,
            loadedTalks: dstTalks,
          })
          return { talks, dstTalks: checked }
        } catch {
          return { talks, dstTalks }
        }
      }
    },
    result => editor.setTalks(result.talks, result.dstTalks, editor.referTalks),
  )
  if (committed && changed > 0) { editor.markUnsaved(); toast.show(`已替换 ${changed} 行`, 'success') }
  else toast.show('没有可替换的译文', 'warn')
}

function handleSpeakerBatchSave(speakers: { japanese: string; chinese: string }[]) {
  if (editor.documentBusy) return
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
  <div class="h-full min-h-0 page-bg flex flex-col">
    <header class="workspace-contextbar px-4 py-2 flex items-center gap-2 flex-wrap" data-tour="story-nav">
      <StoryNavigator :auto-pull="true"/>
      <div class="w-px h-5 bg-[var(--color-border)]" />
      <button @click="handleClear" :disabled="editor.documentBusy" class="btn btn-sm btn-ghost text-[var(--color-text-secondary)] hover:text-error hover:bg-error/10 gap-1.5 whitespace-nowrap"><Eraser :size="15" />清空</button>
    </header>
    <div class="workspace-commandbar editor-commandbar px-4 py-1.5">
      <div class="editor-toolbar-row flex items-center min-w-max" data-tour="toolbar">
        <div class="editor-mode-tabs" data-tour="modes">
          <button
            v-for="m in modes"
            :key="m.key"
            class="editor-mode-tab"
            :class="{ 'is-active': app.editorMode === m.key }"
            :disabled="editor.documentBusy"
            @click="setMode(m.key)"
          >{{ m.label }}</button>
        </div>
        <div class="w-px h-5 bg-[var(--color-border)] mx-3" />
        <div class="flex items-center gap-1 flex-wrap">
            <button @click="handleOpen" :disabled="editor.documentBusy" class="btn btn-sm btn-ghost gap-1.5"><FolderOpen :size="15" />{{ app.editorMode === 2 ? '导入翻译稿' : '打开' }}</button>
            <button v-if="app.editorMode === 2" @click="handleImportBaseline" :disabled="editor.documentBusy" :title="'导入校对稿 (' + formatCombo(resolveCombo(settings.settings.shortcuts, 'importBaseline')) + ')'" class="btn btn-sm btn-ghost gap-1.5"><FileInput :size="15" />导入校对稿</button>
            <!-- @click 显式调用，避免把 MouseEvent 传入保存流程。 -->
            <button @click="handleSave()" :disabled="editor.documentBusy" class="btn btn-sm btn-ghost gap-1.5"><Save :size="15" />保存</button>
            <button @click="doUndo" :disabled="editor.documentBusy || !canUndo" :title="'撤销最近一次修改 (' + formatCombo(resolveCombo(settings.settings.shortcuts, 'undo')) + ')'" class="btn btn-sm btn-ghost gap-1.5"><Undo2 :size="15" />撤销</button>
            <button @click="doRedo" :disabled="editor.documentBusy || !canRedo" :title="'重做 (' + formatCombo(resolveCombo(settings.settings.shortcuts, 'redo')) + ')'" class="btn btn-sm btn-ghost gap-1.5"><Redo2 :size="15" />重做</button>
            <div class="w-px h-5 bg-[var(--color-border)] mx-1" />
            <button class="tbar-toggle" :aria-pressed="app.showFlashback" @click="app.showFlashback = !app.showFlashback"><Eye :size="15" />闪回</button>
            <button class="tbar-toggle" :aria-pressed="app.showGlossary" @click="app.showGlossary = !app.showGlossary"><Languages :size="15" />术语</button>
            <button class="tbar-toggle" :aria-pressed="app.searchOpen" @click="app.searchOpen = !app.searchOpen"><Search :size="15" />搜索</button>
            <div ref="toolbarSearchSep" class="w-px h-5 bg-[var(--color-border)] mx-1" />
            <button @click="showSpeakerCheck = true" class="btn btn-sm btn-ghost gap-1.5"><Users :size="15" />说话人</button>
            <button @click="handleFullCheck" class="btn btn-sm btn-ghost gap-1.5"><ListChecks :size="15" />检查</button>
            <button @click="showSpeakerCount = true" class="btn btn-sm btn-ghost gap-1.5"><BarChart3 :size="15" />统计</button>
            <template v-if="app.editorMode >= 1">
              <div class="w-px h-5 bg-[var(--color-border)] mx-1" />
              <button class="tbar-toggle" :aria-pressed="app.showCompare" @click="app.showCompare = !app.showCompare"><Columns2 :size="15" />对比</button>
            </template>
          </div>
      </div>
          <!-- Search / replace bar. The left group is width-matched to the
               toolbar so the divider sits directly under the toolbar's
               "搜索"-right divider. -->
          <div v-if="app.searchOpen" ref="searchBarRow" class="flex items-center gap-2 mt-2">
            <div class="flex items-center gap-2" :style="{ width: searchLeftWidth + 'px' }">
              <input
                ref="searchInputRef"
                v-model="app.searchQuery"
                type="text"
                placeholder="查找(原文/译文/说话人)"
                class="app-input flex-1 min-w-0"
                @keydown.enter="searchNext"
                @keydown.esc="app.searchOpen = false"
              />
              <span class="text-xs text-[var(--color-text-secondary)] tabular-nums flex-shrink-0">{{ searchCount }}</span>
              <button @click="searchPrev" class="btn btn-xs btn-ghost">上一个</button>
              <button @click="searchNext" class="btn btn-xs btn-ghost">下一个</button>
            </div>
            <div class="w-px h-5 bg-[var(--color-border)]" />
            <input
              v-model="app.searchReplace"
              type="text"
              placeholder="替换为(仅译文)"
              class="app-input w-56"
              @keydown.esc="app.searchOpen = false"
            />
            <button @click="handleReplaceCurrent" class="btn btn-sm btn-ghost border border-[var(--color-border)]">替换当前</button>
            <button @click="handleReplaceAll" class="btn btn-sm btn-ghost border border-[var(--color-border)]">全部替换</button>
            <button @click="app.searchOpen = false" class="btn btn-xs btn-ghost text-[var(--color-text-secondary)] hover:text-[var(--color-text)]" title="关闭搜索 (Esc)">
              <X :size="14" />
            </button>
          </div>
    </div>
    <main
      class="editor-stage flex-1 min-h-0 flex"
      :class="[dockSide === 'top' || dockSide === 'bottom' ? 'flex-col' : 'flex-row']"
    >
      <Live2DDock v-if="dockSide === 'top'" placement="top" />
      <div class="flex-1 min-w-0 min-h-0" data-tour="workspace"><EditorWorkspace ref="workspace"/></div>
      <Live2DDock v-if="dockSide === 'right'" placement="right" />
      <Live2DDock v-if="dockSide === 'bottom'" placement="bottom" />
    </main>
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
.workspace-contextbar {
  min-height: 3.75rem;
  background: color-mix(in oklch, var(--color-surface) 96%, var(--color-bg));
  border-bottom: 1px solid var(--color-border);
}
.editor-commandbar { overflow-x: auto; }
.editor-toolbar-row { min-height: 2rem; }
.editor-mode-tabs {
  align-self: stretch;
  display: flex;
  align-items: center;
  gap: 1.25rem;
}
.editor-mode-tab {
  position: relative;
  align-self: stretch;
  padding: 0 0.05rem;
  border: 0;
  color: var(--color-text-secondary);
  background: transparent;
  font-size: 0.78rem;
  transition: color var(--dur-fast);
}
.editor-mode-tab:hover { color: var(--color-text); }
.editor-mode-tab.is-active { color: var(--color-text); font-weight: 750; }
.editor-mode-tab.is-active::after {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  bottom: -0.42rem;
  height: 2px;
  border-radius: 2px;
  background: var(--accent, var(--color-primary));
}
.editor-stage {
  padding: 0.75rem 0.9rem 0.9rem;
  background-image: linear-gradient(135deg, color-mix(in oklch, var(--color-base-content) 1.2%, transparent) 0 1px, transparent 1px 100%);
  background-size: 2.9rem 2.9rem;
}

@media (max-width: 767px) {
  .workspace-contextbar {
    max-height: 38vh;
    min-height: auto;
    padding: 0.6rem;
    overflow: auto;
  }
  .workspace-contextbar > .w-px {
    display: none;
  }
  .editor-commandbar {
    padding-inline: 0.55rem;
  }
  .editor-toolbar-row {
    align-items: stretch;
    flex-direction: column;
    min-width: 0;
    gap: 0.4rem;
  }
  .editor-toolbar-row > .w-px {
    display: none;
  }
  .editor-mode-tabs {
    min-height: 2.5rem;
    justify-content: space-around;
    gap: 0.5rem;
  }
  .editor-mode-tab {
    flex: 1;
    text-align: center;
  }
  .editor-stage {
    padding: 0.45rem;
  }
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
  transition: background-color var(--dur-fast) var(--ease-out), color var(--dur-fast) var(--ease-out),
    translate 320ms var(--ease-out), scale 320ms var(--ease-out), box-shadow 140ms var(--ease-out);
}
/* 立体按压：scoped transition（未分层+更高特异性）会盖过全局 button 的声明，
   弹簧通道须在此重申；下沉的 translate/scale 值直接来自全局 button:active */
@supports (transition-timing-function: linear(0, 1)) {
  .tbar-toggle {
    transition: background-color var(--dur-fast) var(--ease-out), color var(--dur-fast) var(--ease-out),
      translate 320ms linear(0, 0.2375, 0.5904, 0.8358, 0.9599, 1.0061, 1.0152, 1.0116, 1.0062, 1.0025, 1.0006, 0.9999, 1),
      scale 320ms linear(0, 0.2375, 0.5904, 0.8358, 0.9599, 1.0061, 1.0152, 1.0116, 1.0062, 1.0025, 1.0006, 0.9999, 1),
      box-shadow 140ms var(--ease-out);
  }
}
.tbar-toggle:active {
  transition-duration: 70ms;
  transition-timing-function: ease-out;
  box-shadow: inset 0 1.5px 3px rgb(0 0 0 / 0.25);
}
@media (prefers-reduced-motion: reduce) {
  .tbar-toggle:active { box-shadow: none; }
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
