import { ref, computed } from 'vue'
import { useSettingsStore } from '../stores/settings'
import type { DstTalk } from '../types/translation'

interface Snapshot {
  talks: DstTalk[]
  dstTalks: DstTalk[]
}

interface ModeStacks {
  undo: Snapshot[]
  redo: Snapshot[]
}

// Module-level state: maintain separate undo/redo stacks per mode so switching
// between 翻译 / 校对 / 合意 retains each mode's editing history. Plain arrays
// avoid Vue wrapping thousands of snapshot properties in reactive Proxies.
const modeStacks = new Map<number, ModeStacks>()
let currentModeKey = 0

function getModeStacks(mode: number): ModeStacks {
  let s = modeStacks.get(mode)
  if (!s) {
    s = { undo: [], redo: [] }
    modeStacks.set(mode, s)
  }
  return s
}

const undoCount = ref(0)
const redoCount = ref(0)

function syncCounts() {
  const s = getModeStacks(currentModeKey)
  undoCount.value = s.undo.length
  redoCount.value = s.redo.length
}

export function clearUndoHistory() {
  modeStacks.clear()
  syncCounts()
}

export function useUndo() {
  const settings = useSettingsStore()
  const maxDepth = computed(() => settings.settings.undoDepth ?? 20)

  function switchMode(mode: number) {
    currentModeKey = mode
    syncCounts()
  }

  function pushSnapshot(talks: DstTalk[], dstTalks: DstTalk[]) {
    const s = getModeStacks(currentModeKey)
    s.undo.push({
      talks: JSON.parse(JSON.stringify(talks)),
      dstTalks: JSON.parse(JSON.stringify(dstTalks)),
    })
    while (s.undo.length > maxDepth.value) {
      s.undo.shift()
    }
    // New action invalidates redo history for the active mode
    s.redo = []
    syncCounts()
  }

  function undo(currentTalks: DstTalk[], currentDstTalks: DstTalk[]): Snapshot | null {
    const s = getModeStacks(currentModeKey)
    if (s.undo.length === 0) return null
    s.redo.push({
      talks: JSON.parse(JSON.stringify(currentTalks)),
      dstTalks: JSON.parse(JSON.stringify(currentDstTalks)),
    })
    const snap = s.undo.pop()!
    syncCounts()
    return snap
  }

  function redo(currentTalks: DstTalk[], currentDstTalks: DstTalk[]): Snapshot | null {
    const s = getModeStacks(currentModeKey)
    if (s.redo.length === 0) return null
    s.undo.push({
      talks: JSON.parse(JSON.stringify(currentTalks)),
      dstTalks: JSON.parse(JSON.stringify(currentDstTalks)),
    })
    const snap = s.redo.pop()!
    syncCounts()
    return snap
  }

  function clear() {
    clearUndoHistory()
  }

  const canUndo = computed(() => undoCount.value > 0)
  const canRedo = computed(() => redoCount.value > 0)

  return { pushSnapshot, undo, redo, clear, switchMode, canUndo, canRedo }
}
