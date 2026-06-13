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
  return arr.map(t => ({ ...t }))
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

  function markUnsaved() {
    hasUnsavedChanges.value = true
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
  function switchMode(newMode: EditorMode) {
    if (newMode === currentMode.value) return
    saveModeState(currentMode.value)
    currentMode.value = newMode
    loadModeState(newMode)
  }

  return {
    talks, dstTalks, referTalks, sourceTalks,
    currentFilePath, titleOverride, hasUnsavedChanges, majorClue,
    currentMode,
    setSourceTalks, setTalks, markUnsaved, markSaved, clearAll,
    saveModeState, loadModeState, switchMode,
  }
})
