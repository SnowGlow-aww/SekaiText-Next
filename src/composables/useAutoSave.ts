import { ref } from 'vue'
import { useEditorStore } from '../stores/editor'
import { useAppStore } from '../stores/app'
import { useStoryStore } from '../stores/story'
import { buildRecoverySaveRequest } from '../editor/recovery'
import { clearRecovery, hasPendingRecoveryClear, saveRecovery } from '../editor/recoveryCoordinator'

export async function syncRecoveryNow(beforeCapture?: () => void | Promise<void>): Promise<void> {
  await beforeCapture?.()
  const editor = useEditorStore()
  const app = useAppStore()
  const story = useStoryStore()
  const states = editor.captureModeStates()
  const active = states.find(state => state.mode === editor.currentMode)
  // Local/legacy files may not own a document snapshot. Use the navigator only
  // for that active slot; snapshots in every other mode remain isolated.
  if (active && !active.docMeta && story.selectedType) {
    active.docMeta = {
      saveTitle: story.saveTitle,
      chapterTitle: story.chapterTitle,
      type: story.selectedType,
      sort: story.selectedSort,
      index: story.selectedIndex,
      indexLabel: story.selectedIndexLabel,
      chapter: story.selectedChapter,
      source: story.selectedSource,
      scenarioId: story.scenarioId,
    }
  }
  const request = buildRecoverySaveRequest(states, editor.currentMode, app.saveN)
  if (request.modes.length === 0) await clearRecovery()
  else await saveRecovery(request)
}

/**
 * Periodically saves editor state to a recovery file (autosave).
 * Never writes the real project file — that only happens on explicit save.
 */
export function useAutoSave(
  intervalMs = 30000,
  beforeCapture?: () => void | Promise<void>,
) {
  const editor = useEditorStore()
  const lastSaved = ref(Date.now())
  let timer: ReturnType<typeof setInterval> | null = null
  let intervalSync: Promise<void> | null = null
  const syncNow = (capture: typeof beforeCapture = beforeCapture) => syncRecoveryNow(capture)

  function start() {
    if (timer) return
    timer = setInterval(() => {
      if (!editor.hasAnyUnsaved() && !hasPendingRecoveryClear()) return
      if (intervalSync) return
      intervalSync = syncNow()
        .then(() => { lastSaved.value = Date.now() })
        .catch(() => { /* silent recovery failure */ })
        .finally(() => { intervalSync = null })
    }, intervalMs)
  }

  function stop() {
    if (timer) {
      clearInterval(timer)
      timer = null
    }
  }

  function stopAndSync(capture: typeof beforeCapture = beforeCapture): Promise<void> {
    stop()
    if (!editor.hasAnyUnsaved() && !hasPendingRecoveryClear()) return Promise.resolve()
    return syncNow(capture).then(() => { lastSaved.value = Date.now() })
  }

  return {
    lastSaved,
    start,
    stop,
    stopAndSync,
    syncNow,
  }
}
