<script setup lang="ts">
import { nextTick, ref } from 'vue'
import { api } from '../api/client'
import { useEditorStore } from '../stores/editor'
import { useAppStore } from '../stores/app'
import { useStoryStore } from '../stores/story'
import { useToast } from '../composables/useToast'
import { AlertTriangle } from 'lucide-vue-next'
import { clearUndoHistory } from '../composables/useUndo'
import { hasRecovery, recoveryModes } from '../editor/recovery'
import type { RecoveryLoadResult } from '../editor/recovery'
import type { EditorModeState } from '../stores/editor'
import type { DocMeta } from '../stores/editor'
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

async function restoreNavigatorCoordinates(meta: DocMeta | null) {
  if (!meta?.type) return
  let failure: unknown
  story.selectedSource = meta.source || 'haruki'
  story.selectedType = meta.type
  try { await story.fetchSorts(meta.type) } catch (e) { failure = e }
  await nextTick()
  story.selectedSort = meta.sort || ''
  try { await story.fetchIndex(meta.type, meta.sort || '') } catch (e) { failure ??= e }
  await nextTick()
  story.selectedIndex = meta.index
  story.selectedIndexLabel = meta.indexLabel || meta.index
  try { await story.fetchChapters(meta.type, meta.sort || '', meta.index) } catch (e) { failure ??= e }
  await nextTick()
  story.selectedChapter = meta.chapter
  if (failure) throw failure
}

async function handleRestore() {
  loading.value = true
  let restored = false
  try {
    const result = await api.recoveryLoad() as RecoveryLoadResult
    if (!hasRecovery(result)) throw new Error('恢复内容已不存在')
    if (hasRecovery(result)) {
      if (result.modes?.length) {
        const states: EditorModeState[] = []
        for (const mode of recoveryModes(result)) {
          const hasLosslessState = mode.talks !== undefined
          const talks = mode.talks !== undefined
            ? JSON.parse(JSON.stringify(mode.talks)) as NonNullable<typeof mode.talks>
            : (await api.translationLoadContent(mode.content)).talks
          if (!hasLosslessState && mode.editorMode >= 1) {
            for (const talk of talks) {
              if (talk.baseline === undefined || talk.baseline === '') talk.baseline = talk.text
            }
          }
          const dstTalks = mode.dstTalks
            ? JSON.parse(JSON.stringify(mode.dstTalks)) as typeof mode.dstTalks
            : JSON.parse(JSON.stringify(talks)) as typeof talks
          const referTalks = mode.referTalks
            ? JSON.parse(JSON.stringify(mode.referTalks)) as typeof mode.referTalks
            : []
          states.push({
            mode: mode.editorMode,
            talks,
            dstTalks,
            referTalks,
            sourceTalks: mode.sourceTalks || [],
            currentFilePath: mode.filePath,
            titleOverride: mode.titleOverride || titleFromPath(mode.filePath),
            hasUnsavedChanges: mode.hasUnsavedChanges ?? true,
            recoveryPending: true,
            majorClue: null,
            docMeta: mode.docMeta || null,
            mutationSeq: 0,
          })
        }
        const activeMode = result.activeMode ?? states[0]?.mode ?? 0
        const activeState = states.find(state => state.mode === activeMode) ?? states[0]
        try {
          await restoreNavigatorCoordinates(activeState?.docMeta ?? null)
        } catch (e: any) {
          console.error('Recovery navigator restore failed:', e)
          toast.show('剧情列表恢复失败，已保留恢复坐标', 'warn')
        }
        editor.restoreModeStates(states, activeMode)
        app.setEditorMode(editor.currentMode)
        story.sourceTalks = JSON.parse(JSON.stringify(editor.sourceTalks))
        story.scenarioId = editor.docMeta?.scenarioId || ''
        story.saveTitle = editor.docMeta?.saveTitle || ''
        story.chapterTitle = editor.docMeta?.chapterTitle || ''
        if (editor.docMeta?.source) story.selectedSource = editor.docMeta.source
        clearUndoHistory()
        restored = true
      } else {
        const { talks } = await api.translationLoadContent(result.content || '')
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
        editor.setSourceTalks([])
        editor.markRecovered()
        if (result.filePath) {
          editor.currentFilePath = result.filePath
          // 回同步文件名里的标题段：titleOverride 留空会让恢复后的首次保存
          // 按日文原标题重算规范名，把已翻译好的文件名改回去。
          editor.syncTitleFromPath(result.filePath)
        }

        // Restore story context and re-load source text
        if (result.storyType) {
          const rType = result.storyType
          const rSort = result.storySort || ''
          const rIndex = result.storyIndex || ''
          const rChapter = result.storyChapter ?? -1
          try {
            await restoreNavigatorCoordinates({
              saveTitle: '', chapterTitle: '', type: rType, sort: rSort,
              index: rIndex, indexLabel: rIndex, chapter: rChapter,
              source: result.storySource || 'haruki', scenarioId: '',
            })
            await story.loadStory()
            // 恢复的文档同样绑定身份快照，保存命名不再受之后的全局选择影响。
            editor.docMeta = story.snapshotDocMeta()
            editor.setSourceTalks(story.sourceTalks)
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
        clearUndoHistory()
        restored = true
      }
    }
  } catch (e: any) {
    console.error('Recovery failed:', e)
    toast.show('恢复失败: ' + (e?.message || '未知错误'), 'error')
  } finally {
    loading.value = false
  }
  if (restored) emit('restore')
}

function titleFromPath(path: string): string {
  const base = path.split(/[/\\]/).pop() || ''
  const stripped = base.replace(/\.txt$/i, '').replace(/^【[^】]*】/, '').trim()
  const label = stripped.split(/\s+/)[0] || ''
  return stripped.slice(label.length).trim()
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
