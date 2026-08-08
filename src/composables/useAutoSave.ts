import { ref } from 'vue'
import { useEditorStore } from '../stores/editor'
import { useAppStore } from '../stores/app'
import { useStoryStore } from '../stores/story'
import { buildRecoverySaveRequest } from '../editor/recovery'
import { clearRecovery, hasPendingRecoveryClear, saveRecovery } from '../editor/recoveryCoordinator'

function normalizeBackgroundError(error: unknown): Error {
  if (error instanceof Error) return error
  if (typeof error === 'object' && error !== null) {
    const candidate = error as { name?: unknown, message?: unknown }
    if (typeof candidate.message === 'string') {
      const normalized = new Error(candidate.message)
      if (typeof candidate.name === 'string' && candidate.name) normalized.name = candidate.name
      return normalized
    }
  }
  return new Error(String(error))
}

export async function syncRecoveryNow(
  beforeCapture?: () => void | Promise<void>,
  canCapture: () => boolean = () => true,
): Promise<void> {
  if (!canCapture()) return
  await beforeCapture?.()
  if (!canCapture()) return
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
  onBackgroundError?: (error: Error) => void,
) {
  const editor = useEditorStore()
  const lastSaved = ref(Date.now())
  const lastError = ref<Error | null>(null)
  let timer: ReturnType<typeof setInterval> | null = null
  let changeTimer: ReturnType<typeof setTimeout> | null = null
  let changePending = false
  let active = false
  let intervalSync: Promise<void> | null = null
  let syncRequested = false
  const syncNow = (capture: typeof beforeCapture = beforeCapture) => syncRecoveryNow(capture)
  const syncScheduled = () => syncRecoveryNow(beforeCapture, () => !editor.documentBusy)
  const hasRecoveryWork = () => editor.hasAnyUnsaved() || hasPendingRecoveryClear()
  const needsSync = () => !editor.documentBusy && hasRecoveryWork()

  function runScheduledSync(): boolean {
    if (!needsSync()) return false
    if (intervalSync) {
      syncRequested = true
      return true
    }
    intervalSync = syncScheduled()
      .then(() => {
        lastSaved.value = Date.now()
        lastError.value = null
      })
      .catch((error: unknown) => {
        const normalized = normalizeBackgroundError(error)
        const changed = lastError.value?.name !== normalized.name
          || lastError.value?.message !== normalized.message
        lastError.value = normalized
        if (changed) onBackgroundError?.(normalized)
      })
      .finally(() => {
        intervalSync = null
        if (syncRequested && active) {
          syncRequested = false
          queueMicrotask(runScheduledSync)
        } else {
          syncRequested = false
        }
      })
    return true
  }

  function start() {
    if (active) return
    active = true
    timer = setInterval(runScheduledSync, intervalMs)
  }

  // Android has no independent backend recovery file, so waiting for the first
  // 30-second interval leaves a fresh edit vulnerable to activity/process death.
  // Save the first mutation immediately, then coalesce a burst into one trailing
  // snapshot. Desktop callers need not use this and retain the periodic policy.
  function schedule(delayMs = 1_000) {
    if (!active) return
    if (changeTimer === null) {
      // A document transaction may mark the first Android mutation dirty before
      // its finally block releases documentBusy. Preserve that missed immediate
      // attempt as trailing work instead of silently waiting for the 30s interval.
      const started = runScheduledSync()
      changePending = !started && hasRecoveryWork()
    } else {
      changePending = true
      clearTimeout(changeTimer)
    }

    const flushTrailing = () => {
      changeTimer = null
      if (!active || !changePending) return
      if (editor.documentBusy) {
        // Keep ownership of the pending first snapshot until the transaction is
        // complete. A short bounded retry avoids both a busy spin and a 30s gap.
        changeTimer = setTimeout(flushTrailing, Math.min(delayMs, 250))
        return
      }
      changePending = false
      if (!runScheduledSync() && hasRecoveryWork()) {
        changePending = true
        changeTimer = setTimeout(flushTrailing, Math.min(delayMs, 250))
      }
    }
    changeTimer = setTimeout(flushTrailing, delayMs)
  }

  function stop() {
    active = false
    syncRequested = false
    if (timer) {
      clearInterval(timer)
      timer = null
    }
    if (changeTimer) {
      clearTimeout(changeTimer)
      changeTimer = null
    }
    changePending = false
  }

  async function stopAndSync(capture: typeof beforeCapture = beforeCapture): Promise<void> {
    stop()
    await intervalSync?.catch(() => {})
    if (!needsSync()) return
    await syncNow(capture)
    lastSaved.value = Date.now()
    lastError.value = null
  }

  return {
    lastError,
    lastSaved,
    schedule,
    start,
    stop,
    stopAndSync,
    syncNow,
  }
}
