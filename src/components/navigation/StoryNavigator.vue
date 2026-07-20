<script lang="ts">
// App-lifetime guard (module scope): auto-pull metadata once per app launch.
// Declared in a plain <script> block so it lives at module scope and is shared
// across every component instance. If it were in <script setup>, Vue would run
// that body inside each instance's setup(), re-initializing it to false on every
// mount and defeating the "once per app launch" intent.
let autoPulledOnce = false
</script>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onActivated, onDeactivated } from 'vue'
import { RefreshCw, Download, FolderOpen } from 'lucide-vue-next'
import { useStoryStore } from '../../stores/story'
import { useEditorStore } from '../../stores/editor'
import { useSettingsStore } from '../../stores/settings'
import { api } from '../../api/client'
import { useToast } from '../../composables/useToast'
import { useConfirm } from '../../composables/useConfirm'
import { useDebugLog } from '../../composables/useDebugLog'
import { useDownloadFloat } from '../../composables/useDownloadFloat'
import { clearUndoHistory } from '../../composables/useUndo'
import SkSelect from '../ui/SkSelect.vue'
import { syncRecoveryNow } from '../../composables/useAutoSave'

const sourceOptions = [
  { value: 'haruki', label: 'HarukiBot NEO' },
  { value: 'moesekai-jp', label: 'Moesekai (JP)' },
  { value: 'moesekai-cn', label: 'Moesekai (CN)' },
]

// Only the editor opts into auto-pulling catalog metadata on mount. The Live2D
// player reuses this navigator but doesn't need a fresh catalog pull, so it
// leaves autoPull at its default (false) and just uses whatever is cached.
const props = withDefaults(defineProps<{ autoPull?: boolean }>(), { autoPull: false })

const story = useStoryStore()
const editor = useEditorStore()
const settingsStore = useSettingsStore()

const displayIndices = computed(() => {
  if (settingsStore.settings.indexOrder === 'desc') {
    return [...story.indices].reverse()
  }
  return story.indices
})
const refreshing = ref(false)
const toast = useToast()
const { confirm } = useConfirm()

// Loading a story replaces the whole editor document with a fresh template.
// With unsaved edits that is unrecoverable data loss (the next 30s autosave
// tick then overwrites the recovery file with the empty template as well), so
// it must never happen on a stray click without confirmation.
async function confirmDiscardUnsaved(): Promise<boolean> {
  if (!editor.hasAnyUnsaved()) return true
  return confirm({
    title: '载入故事',
    message: '有未保存的更改，载入将用新模板覆盖当前译文。确定继续吗？',
    tone: 'danger',
    confirmText: '不保存并载入',
  })
}
const debug = useDebugLog()
const dlFloat = useDownloadFloat()
const navigatorActive = ref(true)
onActivated(() => { navigatorActive.value = true })
onDeactivated(() => { navigatorActive.value = false })

const unitMap = ref<Record<string, string>>({})
async function loadUnitDict() {
  try {
    const res = await fetch('/unitDict.json')
    unitMap.value = await res.json()
  } catch { /* fallback to raw IDs */ }
}
function unitName(key: string): string {
  return unitMap.value[key] || key
}

debug.log('StoryNavigator mounted, fetching types...')
onMounted(() => {
  loadUnitDict()
  const t0 = performance.now()
  const doFetch = async () => {
    for (let i = 0; i < 5; i++) {
      try {
        await story.fetchTypes()
        if (story.storyTypes.length > 0) {
          debug.log(`故事类型已加载 (${story.storyTypes.length}个) 耗时:${Math.round(performance.now()-t0)}ms`)
          return
        }
      } catch (e: any) {
        debug.log(`获取类型失败尝试 ${i+1}: ${e.message}`, 'warn')
      }
      await new Promise(r => setTimeout(r, 1000))
    }
    debug.log('无法加载故事类型', 'error')
  }
  doFetch()
  // Auto-pull metadata once per app launch so the catalog is fresh on startup —
  // but only when the host page opts in (editor). The Live2D player skips this.
  if (props.autoPull && !autoPulledOnce) {
    autoPulledOnce = true
    handleRefresh()
  }
})



watch(() => story.selectedType, async (type) => {
  if (!navigatorActive.value) return
  debug.log(`selectedType changed: "${type}"`)
  if (!type) return
  await story.fetchSorts(type)
  if (navigatorActive.value && story.selectedType === type && story.sorts.length === 0) {
    await story.fetchIndex(type, '')
  }
})

watch(() => story.selectedSort, (sort) => {
  if (!navigatorActive.value) return
  debug.log(`selectedSort changed: "${sort}"`)
  if (!sort || !story.selectedType) return
  void story.fetchIndex(story.selectedType, sort)
})

watch(() => story.selectedIndex, (idx) => {
  if (!navigatorActive.value) return
  debug.log(`selectedIndex changed: "${idx}"`)
  if (!idx || !story.selectedType) return
  if (idx !== '-') {
    void story.fetchChapters(story.selectedType, story.selectedSort, idx)
  }
})

async function handleRefresh() {
  refreshing.value = true
  const taskId = dlFloat.add('拉取元数据')
  dlFloat.start(taskId)
  try {
    await api.update()
    let done = false
    while (!done) {
      await new Promise(r => setTimeout(r, 500))
      const progress = await api.updateProgress()
      if (progress.total > 0) {
        dlFloat.progress(taskId, progress.current, progress.total, Math.round((progress.current / progress.total) * 100))
      }
      done = progress.done
    }
    dlFloat.done(taskId, '元数据已拉取')
  } catch (e: any) {
    dlFloat.fail(taskId, e.message || '拉取失败')
  } finally {
    refreshing.value = false
  }
}

