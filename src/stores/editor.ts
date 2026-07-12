import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { DstTalk, SourceTalk, EditorMode } from '../types/translation'

// 文档身份快照：载入内容时从 story store 拷一份，此后保存命名/元数据一律读快照。
// story 的选择状态是全局的，载入后再去下载页/其它模式拉别的剧情就会被改走——
// 若保存时才读全局状态，当前文档会被存成别的剧情的文件名（用户反馈：编辑前篇
// 存成了后篇的文件）。
export interface DocMeta {
  saveTitle: string
  chapterTitle: string
  type: string
  sort: string
  index: string
  indexLabel: string
  chapter: number
  source: string
  scenarioId: string
}

interface ModeState {
  talks: DstTalk[]
  dstTalks: DstTalk[]
  referTalks: DstTalk[]
  currentFilePath: string
  titleOverride: string
  hasUnsavedChanges: boolean
  majorClue: string | null
  docMeta: DocMeta | null
}

function emptyModeState(): ModeState {
  return {
    talks: [],
    dstTalks: [],
    referTalks: [],
    currentFilePath: '',
    titleOverride: '',
    hasUnsavedChanges: false,
    majorClue: null,
    docMeta: null,
  }
}

function cloneTalks<T>(arr: T[]): T[] {
  // Deep copy: DstTalk holds nested arrays (diff, voices). A shallow {...t}
  // would leave those shared between modeCache and live talks, so an in-place
  // nested mutation in one mode would leak across modes. Matches useUndo.
  return JSON.parse(JSON.stringify(arr)) as T[]
}

