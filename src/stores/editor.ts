import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { DstTalk, SourceTalk, EditorMode } from '../types/translation'

interface ModeState {
  talks: DstTalk[]
  dstTalks: DstTalk[]
  referTalks: DstTalk[]
  currentFilePath: string
  hasUnsavedChanges: boolean
  majorClue: string | null
}

function emptyModeState(): ModeState {
  return {
    talks: [],
    dstTalks: [],
    referTalks: [],
    currentFilePath: '',
    hasUnsavedChanges: false,
    majorClue: null,
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
  const titleOverride = ref('')
  const hasUnsavedChanges = ref(false)
  const majorClue = ref<string | null>(null)

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
    modeCache.clear()
  }

  function saveModeState(mode: EditorMode) {
    modeCache.set(mode, {
      talks: cloneTalks(talks.value),
      dstTalks: cloneTalks(dstTalks.value),
      referTalks: cloneTalks(referTalks.value),
      currentFilePath: currentFilePath.value,
      hasUnsavedChanges: hasUnsavedChanges.value,
      majorClue: majorClue.value,
    })
  }

  function loadModeState(mode: EditorMode) {
    const state = modeCache.get(mode) || emptyModeState()
    talks.value = cloneTalks(state.talks)
    dstTalks.value = cloneTalks(state.dstTalks)
    referTalks.value = cloneTalks(state.referTalks)
    currentFilePath.value = state.currentFilePath
    hasUnsavedChanges.value = state.hasUnsavedChanges
    majorClue.value = state.majorClue
  }

  // Switch between editor modes. Each mode owns a fully independent, deep-copied
  // state — editing one mode must never mutate another. There is no cross-mode
  // seeding: deriving a proofread/agreement baseline from a translation is an
  // explicit load-time action (see EditorPage.handleOpen), not a side effect of
  // switching tabs.
  // 保存根目录迁移后，把所有模式里已绑定的文档路径从旧根改写到新根——
  // 否则 autosave 会把译文写回旧位置，把刚迁走的目录又建回来。
  function rebindPaths(oldRoot: string, newRoot: string) {
    if (!oldRoot || oldRoot === newRoot) return
    const rewrite = (p: string) => p && p.startsWith(oldRoot) ? newRoot + p.slice(oldRoot.length) : p
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
    currentFilePath, titleOverride, hasUnsavedChanges, majorClue, mutationSeq,
    currentMode,
    setSourceTalks, setTalks, markUnsaved, markSaved, hasAnyUnsaved, clearAll,
    saveModeState, loadModeState, switchMode, rebindPaths,
  }
})