async function openSaveDir() {
  try {
    await api.openSaveDir()
  } catch (e: any) {
    toast.show('打开失败: ' + (e.message || '未知错误'), 'error')
  }
}

async function handleLoad() {
  debug.log(`载入按钮点击 loading=${story.loading} selectedType="${story.selectedType}"`)
  if (editor.documentBusy) return
  if (!(await confirmDiscardUnsaved())) return
  const operation = editor.beginDocumentOperation()
  if (operation === null) return
  const taskId = dlFloat.add('载入故事')
  dlFloat.start(taskId)
  story.loading = true
  try {
    // Fetch both halves before committing either. If template creation fails,
    // the previous source, translation, file binding and metadata stay intact.
    const loadedStory = await story.fetchStory()
    const dstTalks = loadedStory.sourceTalks.length > 0
      ? await api.translationCreate({
        sourceTalks: loadedStory.sourceTalks,
        jp: false,
      })
      : []
    if (!editor.isCurrentDocumentOperation(operation)) return

    if (loadedStory.sourceTalks.length > 0) {
      debug.log(`故事载入成功 ${loadedStory.sourceTalks.length}行`)
      story.applyStory(loadedStory)
      editor.clearAll()
      editor.setSourceTalks(loadedStory.sourceTalks)
      editor.setTalks(dstTalks, dstTalks, [])
      editor.majorClue = null
      // 新文档会话：命名/元数据快照跟内容走（此后别处再拉别的剧情不影响本文档），
      // 并解除上一个文档的路径绑定与标题覆盖——否则新剧情的译文会继续写进上一个
      // 剧情已绑定的文件（用户反馈：后篇内容被存进前篇的文件，改文件名也拦不住，
      // 因为下次保存按旧绑定路径重建）。
      editor.docMeta = story.snapshotDocMeta()
      editor.currentFilePath = ''
      editor.titleOverride = ''
      // Fresh template = clean state; keeping the OLD document's dirty flag
      // would let the 30s autosave overwrite the recovery file with this
      // near-empty template.
      editor.markSaved()
      await syncRecoveryNow().catch(() => {})
      clearUndoHistory()
      dlFloat.done(taskId, `已载入 ${loadedStory.sourceTalks.length} 行`)
    } else {
      debug.log('故事载入返回0行', 'warn')
      dlFloat.done(taskId, '载入完成（0行）')
    }
  } catch (e: any) {
    debug.log('载入失败: ' + e.message, 'error')
    dlFloat.fail(taskId, e.message || '载入失败')
  } finally {
    story.loading = false
    editor.finishDocumentOperation(operation)
  }
}
</script>

<template>
  <div class="flex items-center gap-2 flex-wrap">
    <SkSelect
      size="sm"
      :disabled="editor.documentBusy"
      :model-value="story.selectedType"
      @update:model-value="story.selectedType = $event as string"
      :options="story.storyTypes.map(t => ({ value: t, label: unitName(t) }))"
      placeholder="故事类型"
    />

    <SkSelect
      v-if="story.sorts?.length"
      size="sm"
      :disabled="editor.documentBusy"
      :model-value="story.selectedSort"
      @update:model-value="story.selectedSort = $event as string"
      :options="story.sorts.map(s => ({ value: s.value, label: s.label }))"
      placeholder="排序"
    />

    <SkSelect
      size="sm"
      :disabled="editor.documentBusy"
      :model-value="story.selectedIndex"
      @update:model-value="story.selectedIndex = $event as string"
      :options="displayIndices.map(i => ({ value: i.value, label: i.label }))"
      placeholder="索引"
    />

    <SkSelect
      size="sm"
      :disabled="editor.documentBusy"
      :model-value="story.selectedChapter"
      @update:model-value="story.selectedChapter = $event as number"
      :options="story.chapters.map(c => ({ value: c.number, label: c.label }))"
      placeholder="章节"
    />

    <SkSelect
      size="sm"
      :disabled="editor.documentBusy"
      :model-value="story.selectedSource"
      @update:model-value="story.selectedSource = $event as string"
      :options="sourceOptions"
    />

    <button
      @click="handleRefresh"
      class="btn btn-sm btn-ghost border border-[var(--color-border)] gap-1.5"
      :disabled="refreshing || editor.documentBusy"
    >
      <RefreshCw :size="15" :class="{ 'animate-spin': refreshing }" />
      {{ refreshing ? '拉取中…' : '拉取' }}
    </button>

    <button
      @click="handleLoad"
      class="btn btn-sm btn-brand gap-1.5"
      :disabled="story.loading || editor.documentBusy || !story.selectedType || story.selectedChapter < 0"
    >
      <span v-if="story.loading" class="loading loading-spinner loading-sm" />
      <Download v-else :size="15" />
      {{ story.loading ? '载入中…' : '载入' }}
    </button>

    <!-- 打开译文保存根目录（目录不存在时后端先建再开）——用户从这里直达自己的文稿 -->
    <button
      @click="openSaveDir"
      class="btn btn-sm btn-ghost border border-[var(--color-border)] gap-1.5"
      title="在文件管理器中打开译文保存目录"
      :disabled="editor.documentBusy"
    >
      <FolderOpen :size="15" />
      文稿目录
    </button>
  </div>
</template>
