<script setup lang="ts">
import { ref } from 'vue'
import { api } from '../api/client'
import { useEditorStore } from '../stores/editor'
import { useAppStore } from '../stores/app'
import { useStoryStore } from '../stores/story'
import { useToast } from '../composables/useToast'
import { AlertTriangle } from 'lucide-vue-next'
import { clearUndoHistory } from '../composables/useUndo'
import type { RecoveryLoadResult } from '../editor/recovery'
import {
  prepareRecoveryDocumentCandidate,
  RecoveryCandidateError,
} from '../editor/recoveryCandidate'
import { clearRecovery } from '../editor/recoveryCoordinator'

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
  if (editor.documentBusy) return
  const operation = editor.beginDocumentOperation(false)
  if (operation === null) return
  loading.value = true
  try {
    const result = await api.recoveryLoad() as RecoveryLoadResult
    const candidate = await prepareRecoveryDocumentCandidate(result, {
      loadTranslationContent: content => api.translationLoadContent(content),
      loadStory: request => api.storyLoad(request),
      checkLines: data => api.checkLines(data),
      compareText: data => api.compareText(data),
      loadSorts: type => api.storySorts(type),
      loadIndices: (type, sort) => api.storyIndex(type, sort),
      loadChapters: (type, sort, index) => api.storyChapter(type, sort, index),
      isCurrent: () => editor.isCurrentDocumentOperation(operation),
    })
    if (!editor.isCurrentDocumentOperation(operation)) return

    // Clearing the consumed recovery snapshot is the final asynchronous
    // prerequisite. If it fails, the current live document, story, undo stack,
    // and recovery file all remain untouched.
    await clearRecovery()
    if (!editor.advanceDocumentOperation(operation)) return

    const nav = candidate.navigator
    story.selectedSource = nav.meta.source
    story.selectedType = nav.meta.type
    story.selectedSort = nav.meta.sort
    story.sorts = nav.sorts
    story.indices = nav.indices
    story.selectedIndex = nav.meta.index
    story.chapters = nav.chapters
    story.selectedIndexLabel = nav.meta.indexLabel
    story.selectedChapter = nav.meta.chapter

    editor.restoreModeStates(candidate.states, candidate.activeMode)
    app.setEditorMode(candidate.activeMode)
    story.applyStory({
      ...candidate.activeStory,
      indexLabel: nav.meta.indexLabel,
      sourceTalks: JSON.parse(JSON.stringify(candidate.activeStory.sourceTalks)),
    })
    clearUndoHistory()
    emit('restore')
  } catch (e: any) {
    if (e instanceof RecoveryCandidateError && e.code === 'stale') return
    console.error('Recovery failed:', e)
    toast.show('恢复失败: ' + (e?.message || '未知错误'), 'error')
  } finally {
    loading.value = false
    editor.finishDocumentOperation(operation)
  }
}

async function handleDiscard() {
  loading.value = true
  try {
    await clearRecovery()
    emit('discard')
  } catch (e: any) {
    console.error('Recovery discard failed:', e)
    toast.show('丢弃恢复内容失败: ' + (e?.message || '未知错误'), 'error')
  } finally {
    loading.value = false
  }
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
