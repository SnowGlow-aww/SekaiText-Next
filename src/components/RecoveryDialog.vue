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
      if (result.filePath) {
        editor.currentFilePath = result.filePath
        // 回同步文件名里的标题段：titleOverride 留空会让恢复后的首次保存
        // 按日文原标题重算规范名，把已翻译好的文件名改回去。
        editor.syncTitleFromPath(result.filePath)
      }

      // Restore story context and re-load source text
      if (result.storyType) {
        story.selectedType = result.storyType
        story.selectedSort = result.storySort || ''
        story.selectedIndex = result.storyIndex || ''
        story.selectedChapter = result.storyChapter ?? -1
        story.selectedSource = result.storySource || 'haruki'
        try {
          await story.loadStory()
          // 恢复的文档同样绑定身份快照，保存命名不再受之后的全局选择影响。
          editor.docMeta = story.snapshotDocMeta()
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
  <Transition name="recovery-fade" appear>
    <div class="fixed inset-0 flex items-center justify-center p-4 z-[var(--z-modal)]">
      <!-- scrim -->
      <div class="absolute inset-0 bg-black/45 backdrop-blur-[2px]" />
      <!-- panel -->
      <div class="app-card app-glass relative w-full max-w-sm p-5" style="box-shadow: var(--shadow-lg)">
        <div class="flex items-start gap-3">
          <div class="grid place-items-center w-9 h-9 rounded-full shrink-0 bg-warning/15 text-warning">
            <AlertTriangle :size="18" />
          </div>
          <div class="min-w-0 flex-1">
            <h3 class="section-title mb-1">恢复未保存的更改</h3>
            <p class="text-sm text-[var(--color-text-secondary)] leading-relaxed">
              检测到上次编辑的自动保存内容，可能由于程序意外退出导致。
            </p>
          </div>
        </div>

        <div class="flex justify-end gap-2 mt-5">
          <button
            @click="handleDiscard"
            :disabled="loading"
            class="btn btn-sm btn-ghost"
          >
            丢弃
          </button>
          <button
            @click="handleRestore"
            :disabled="loading"
            class="btn btn-sm btn-brand gap-1.5"
          >
            <span v-if="loading" class="loading loading-spinner loading-sm" />
            {{ loading ? '恢复中…' : '恢复' }}
          </button>
        </div>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.recovery-fade-enter-active,
.recovery-fade-leave-active {
  transition: opacity var(--dur) var(--ease-out);
}
.recovery-fade-enter-from,
.recovery-fade-leave-to {
  opacity: 0;
}
.recovery-fade-enter-active .app-card,
.recovery-fade-leave-active .app-card {
  transition: transform var(--dur) var(--ease-out);
}
.recovery-fade-enter-from .app-card {
  transform: translateY(8px) scale(0.97);
}
</style>
