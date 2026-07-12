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

      // 恢复坐标优先取文档身份快照（editor.docMeta，载入时绑定，5.7.6 起随
      // modeCache 存取）。story.selected* 是全局的：下载页会直改它且离开不还原，
      // 若这里读实时选择，崩溃恢复会按错剧情去 CheckLines 重排译文并自动写回原
      // 文件。docMeta 为 null（如本地导入）才回退现有 story.selected* 行为。
      const meta = editor.docMeta
      const coord = meta
        ? { type: meta.type, sort: meta.sort, index: meta.index, chapter: meta.chapter, source: meta.source }
        : { type: story.selectedType, sort: story.selectedSort, index: story.selectedIndex, chapter: story.selectedChapter, source: story.selectedSource }

      // Always save recovery file with story context
      try {
        await api.recoverySave({
          talks: editor.dstTalks,
          saveN: app.saveN,
          filePath: editor.currentFilePath,
          editorMode: app.editorMode,
          storyType: coord.type || undefined,
          storySort: coord.sort || undefined,
          storyIndex: coord.index || undefined,
          storyChapter: coord.chapter >= 0 ? coord.chapter : undefined,
          storySource: coord.source || undefined,
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
