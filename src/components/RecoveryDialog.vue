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
      editor.setTalks(talks, talks, [])
      editor.markUnsaved()
      if (result.filePath) editor.currentFilePath = result.filePath
      if (result.editorMode != null) app.setEditorMode(result.editorMode as 0 | 1 | 2)

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
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
    <div class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-xl shadow-xl w-96 max-w-[90vw] p-6">
      <div class="flex items-center gap-3 mb-4">
        <div class="w-10 h-10 rounded-full bg-yellow-500/10 flex items-center justify-center">
          <AlertTriangle class="text-yellow-500" :size="20" />
        </div>
        <div>
          <h3 class="font-semibold text-sm text-[var(--color-text)]">恢复未保存的更改</h3>
          <p class="text-xs text-[var(--color-text-secondary)] mt-0.5">
            检测到上次编辑的自动保存内容，可能由于程序意外退出导致。
          </p>
        </div>
      </div>

      <div class="flex justify-end gap-2 mt-6">
        <button
          @click="handleDiscard"
          :disabled="loading"
          class="px-4 py-2 text-sm rounded-lg border border-[var(--color-border)] text-[var(--color-text-secondary)] hover:text-[var(--color-primary)] transition-colors disabled:opacity-40"
        >
          丢弃
        </button>
        <button
          @click="handleRestore"
          :disabled="loading"
          class="px-4 py-2 text-sm rounded-lg bg-[var(--color-primary)] text-white hover:opacity-90 transition-opacity disabled:opacity-40"
        >
          {{ loading ? '恢复中...' : '恢复' }}
        </button>
      </div>
    </div>
  </div>
</template>
