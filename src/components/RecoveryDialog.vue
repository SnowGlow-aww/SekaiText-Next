<script setup lang="ts">
import { ref } from 'vue'
import { api } from '../api/client'
import { useEditorStore } from '../stores/editor'
import { useAppStore } from '../stores/app'
import { useStoryStore } from '../stores/story'
import { useToast } from '../composables/useToast'
import { AlertTriangle } from 'lucide-vue-next'

const emit = defineEmits<{
  restore: []
  discard: []
}>()

const editor = useEditorStore()
const app = useAppStore()
const story = useStoryStore()
const toast = useToast()
const loading = ref(false)

async function handleRestore() {
  loading.value = true
  try {
    const result = await api.recoveryLoad()
    if (result.exists && result.content) {
      const { talks } = await api.translationLoadContent(result.content)
      // Baseline fallback (same as EditorPage.handleOpen): seed every row's
      // baseline to its current text in 校对/合意 modes before anything else, so
      // edits always produce a diff even when the source story fails to re-load
      // (empty index, missing catalog, etc.). Without this, recovered files show
      // no reaction when edited under compare.
      if ((result.editorMode ?? 0) >= 1) {
        for (const t of talks) if (t.baseline === undefined || t.baseline === '') t.baseline = t.text
      }
      // Keep the editor store's currentMode (which keys the per-mode modeCache)
      // in sync with app.editorMode BEFORE seeding talks. EditorPage.setMode is the
      // only other place these two are synced; recovering via app.setEditorMode
      // alone left editor.currentMode at 0, so the next mode switch saved the
      // recovered rows into the wrong cache slot and dropped them. switchMode runs
      // first so its (empty target-slot) load doesn't clobber the seeded talks.
      if (result.editorMode != null) {
        editor.switchMode(result.editorMode as 0 | 1 | 2)
        app.setEditorMode(result.editorMode as 0 | 1 | 2)
      }
      editor.setTalks(talks, talks, [])
      editor.markUnsaved()
      if (result.filePath) editor.currentFilePath = result.filePath

      // Restore story context and re-load source text
      if (result.storyType) {
        story.selectedType = result.storyType
        story.selectedSort = result.storySort || ''
        story.selectedIndex = result.storyIndex || ''
        story.selectedChapter = result.storyChapter ?? -1
        story.selectedSource = result.storySource || 'haruki'
        try {
          await story.loadStory()
          if (story.sourceTalks.length > 0) {
            const aligned = await api.checkLines({
              sourceTalks: story.sourceTalks,
              loadedTalks: talks,
            })
            // Seed baseline = current text in 校对/合意 so compare shows nothing
            // changed until edited (matches EditorPage.handleOpen).
            if (app.editorMode >= 1) {
              for (const t of aligned) t.baseline = t.text
            }
            editor.setTalks(aligned, aligned, [])
          }
        } catch (e: any) {
          // Story re-load failed; keep recovered talks as-is
          console.error('Recovery story re-load failed:', e)
          toast.show('原文恢复失败，请手动重新选择剧情', 'warn')
        }
      }
      await api.recoveryClear().catch(() => {})
    }
  } catch {
    // Recovery failed, continue without it
  } finally {
    loading.value = false
    emit('restore')
  }
}

async function handleDiscard() {
  try {
    await api.recoveryClear()
  } catch {
    // ignore
  }
  emit('discard')
}
</script>

<template>
  <div class="modal modal-open">
    <div class="modal-box w-96 max-w-[90vw]">
      <div class="flex items-center gap-3 mb-4">
        <div class="w-10 h-10 rounded-full bg-warning/10 flex items-center justify-center shrink-0">
          <AlertTriangle class="text-warning" :size="20" />
        </div>
        <div>
          <h3 class="font-semibold text-sm">恢复未保存的更改</h3>
          <p class="text-xs opacity-60 mt-0.5">
            检测到上次编辑的自动保存内容，可能由于程序意外退出导致。
          </p>
        </div>
      </div>

      <div class="modal-action">
        <button
          @click="handleDiscard"
          :disabled="loading"
          class="btn btn-ghost btn-sm"
        >
          丢弃
        </button>
        <button
          @click="handleRestore"
          :disabled="loading"
          class="btn btn-primary btn-sm"
        >
          {{ loading ? '恢复中...' : '恢复' }}
        </button>
      </div>
    </div>
  </div>
</template>
