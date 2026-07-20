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
import { matchEvent, resolveCombo, formatCombo } from '../constants/shortcuts'
import { api } from '../api/client'
import { Users, AlertTriangle, Info,
  FolderOpen, Save, Eraser, Eye, Languages, Search, Columns2, ListChecks, BarChart3, FileInput, Undo2, Redo2 } from 'lucide-vue-next'
import { getCurrentWindow } from '@tauri-apps/api/window'
import StoryNavigator from '../components/navigation/StoryNavigator.vue'
import EditorWorkspace from '../components/editor/EditorWorkspace.vue'
import Live2DDock from '../components/live2d/Live2DDock.vue'
import { useLive2dDockStore } from '../stores/live2dDock'
import SpeakerCountDialog from '../components/dialogs/SpeakerCountDialog.vue'
import SpeakerCheckDialog from '../components/dialogs/SpeakerCheckDialog.vue'
import { usePluginRegistry } from '../plugin-host/registry'
import { commitDocumentMutation } from '../editor/documentMutation'
import { saveDirectoryCoordinator } from '../editor/saveDirectoryCoordinator'

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
const autoSave = useAutoSave(30000, () => workspace.value?.flushPendingEdit())

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

const isTauri = typeof window !== 'undefined' && !!(window as any).__TAURI_INTERNALS__

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

