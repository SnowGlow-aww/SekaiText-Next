import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api/client'
import type { MigrateSaveDirResult, Settings } from '../types/api'
import { saveDirectoryCoordinator } from '../editor/saveDirectoryCoordinator'

function cloneSettings(value: Settings): Settings {
  return {
    ...value,
    shortcuts: value.shortcuts ? { ...value.shortcuts } : undefined,
    seenTours: value.seenTours ? [...value.seenTours] : undefined,
  }
}

function sameValue(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}

// Apply only fields actually edited relative to `base`. Runtime-maintained
// fields (seenTours, lastSeenVersion, story coordinates, etc.) therefore stay at
// their latest values when an older settings-page draft is eventually saved.
export function rebaseSettings(base: Settings, edited: Settings, latest: Settings): Settings {
  const rebased = cloneSettings(latest)
  const keys = new Set<keyof Settings>([
    ...Object.keys(base) as (keyof Settings)[],
    ...Object.keys(edited) as (keyof Settings)[],
  ])
  for (const key of keys) {
    if (!sameValue(base[key], edited[key])) {
      ;(rebased as Record<keyof Settings, unknown>)[key] = cloneSettings({
        ...latest,
        [key]: edited[key],
      })[key]
    }
  }
  return rebased
}

export const useSettingsStore = defineStore('settings', () => {
  const settings = ref<Settings>({
    fontSize: 18,
    uiFontSize: 16,
    saveN: true,
    debugEnabled: false,
    // Left empty on purpose: a hardcoded default would be developer-specific
    // and invalid on the user's machine (and on Windows). Resolved per-user at
    // runtime — the JSON download handler falls back to {DataDir}/json and the
    // save flow falls back to the OS save-dialog default location when empty.
    jsonDownloadDir: '',
    saveBaseDir: '',
    undoDepth: 20,
    keepHighlightWhenCompareOff: true,

    indexOrder: 'asc',
    shortcuts: {},
    hideAgreementImportHint: false,
    live2dPosition: 'window',
  })
  const loading = ref(false)
  let lastPersisted = cloneSettings(settings.value)
  let saveTail: Promise<void> = Promise.resolve()

  async function fetchSettings() {
    loading.value = true
    try {
      const s = await api.getSettings()
      // Migrate configs saved before uiFontSize existed (absent → 0): keep the
      // browser-default 16px so the UI doesn't collapse to a 0px root font.
      if (!s.uiFontSize) s.uiFontSize = 16
      // Default the Live2D dock to a standalone window for pre-existing configs.
      if (!s.live2dPosition) s.live2dPosition = 'window'
      // The 右侧 (right) placement option was retired; migrate any saved 'right'
      // to the standalone window so the removed dropdown option can't strand the
      // layout (or render a blank select for that now-unknown value).
      if (s.live2dPosition === 'right') s.live2dPosition = 'window'
      settings.value = cloneSettings(s)
      lastPersisted = cloneSettings(s)
    } finally {
      loading.value = false
    }
  }

  function createDraft() {
    return cloneSettings(settings.value)
  }

  function saveSettings(next: Settings = settings.value, base: Settings = lastPersisted): Promise<Settings> {
    const edited = cloneSettings(next)
    const baseline = cloneSettings(base)
    const run = async () => {
      const payload = rebaseSettings(baseline, edited, settings.value)
      const liveAtRequest = cloneSettings(settings.value)
      let saved = await api.putSettings(payload)
      const liveAfterRequest = cloneSettings(settings.value)
      let committed = rebaseSettings(liveAtRequest, liveAfterRequest, saved)
      // migrateSaveDir persists saveBaseDir independently. If that (or another
      // runtime mutation) completes while this older PUT is in flight, the PUT
      // may restore stale settings on disk even though reactive state is rebased.
      // Persist the rebased result once more before reporting Save complete.
      if (!sameValue(committed, saved)) {
        const liveBeforeFollowup = liveAfterRequest
        saved = await api.putSettings(committed)
        committed = rebaseSettings(liveBeforeFollowup, cloneSettings(settings.value), saved)
      }
      lastPersisted = cloneSettings(saved)
      // Preserve direct runtime mutations made while PUT was in flight. A later
      // queued save will persist them, but the completed response must not erase
      // them from reactive state in the meantime.
      settings.value = committed
      return settings.value
    }
    const result = saveTail.then(run, run)
    saveTail = result.then(() => undefined, () => undefined)
    return result
  }

  function migrateSaveDir(
    newDir: string,
    afterCommit?: (result: MigrateSaveDirResult) => void,
  ): Promise<MigrateSaveDirResult> {
    const run = () => saveDirectoryCoordinator.run(async () => {
      const result = await api.migrateSaveDir(newDir)
      // Publish the new root and rebind every open document before releasing the
      // shared file-transaction queue. A queued autosave therefore recomputes its
      // target under the new root instead of writing a stale old-root path.
      settings.value.saveBaseDir = result.newDir
      lastPersisted.saveBaseDir = result.newDir
      afterCommit?.(result)
      return result
    })
    const result = saveTail.then(run, run)
    saveTail = result.then(() => undefined, () => undefined)
    return result
  }

  return { settings, loading, fetchSettings, createDraft, saveSettings, migrateSaveDir }
})