export const useEditorStore = defineStore('editor', () => {
  const talks = ref<DstTalk[]>([])
  const dstTalks = ref<DstTalk[]>([])
  const referTalks = ref<DstTalk[]>([])
  const sourceTalks = ref<SourceTalk[]>([])

  const currentFilePath = ref('')
  // User-editable title segment shown in the 译文 header input. Replaces ONLY
  // the chapter-title part of the saved filename (the 【模式】<saveTitle> prefix
  // and .txt suffix stay fixed). Empty = fall back to the story's chapterTitle.
  // Part of the per-mode document identity (cached in ModeState like docMeta):
  // each mode slot names its own file.
  const titleOverride = ref('')

  // 从既有规范命名（【模式】<剧本标号> <标题>.txt）回同步标题段。绑定到已存在
  // 文件（恢复、保存对话框选路径）时调用——否则 titleOverride 空值会让下一次
  // 保存把文件名改回日文原标题。
  function syncTitleFromPath(path: string) {
    const base = path.split(/[/\\]/).pop() || ''
    if (!base.startsWith('【')) return
    const stripped = base.replace(/\.txt$/i, '').replace(/^【[^】]*】/, '').trim()
    const label = stripped.split(/\s+/)[0] || ''
    const titlePart = stripped.slice(label.length).trim()
    if (titlePart) titleOverride.value = titlePart
  }
  const hasUnsavedChanges = ref(false)
  const majorClue = ref<string | null>(null)
  const docMeta = ref<DocMeta | null>(null)

  const currentMode = ref<EditorMode>(0)
  const modeCache = new Map<EditorMode, ModeState>()

  function setSourceTalks(talks: SourceTalk[]) {
    sourceTalks.value = talks
  }

  function setTalks(newTalks: DstTalk[], newDstTalks: DstTalk[], newReferTalks: DstTalk[]) {
    talks.value = newTalks
    dstTalks.value = newDstTalks
    referTalks.value = newReferTalks
  }

  // Monotonic edit counter: bumps on EVERY content mutation (line edit, add/
  // remove, undo/redo, replace-all …). hasUnsavedChanges flips once and stays,
  // so per-edit consumers (the autosave.txt writer) watch this instead.
  const mutationSeq = ref(0)

  function markUnsaved() {
    hasUnsavedChanges.value = true
    mutationSeq.value++
  }

  // Dirty check across ALL modes: hasUnsavedChanges only reflects the current
  // mode; edits parked in another mode's cache slot (switchMode deep-copies
  // state per mode) would otherwise let the app quit without any warning.
  function hasAnyUnsaved(): boolean {
    if (hasUnsavedChanges.value) return true
    for (const [mode, state] of modeCache) {
      if (mode !== currentMode.value && state.hasUnsavedChanges) return true
    }
    return false
  }

  function markSaved() {
    hasUnsavedChanges.value = false
  }

  function clearAll() {
    talks.value = []
    dstTalks.value = []
    referTalks.value = []
    sourceTalks.value = []
    currentFilePath.value = ''
    titleOverride.value = ''
    hasUnsavedChanges.value = false
    majorClue.value = null
    docMeta.value = null
    modeCache.clear()
  }

  function saveModeState(mode: EditorMode) {
    modeCache.set(mode, {
      talks: cloneTalks(talks.value),
      dstTalks: cloneTalks(dstTalks.value),
      referTalks: cloneTalks(referTalks.value),
      currentFilePath: currentFilePath.value,
      titleOverride: titleOverride.value,
      hasUnsavedChanges: hasUnsavedChanges.value,
      majorClue: majorClue.value,
      docMeta: docMeta.value ? { ...docMeta.value } : null,
    })
  }

  function loadModeState(mode: EditorMode) {
    const state = modeCache.get(mode) || emptyModeState()
    talks.value = cloneTalks(state.talks)
    dstTalks.value = cloneTalks(state.dstTalks)
    referTalks.value = cloneTalks(state.referTalks)
    currentFilePath.value = state.currentFilePath
    titleOverride.value = state.titleOverride
    hasUnsavedChanges.value = state.hasUnsavedChanges
    majorClue.value = state.majorClue
    docMeta.value = state.docMeta ? { ...state.docMeta } : null
  }

  // Switch between editor modes. Each mode owns a fully independent, deep-copied
  // state — editing one mode must never mutate another. There is no cross-mode
  // seeding: deriving a proofread/agreement baseline from a translation is an
  // explicit load-time action (see EditorPage.handleOpen), not a side effect of
  // switching tabs.
  // 保存根目录迁移后，把所有模式里已绑定的文档路径从旧根改写到新根——
  // 否则 autosave 会把译文写回旧位置，把刚迁走的目录又建回来。
  // skipRel：迁移时因同名冲突未搬走、仍留在旧目录的文件相对路径。这些绑定必须
  // 继续指向旧目录的原文件，绝不改写到新根那个内容不同的同名陌生文件（否则下次
  // 自动保存会覆盖它、丢掉原稿）——译文数据安全高于一切。
  function rebindPaths(oldRoot: string, newRoot: string, skipRel?: string[]) {
    if (!oldRoot || oldRoot === newRoot) return
    const norm = (p: string) => p.replace(/\\/g, '/').replace(/^\/+/, '')
    const skip = new Set((skipRel || []).map(norm))
    const rewrite = (p: string) => {
      if (!p || !p.startsWith(oldRoot)) return p
      if (skip.has(norm(p.slice(oldRoot.length)))) return p // 留在旧目录原文件
      return newRoot + p.slice(oldRoot.length)
    }
    currentFilePath.value = rewrite(currentFilePath.value)
    for (const state of modeCache.values()) state.currentFilePath = rewrite(state.currentFilePath)
  }

  function switchMode(newMode: EditorMode) {
    if (newMode === currentMode.value) return
    saveModeState(currentMode.value)
    currentMode.value = newMode
    loadModeState(newMode)
  }

  return {
    talks, dstTalks, referTalks, sourceTalks,
    currentFilePath, titleOverride, hasUnsavedChanges, majorClue, docMeta, mutationSeq,
    currentMode,
    setSourceTalks, setTalks, markUnsaved, markSaved, hasAnyUnsaved, clearAll,
    saveModeState, loadModeState, switchMode, rebindPaths, syncTitleFromPath,
  }
})
