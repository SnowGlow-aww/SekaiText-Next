import { ref } from 'vue'
import { useEditorStore } from '../stores/editor'
import { useAppStore } from '../stores/app'
import { useStoryStore } from '../stores/story'
import { api } from '../api/client'

/**
 * Periodically saves editor state to a recovery file (autosave).
 * Never writes the real project file — that only happens on explicit save.
 */
export function useAutoSave(intervalMs = 30000) {
  const editor = useEditorStore()
  const app = useAppStore()
  const story = useStoryStore()
  const lastSaved = ref(Date.now())
  let timer: ReturnType<typeof setInterval> | null = null

  function start() {
    if (timer) return
    timer = setInterval(async () => {
      if (!editor.hasUnsavedChanges || editor.talks.length === 0) return

      // Always save recovery file with story context
      try {
        await api.recoverySave({
          talks: editor.dstTalks,
          saveN: app.saveN,
          filePath: editor.currentFilePath,
          editorMode: app.editorMode,
          storyType: story.selectedType || undefined,
          storySort: story.selectedSort || undefined,
          storyIndex: story.selectedIndex || undefined,
          storyChapter: story.selectedChapter >= 0 ? story.selectedChapter : undefined,
          storySource: story.selectedSource || undefined,
        })
      } catch {
        // Silent fail on recovery save
      }
    }, intervalMs)
  }

  function stop() {
    if (timer) {
      clearInterval(timer)
      timer = null
    }
  }

  return {
    lastSaved,
    start,
    stop,
  }
}