// ── 逐行落盘的自动保存（写正式文件本体）──────────────────────────────────────
// 每次内容变更（mutationSeq：行编辑/增删行/撤销重做/全部替换…）后短防抖，把当前
// 译文写进「当前文档本体」：已打开/已保存的文件直接更新；从未落盘的文档则按选中
// 模式自动创建规范命名的 txt（<保存根目录>/<类型>/<索引>/【模式】….txt）并绑定为
// 当前文档。与 30s 的恢复文件（dataDir，启动时提示恢复）互补，不替代。
let txtAutosaveTimer: ReturnType<typeof setTimeout> | null = null
function scheduleTxtAutosave() {
  if (editor.recoveryPending || !editor.hasUnsavedChanges) return
  if (txtAutosaveTimer) clearTimeout(txtAutosaveTimer)
  txtAutosaveTimer = setTimeout(() => { txtAutosaveTimer = null; void writeTxtAutosave() }, 800)
}
// 所有 txt 落盘（防抖自动保存/切模式冲写/手动保存前的排空）走同一条串行链：
// 「就地改名(A→B)+写入」不是原子操作，两路并发时输家的 rename 失败会回落重建
// 旧名文件——标题改名丢失、还多出一份孤儿档。
const txtSaveCoordinator = saveDirectoryCoordinator
function writeTxtAutosave(): Promise<void> {
  return txtSaveCoordinator.run(async () => {
    // Materialize both debounced rows and the currently focused contenteditable
    // before any save snapshot/version is captured. Keeping this inside the save
    // queue prevents manual save or mode switch from overtaking the flush.
    if (editor.recoveryPending) return
    await workspace.value?.flushPendingEdit()
    if (editor.recoveryPending) return
    await doWriteTxtAutosave()
  })
}
watch(() => editor.mutationSeq, scheduleTxtAutosave)
// Programmatic title changes (open/import/path sync) do not necessarily bump the
// mutation sequence, so a bound managed document still watches the title itself.
watch(() => editor.titleOverride, () => {
  if (editor.currentFilePath) scheduleTxtAutosave()
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
// 规范文件名：【模式】<剧本标号> <标题>.txt——保存对话框、自动建档共用一套。
function canonicalFileName(): string {
  const modeLabel = EditorModeLabel[app.editorMode as 0 | 1 | 2]
  const d = editor.docMeta
  const saveTitle = d ? d.saveTitle : story.saveTitle
  const chapterTitle = d ? d.chapterTitle : story.chapterTitle
  const title = (editor.titleOverride || chapterTitle || '').trim()
  let fileName = '【' + modeLabel + '】' + (saveTitle || 'untitled')
  if (title) fileName += ' ' + title
  return fileName + '.txt'
}
// 分层规范路径：<saveBaseDir>/<故事类型>/<索引名>/<规范文件名>。缺少根目录或
// 未选剧情（网页端/全新空文档）时返回 null——此时只有恢复文件兜底。
function canonicalSavePath(): string | null {
  const base = settings.settings.saveBaseDir
  const d = editor.docMeta
  const type = d ? d.type : story.selectedType
  const indexLabel = d ? d.indexLabel : story.selectedIndexLabel
  if (isTauri && base && type && indexLabel) {
    const sep = (s: string) => s.replace(/[/\\]/g, '_')
    return `${base}/${sep(type)}/${sep(indexLabel)}/${canonicalFileName()}`
  }
  return null
}
// 已绑定文档的写入目标：规范文件名（【模式】前缀 + 标题段）变了就就地改名跟上
// ——标题译文往往在首次自动建档（首编辑后 800ms）之后才填，绑定路径若一直冻结，
// 标题/模式就永远进不了文件名（用户反馈：改了标题文件名不变）。只接管【开头的
// 托管命名；用户自定义名/目录不动。改名只换 basename，文件所在目录保持不变。
function resolveBoundTarget(): { path: string; renameFrom?: string } | null {
  const bound = editor.currentFilePath
  if (!bound || !/[/\\]/.test(bound)) return null
  const m = bound.match(/^(.*[/\\])([^/\\]+)$/)
  if (!m) return { path: bound }
  const [, dir, base] = m
  if (!base.startsWith('【')) return { path: bound }
  // 没有文档身份快照（docMeta）时绝不改名：canonicalFileName 会退回全局 story
  // 状态，可能算出 untitled 或别的剧情的名字，把文件静默改错——只就地写内容。
  if (!editor.docMeta) return { path: bound }
  // 托管根目录内连“文件夹”一起跟随规范路径：索引标签修正后（208 → 208 褪せない
  // 今を、彩って）下次保存把文件搬进正确目录，同一活动不再出现两个文件夹；
  // 根目录外（用户另存到别处）只换 basename，不把文件挪走。
  // 只有持有文档身份快照（docMeta）时才跟随文件夹——否则规范路径读的是全局
  // story 状态（可能是裸标签/别的剧情），按它搬文件等于把文档搬错目录。
  const root = settings.settings.saveBaseDir
  const managed = root && editor.docMeta
    && (bound.startsWith(root + '/') || bound.startsWith(root + '\\'))
  const want = (managed ? canonicalSavePath() : null) || dir + canonicalFileName()
  if (want === bound) return { path: bound }
  return { path: want, renameFrom: bound }
}
async function doWriteTxtAutosave() {
  if (editor.talks.length === 0 || !isTauri) return
  // 定时器可能在切模式后才触发：进入 await 前把本槽位的一切快照下来，写完回来
  // 若槽位已换人（loadModeState 换掉了整套状态），不得把路径/脏标记写进新模式。
  const mode = editor.currentMode
  const dstTalks = JSON.parse(JSON.stringify(editor.dstTalks)) as typeof editor.dstTalks
  const meta = buildSaveMeta()
  const saveVersion = editor.captureSaveVersion()
  let target = resolveBoundTarget()
  let binding = false
  if (!target) {
    const canonical = canonicalSavePath()
    if (!canonical) return
    target = { path: canonical }
    binding = true
  }
  // 写盘期间若有新编辑（mutationSeq 变化），写入内容已过期，不清脏标记——
  // 下一轮防抖会再写一次。
  const seq = editor.mutationSeq
  try {
    if (target.renameFrom) {
      try {
        await api.renameFile(target.renameFrom, target.path)
        if (editor.currentMode === mode && editor.documentRevision === saveVersion.documentRevision) {
          editor.currentFilePath = target.path
        }
      } catch (e) {
        // 目标被占用等：保持原名原路径写入，名字不动但内容绝不丢。
        console.warn('[Autosave] 就地改名失败，保持原名', target, e)
        target = { path: target.renameFrom }
      }
    }
    if (binding) await api.ensureDir(target.path)
    await api.translationSave(target.path, dstTalks, app.saveN, meta)
    if (editor.currentMode !== mode || editor.documentRevision !== saveVersion.documentRevision) return
    if (binding) editor.currentFilePath = target.path
    if (editor.mutationSeq === seq && editor.markSavedIfUnchanged(saveVersion)) {
      // 正式文件已是最新，作废恢复快照——否则启动时会把这份陈旧快照当作未保存
      // 更改恢复，盖回真实文件丢译文。只在写成功且内容未过期时清。
      await autoSave.syncNow().catch(() => {})
    }
  } catch (e) {
    console.warn('[Autosave] 自动保存写入失败', target.path, e) // 静默失败，不打扰编辑
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
  if (open) nextTick(measureSearchAlign)
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
  // 冲洗（不是丢弃）待写的 txt 自动保存：那个定时器属于旧模式的编辑，火在切换
  // 之后会按新模式的槽位落盘——旧模式的最后一笔就滞留缓存、新模式凭空建档。
  // 必须 await：writeTxtAutosave 里的就地改名是异步的，改名后才把 currentFilePath
  // 更新为新路径；不等它就 switchMode，saveModeState 会把已被改走的旧路径快照进
  // modeCache，切回该模式即 409 回落重建同名孤儿文件。
  if (changed) {
    if (txtAutosaveTimer) clearTimeout(txtAutosaveTimer)
    txtAutosaveTimer = null
    // Recovered rows are only a proposed recovery state. Switching tabs is not
    // edit/save intent, so do not write an untouched recovered slot over its
    // original file. recoveryPending is restored independently for every mode.
    if (editor.hasUnsavedChanges && !editor.recoveryPending) await writeTxtAutosave().catch(() => {})
    else await txtSaveCoordinator.wait()
  }
  if (operation !== null && !editor.advanceDocumentOperation(operation)) return
  editor.switchMode(key as 0 | 1 | 2)
  story.sourceTalks = JSON.parse(JSON.stringify(editor.sourceTalks))
  story.scenarioId = editor.docMeta?.scenarioId || ''
  story.saveTitle = editor.docMeta?.saveTitle || ''
  story.chapterTitle = editor.docMeta?.chapterTitle || ''
  if (editor.docMeta?.source) story.selectedSource = editor.docMeta.source
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
  const operation = editor.beginDocumentOperation()
  if (operation === null) return
  story.loading = true
  try {
    const result = await fileDialog.openTranslation()
    if (!result) return
    if (!editor.isCurrentDocumentOperation(operation)) return
    workspace.value?.cancelPendingEdit()
    editor.clearAll()
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
    editor.setSourceTalks([])
    story.clearLoadedStory()
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
    // meta.mode 缺失（老版本/手工文件）时用文件名的【前缀】兜底再默认翻译——
    // 否则【校对】命名的老文件会被当成翻译稿绑定改写（名实不符的模式错乱）。
    const prefixMode = ({ 翻译: 0, 校对: 1, 合意: 2 } as Record<string, 0 | 1 | 2>)[
      rawName.match(/^【([^】]*)】/)?.[1] ?? ''
    ]
    const fileMode = (result.meta?.mode ?? prefixMode ?? 0) as 0 | 1 | 2
    const deriving = fileMode !== app.editorMode
    editor.currentFilePath = deriving ? '' : (result.filePath || result.fileName || '')
    // 新文档会话：先清掉上一个文档的身份快照，标签解析成功后再重新绑定。
    // 留着旧快照会让这个文件保存时套上一个剧情的名字/目录。
    editor.docMeta = null
    editor.markSaved()
    await autoSave.syncNow().catch(() => {})
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
          // 后端直接给完整标签（"208 褪せない今を、彩って"）；退回列表查找或裸索引
          // 会让文稿目录出现光秃秃的「208」文件夹，与导航载入建的目录并存。
          story.selectedIndexLabel = r.indexLabel
            || story.indices.find(i => i.value === r.index)?.label || r.index
          await story.fetchChapters(r.storyType, '', r.index)
          await nextTick()
          story.selectedChapter = r.chapter
          // Keep the outer document operation in charge of loading state. The
          // store's loadStory() releases story.loading after only the source
          // request, which used to expose editable rows before alignment landed.
          const loadedStory = await story.fetchStory()
          if (loadedStory.sourceTalks.length > 0) {
            const aligned = await api.checkLines({ sourceTalks: loadedStory.sourceTalks, loadedTalks: result.talks })
            if (app.editorMode >= 1) {
              // Derive baseline rows for compare (校对/合意).
              const compared = await api.compareText({ referTalks: aligned, checkTalks: aligned, editorMode: app.editorMode })
              editor.setTalks(compared.talks, compared.dstTalks, aligned)
            } else {
              editor.setTalks(aligned, aligned, [])
            }
          }
          story.applyStory(loadedStory)
          editor.setSourceTalks(loadedStory.sourceTalks)
          editor.docMeta = story.snapshotDocMeta()
        }
      } catch { /* keep manual selection */ }
    }
    toast.show('已打开: ' + (label || rawName), 'success')
  } catch (e: any) { toast.show('Open failed: ' + (e.message || String(e)), 'error') }
  finally {
    story.loading = false
    editor.finishDocumentOperation(operation)
  }
}

