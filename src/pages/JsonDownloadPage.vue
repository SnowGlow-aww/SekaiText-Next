<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowLeft, BookOpen, Download } from 'lucide-vue-next'
import { useStoryStore } from '../stores/story'
import { useSettingsStore } from '../stores/settings'
import { api } from '../api/client'
import { useToast } from '../composables/useToast'
import { useDownloadFloat } from '../composables/useDownloadFloat'

const router = useRouter()
const story = useStoryStore()
const settings = useSettingsStore()
const toast = useToast()
const dlFloat = useDownloadFloat()

const outputDir = ref(settings.settings.jsonDownloadDir || '')

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

async function handleDownload() {
  if (!story.selectedType || !story.selectedIndex || story.selectedChapter < 0 || !outputDir.value) {
    toast.show('请填写所有字段', 'warn')
    return
  }
  const taskId = dlFloat.add(story.selectedIndex + ' ch' + story.selectedChapter)
  dlFloat.start(taskId)
  try {
    const { taskId: tid } = await api.downloadJson({
      storyType: story.selectedType,
      sort: story.selectedSort,
      index: story.selectedIndex,
      chapter: story.selectedChapter,
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
</script>

<template>
  <div class="min-h-screen bg-[var(--color-bg)] text-[var(--color-text)]">
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
            <select v-model="story.selectedType" class="app-input mt-1.5 cursor-pointer">
              <option value="" disabled>选择类型</option>
              <option v-for="t in story.storyTypes" :key="t" :value="t">{{ t }}</option>
            </select>
          </div>

          <div v-if="story.sorts.length">
            <label class="app-label">排序</label>
            <select v-model="story.selectedSort" class="app-input mt-1.5 cursor-pointer">
              <option value="" disabled>选择排序</option>
              <option v-for="s in story.sorts" :key="s.value" :value="s.value">{{ s.label }}</option>
            </select>
          </div>

          <div>
            <label class="app-label">索引</label>
            <select v-model="story.selectedIndex" class="app-input mt-1.5 cursor-pointer">
              <option value="" disabled>选择索引</option>
              <option v-for="i in displayIndices" :key="i.value" :value="i.value">{{ i.label }}</option>
            </select>
          </div>

          <div>
            <label class="app-label">章节</label>
            <select v-model="story.selectedChapter" class="app-input mt-1.5 cursor-pointer">
              <option :value="-1" disabled>选择章节</option>
              <option v-for="c in story.chapters" :key="c.number" :value="c.number">{{ c.label }}</option>
            </select>
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
              placeholder="输入保存目录路径..."
              class="app-input flex-1"
            />
            <button @click="handleDownload" class="btn btn-sm btn-brand whitespace-nowrap">
              <Download :size="15" /> 下载
            </button>
          </div>
        </div>
      </section>
    </main>
  </div>
</template>
