import { ref, computed } from 'vue'
import { useSettingsStore } from '../stores/settings'
import type { DstTalk } from '../types/translation'

interface Snapshot {
  talks: DstTalk[]
  dstTalks: DstTalk[]
}

// Module-level singleton
const undoStack = ref<Snapshot[]>([])
const redoStack = ref<Snapshot[]>([])

export function useUndo() {
  const settings = useSettingsStore()
  const maxDepth = computed(() => settings.settings.undoDepth ?? 20)

  function pushSnapshot(talks: DstTalk[], dstTalks: DstTalk[]) {
    undoStack.value.push({
      talks: JSON.parse(JSON.stringify(talks)),
      dstTalks: JSON.parse(JSON.stringify(dstTalks)),
    })
    while (undoStack.value.length > maxDepth.value) {
      undoStack.value.shift()
    }
    // New action invalidates redo history
    redoStack.value = []
  }

  function undo(currentTalks: DstTalk[], currentDstTalks: DstTalk[]): Snapshot | null {
    if (undoStack.value.length === 0) return null
    // Save current state to redo stack before undoing
    redoStack.value.push({
      talks: JSON.parse(JSON.stringify(currentTalks)),
      dstTalks: JSON.parse(JSON.stringify(currentDstTalks)),
    })
    const snap = undoStack.value.pop()!
    return JSON.parse(JSON.stringify(snap))
  }

  function redo(currentTalks: DstTalk[], currentDstTalks: DstTalk[]): Snapshot | null {
    if (redoStack.value.length === 0) return null
    // Save current state to undo stack before redoing
    undoStack.value.push({
      talks: JSON.parse(JSON.stringify(currentTalks)),
      dstTalks: JSON.parse(JSON.stringify(currentDstTalks)),
    })
    const snap = redoStack.value.pop()!
    return JSON.parse(JSON.stringify(snap))
  }

  function clear() {
    undoStack.value = []
    redoStack.value = []
  }

  const canUndo = computed(() => undoStack.value.length > 0)
  const canRedo = computed(() => redoStack.value.length > 0)

  return { pushSnapshot, undo, redo, clear, canUndo, canRedo }
}