async function handleSave(saveAs = false) {
  if (editor.documentBusy) return
  if (editor.talks.length === 0) return
  // Lock the document before enqueueing the ENTIRE flush + save transaction.
  // Otherwise a mode switch can enter the queue while flushPendingEdit is still
  // materializing rows and overtake the actual manual save.
  const operation = editor.beginDocumentOperation(false)
  if (operation === null) return
  try {
    await txtSaveCoordinator.run(async () => {
      if (txtAutosaveTimer) clearTimeout(txtAutosaveTimer)
      txtAutosaveTimer = null
      await workspace.value?.flushPendingEdit()
      if (txtAutosaveTimer) clearTimeout(txtAutosaveTimer)
      txtAutosaveTimer = null
      await saveCurrentMode(saveAs)
    })
  } finally {
    editor.finishDocumentOperation(operation)
  }
}

async function finishCurrentSave(version: ReturnType<typeof editor.captureSaveVersion>) {
  if (!editor.markSavedIfUnchanged(version)) return
  // A save can clean only one mode. Rewrite immediately so a now-clean slot is
  // removed from Recovery V2 instead of waiting for the next interval.
  await autoSave.syncNow().catch(() => {})
}

function isCurrentSaveDocument(version: ReturnType<typeof editor.captureSaveVersion>): boolean {
  return editor.currentMode === version.mode && editor.documentRevision === version.documentRevision
}

