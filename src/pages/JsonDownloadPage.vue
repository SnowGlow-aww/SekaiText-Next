<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowLeft, BookOpen, Download, FolderOpen } from 'lucide-vue-next'
import { useStoryStore } from '../stores/story'
import { useSettingsStore } from '../stores/settings'
import { api } from '../api/client'
import { useToast } from '../composables/useToast'
import { useDownloadFloat } from '../composables/useDownloadFloat'
import { useFileDialog } from '../composables/useFileDialog'
import SkSelect from '../components/ui/SkSelect.vue'

const router = useRouter()
const story = useStoryStore()
const settings = useSettingsStore()
const toast = useToast()
const dlFloat = useDownloadFloat()
const { pickDirectory, isTauri } = useFileDialog()

const outputDir = ref(settings.settings.jsonDownloadDir || '')

async function browseOutputDir() {
  const dir = await pickDirectory('选择保存目录')
  if (dir) outputDir.value = dir
}

// Persist the chosen output directory so it survives an app restart. Previously
// it was read from settings on mount but never written back, so a typed path was
// lost on exit. Debounced to avoid a settings PUT on every keystroke.
let saveDirTimer: ReturnType<typeof setTimeout> | null = null
watch(outputDir, (v) => {
  settings.settings.jsonDownloadDir = v
  if (saveDirTimer) clearTimeout(saveDirTimer)
  saveDirTimer = setTimeout(() => { settings.saveSettings().catch(() => {}) }, 600)
})

const displayIndices = computed(() => {
  if (settings.settings.indexOrder === 'desc') {
    return [...story.indices].reverse()
  }
  return story.indices
})

onMounted(async () => {
  await story.fetchTypes()
})

watch(() => story.selectedType, async (type) => {
  if (!type) return
  story.selectedSort = ''
  story.selectedIndex = ''
  story.selectedChapter = -1
  story.sorts = []
  story.indices = []
  story.chapters = []
  await story.fetchSorts(type)
  if (story.sorts.length === 0) story.fetchIndex(type, '')
})

watch(() => story.selectedSort, async (sort) => {
  if (!sort || !story.selectedType) return
  story.selectedIndex = ''
  story.selectedChapter = -1
  story.indices = []
  story.chapters = []
  await story.fetchIndex(story.selectedType, sort)
})

watch(() => story.selectedIndex, async (idx) => {
  if (!idx || !story.selectedType) return
  story.selectedChapter = -1
  story.chapters = []
  if (idx !== '-') {
    await story.fetchChapters(story.selectedType, story.selectedSort, idx)
  }
})

async function downloadOne(chapter: number) {
  const taskId = dlFloat.add(story.selectedIndex + ' ch' + chapter)
  dlFloat.start(taskId)
  try {
    const { taskId: tid } = await api.downloadJson({
      storyType: story.selectedType,
      sort: story.selectedSort,
      index: story.selectedIndex,
      chapter,
      source: 'haruki',
      outputDir: outputDir.value,
    })
    let done = false
    while (!done) {
      await new Promise(r => setTimeout(r, 300))
      const prog = await api.downloadProgress(tid)
      if (prog.total > 0) {
        dlFloat.progress(taskId, prog.read, prog.total, Math.round((prog.read / prog.total) * 100))
      }
      if (prog.status === 'done') {
        dlFloat.done(taskId, prog.filePath || '')
        done = true
      } else if (prog.status === 'error') {
        dlFloat.fail(taskId, prog.error || '下载失败')
        done = true
      }
    }
  } catch (e: any) {
    dlFloat.fail(taskId, e.message || '下载失败')
  }
}

async function handleDownload() {
  if (!story.selectedType || !story.selectedIndex || !outputDir.value) {
    toast.show('请填写所有字段', 'warn')
    return
  }
  // 未选章节 = 下载该索引下的全部章节（逐章排队，各自独立进度）
  if (story.selectedChapter < 0) {
    if (!story.chapters.length) {
      toast.show('该索引没有可下载的章节', 'warn')
      return
    }
    toast.show(`未选择章节，将下载全部 ${story.chapters.length} 章`, 'info')
    for (const c of story.chapters) {
      await downloadOne(c.number)
    }
    return
  }
  await downloadOne(story.selectedChapter)
}
</script>

<template>
  <div class="min-h-screen page-bg text-[var(--color-text)]">
    <header class="sticky top-0 z-[var(--z-sticky)] bg-[color-mix(in_oklch,var(--color-bg)_82%,transparent)] backdrop-blur-md border-b border-[var(--color-border)]">
      <div class="max-w-3xl mx-auto px-6 h-14 flex items-center gap-3">
        <button @click="router.push('/')" class="icon-btn -ml-1" title="返回编辑器"><ArrowLeft :size="18" /></button>
        <h1 class="text-base font-bold tracking-tight">JSON 下载</h1>
      </div>
    </header>

    <main class="max-w-3xl mx-auto px-6 py-8 space-y-6">
      <!-- 选择故事 -->
      <section class="app-card p-5">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-info/12 text-info"><BookOpen :size="15" /></span>
          <div class="section-title">选择故事</div>
        </div>

        <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <div>
            <label class="app-label">故事类型</label>
            <SkSelect
              class="mt-1.5"
              :model-value="story.selectedType"
              @update:model-value="story.selectedType = $event as string"
              :options="story.storyTypes.map(t => ({ value: t, label: t }))"
              placeholder="选择类型"
            />
          </div>

          <div v-if="story.sorts.length">
            <label class="app-label">排序</label>
            <SkSelect
              class="mt-1.5"
              :model-value="story.selectedSort"
              @update:model-value="story.selectedSort = $event as string"
              :options="story.sorts.map(s => ({ value: s.value, label: s.label }))"
              placeholder="选择排序"
            />
          </div>

          <div>
            <label class="app-label">索引</label>
            <SkSelect
              class="mt-1.5"
              :model-value="story.selectedIndex"
              @update:model-value="story.selectedIndex = $event as string"
              :options="displayIndices.map(i => ({ value: i.value, label: i.label }))"
              placeholder="选择索引"
            />
          </div>

          <!-- 无排序列时章节是单数项，占满整行居中，别孤零零挂在左下角 -->
          <div :class="story.sorts.length ? '' : 'sm:col-span-2 sm:justify-self-center sm:w-[calc(50%-0.375rem)]'">
            <label class="app-label">章节</label>
            <SkSelect
              class="mt-1.5"
              :model-value="story.selectedChapter"
              @update:model-value="story.selectedChapter = $event as number"
              :options="story.chapters.map(c => ({ value: c.number, label: c.label }))"
              placeholder="不选 = 下载全部章节"
            />
          </div>
        </div>
      </section>

      <!-- 下载选项 -->
      <section class="app-card p-5">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-success/12 text-success"><Download :size="15" /></span>
          <div class="section-title">下载选项</div>
        </div>

        <div>
          <label class="app-label">输出目录</label>
          <div class="flex gap-2 mt-1.5">
            <input
              v-model="outputDir"
              type="text"
              placeholder="输入或浏览选择保存目录..."
              class="app-input flex-1"
            />
            <button v-if="isTauri" @click="browseOutputDir" class="btn btn-sm btn-ghost border border-[var(--color-border)] whitespace-nowrap">
              <FolderOpen :size="15" /> 浏览
            </button>
            <button @click="handleDownload" class="btn btn-sm btn-brand whitespace-nowrap">
              <Download :size="15" /> 下载
            </button>
          </div>
        </div>
      </section>
    </main>
  </div>
</template>