async function saveCurrentMode(saveAs: boolean) {
  if (editor.talks.length === 0) return
  const version = editor.captureSaveVersion()
  const dstTalks = JSON.parse(JSON.stringify(editor.dstTalks)) as typeof editor.dstTalks
  const meta = buildSaveMeta()
  // 保存 = 直接写当前文档本体（已打开/已保存过的文件），不再弹对话框重建文件。
  // 只有从未落盘、或用户点了「另存为」、或直写失败（原目录被删等）才走对话框。
  const bound = editor.currentFilePath
  if (!saveAs && isTauri && bound && /[/\\]/.test(bound)) {
    // 同 autosave：规范名（标题译文/模式标签）变了就先就地改名再写。
    let target = resolveBoundTarget() || { path: bound }
    try {
      if (target.renameFrom) {
        try {
          await api.renameFile(target.renameFrom, target.path)
          if (isCurrentSaveDocument(version)) editor.currentFilePath = target.path
        } catch (e: any) {
          console.warn('[Save] 就地改名失败，保持原名', { target, error: e?.message || String(e) })
          target = { path: target.renameFrom }
        }
      }
      await api.translationSave(target.path, dstTalks, app.saveN, meta)
      await finishCurrentSave(version)
      console.log('[Save] saved in place', { path: target.path })
      toast.show('已保存', 'success')
      return
    } catch (e: any) {
      console.warn('[Save] in-place save failed, falling back to dialog', { path: target.path, error: e?.message || String(e) })
    }
  }
  // 首次落盘也不问位置：直接按 <保存根目录>/<类型>/<索引>/ 分级自动建档并绑定。
  if (!saveAs && isTauri) {
    const canonical = canonicalSavePath()
    if (canonical) {
      try {
        await api.ensureDir(canonical)
        await api.translationSave(canonical, dstTalks, app.saveN, meta)
        if (isCurrentSaveDocument(version)) editor.currentFilePath = canonical
        await finishCurrentSave(version)
        console.log('[Save] auto-bound canonical path', { path: canonical })
        toast.show('已保存', 'success')
        return
      } catch (e: any) {
        console.warn('[Save] canonical save failed, falling back to dialog', { canonical, error: e?.message || String(e) })
      }
    }
  }
  // 兜底对话框：算不出规范路径（未选剧情的空白文档/网页端）或上面写入失败。
  // The 译文 header input (editor.titleOverride) owns the title segment. The
  // filename is always rebuilt as 【模式】<saveTitle> <title>.txt — the prefix
  // and .txt suffix are fixed, only the title part is user-editable. Empty
  // override falls back to the story's chapterTitle.
  const defaultName = canonicalSavePath() || canonicalFileName()
  console.log('[Save] starting save', { defaultName, talkCount: editor.talks.length, dstCount: editor.dstTalks.length, saveN: app.saveN, hasMeta: !!meta, isTauri: isTauri })
  try {
    const path = await fileDialog.saveTranslation(defaultName, dstTalks, app.saveN, meta)
    if (!path) { console.log('[Save] cancelled by user'); return }
    if (isCurrentSaveDocument(version)) editor.currentFilePath = path
    // 用户在对话框里改过标题段的话，同步回 titleOverride——否则下一次保存按
    // 旧标题重算规范名，把文件名又改回去。
    if (isCurrentSaveDocument(version)) editor.syncTitleFromPath(path)
    await finishCurrentSave(version)
    // Awaited: 保存并退出 destroys the window right after handleSave returns, so a
    // fire-and-forget clear could be cut off — leaving a STALE autosave that the
    // next launch offers as "recovery" over the newer just-saved file.
    console.log('[Save] saved successfully', { path })
    toast.show('已保存', 'success')
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
    const result = await fileDialog.openTranslation()
    if (!result) return
    undo.pushSnapshot(editor.talks, editor.dstTalks)
    const committed = await commitDocumentMutation(
      () => ({ documentRevision: editor.documentRevision, mutationSeq: editor.mutationSeq }),
      async () => {
        // Align the imported 校对稿 to the source story before comparing. A
        // freshly parsed txt has positional indices, while the current rows are
        // aligned to source indices.
        let checkTalks = result.talks
        if (story.sourceTalks.length > 0) {
          checkTalks = await api.checkLines({ sourceTalks: story.sourceTalks, loadedTalks: result.talks })
        } else {
          toast.show('未加载原文，对比可能错位，请先选择剧情', 'warn')
        }
        const compared = await api.compareText({
          referTalks: editor.talks,
          checkTalks,
          editorMode: 2,
        })
        return { compared, imported: result }
      },
      ({ compared, imported }) => {
        editor.advanceDocumentOperation(operation)
        editor.setTalks(compared.talks, compared.dstTalks, editor.referTalks)
        app.showCompare = true
        const impName = (imported.filePath || imported.fileName || '').split(/[/\\]/).pop() || ''
        const impBase = impName.replace(/\.txt$/i, '').replace(/^【[^】]*】/, '').trim()
        const impTitle = impBase.slice((impBase.split(/\s+/)[0] || '').length).trim()
        if (impTitle) editor.titleOverride = impTitle
        editor.markUnsaved()
      },
    )
    if (committed) toast.show('已导入校对稿', 'success')
  } catch (e: any) {
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
  // Invalidate an already-dispatched line edit before its full-table response
  // can land over this replacement. The replacement requests begin below.
  editor.markUnsaved()
  const committed = await commitDocumentMutation(
    () => ({ documentRevision: editor.documentRevision, mutationSeq: editor.mutationSeq }),
    async () => {
      let talks = JSON.parse(JSON.stringify(editor.talks)) as typeof editor.talks
      let dstTalks = JSON.parse(JSON.stringify(editor.dstTalks)) as typeof editor.dstTalks
      const referTalks = JSON.parse(JSON.stringify(editor.referTalks)) as typeof editor.referTalks
      // Route each affected row through the backend, but keep intermediate full-
      // table responses local. Only the final snapshot may commit to the store.
      for (let i = 0; i < talks.length; i++) {
        const talk = talks[i]
        if (talk.text && talk.text.includes(q) && talk.save) {
          const newText = talk.text.split(q).join(repl)
          try {
            const result = await api.changeText({
              row: i, text: newText, editorMode: app.editorMode,
              talks, dstTalks, referTalks,
            })
            talks = result.talks
            dstTalks = result.dstTalks
            changed++
          } catch { /* skip row */ }
        }
      }
      return { talks, dstTalks }
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
            <!-- @click 必须写 handleSave()：裸引用会把 MouseEvent 当 saveAs 传入 -->
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
