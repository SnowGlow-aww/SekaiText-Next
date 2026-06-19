<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowLeft, Play, ChevronRight, ChevronLeft } from 'lucide-vue-next'
import StoryNavigator from '../components/navigation/StoryNavigator.vue'
import Live2DStage from '../components/live2d/Live2DStage.vue'
import { useStoryStore } from '../stores/story'

const router = useRouter()
const story = useStoryStore()

const stageRef = ref<InstanceType<typeof Live2DStage> | null>(null)
const autoPlay = ref(false)
const voiceVolume = ref(1)
const bgmVolume = ref(0.6)
const status = ref('')
const progress = ref({ current: 0, total: 0 })

function playSelected() {
  if (!story.selectedType || story.selectedChapter < 0) {
    status.value = '请先选择剧情类型和章节'
    return
  }
  status.value = '加载中...'
  stageRef.value?.play(
    story.selectedType,
    story.selectedSort,
    story.selectedIndex,
    story.selectedChapter,
  )
}

function onLoaded() { status.value = '播放中' }
function onError(msg: string) { status.value = '失败: ' + msg }
function onEnded() { status.value = '剧情结束' }
function onProgress(c: number, t: number) { progress.value = { current: c, total: t } }

// Jump to a specific dialog line via the progress input.
const jumpInput = ref<number | null>(null)
function jumpToLine() {
  const total = progress.value.total
  if (!total || jumpInput.value == null) return
  const line = Math.max(1, Math.min(Math.round(jumpInput.value), total))
  stageRef.value?.seekToLine(line)
  jumpInput.value = null
}

watch([voiceVolume, bgmVolume], ([v, b]) => stageRef.value?.setVolumes(v, b))
</script>

<template>
  <div class="h-screen flex flex-col bg-base-100">
    <header class="border-b border-base-300 px-4 py-2 flex items-center gap-3 shrink-0 flex-wrap">
      <button @click="router.push('/')" class="btn btn-ghost btn-sm gap-1.5">
        <ArrowLeft :size="18" /> 返回
      </button>
      <span class="text-sm font-medium shrink-0">Live2D 播放器</span>
      <div class="flex-1 min-w-0"><StoryNavigator /></div>
      <button @click="playSelected" class="btn btn-primary btn-sm gap-1.5">
        <Play :size="16" /> 播放
      </button>
    </header>

    <div class="border-b border-base-300 px-4 py-1.5 flex items-center gap-4 text-sm shrink-0 flex-wrap">
      <label class="flex items-center gap-1.5 cursor-pointer">
        <input v-model="autoPlay" type="checkbox" class="toggle toggle-primary toggle-sm" />
        自动播放
      </label>
      <button v-if="!autoPlay" @click="stageRef?.prev()" class="btn btn-ghost btn-sm gap-1">
        <ChevronLeft :size="16" /> 上一步
      </button>
      <button v-if="!autoPlay" @click="stageRef?.advance()" class="btn btn-ghost btn-sm gap-1">
        下一步 <ChevronRight :size="16" />
      </button>
      <label class="flex items-center gap-1.5">
        语音
        <input v-model.number="voiceVolume" type="range" min="0" max="1" step="0.05" class="range range-primary range-xs w-24" />
      </label>
      <label class="flex items-center gap-1.5">
        BGM
        <input v-model.number="bgmVolume" type="range" min="0" max="1" step="0.05" class="range range-primary range-xs w-24" />
      </label>
      <div v-if="progress.total" class="flex items-center gap-2 flex-1 min-w-32 max-w-md">
        <progress
          class="progress progress-primary flex-1"
          :value="progress.current"
          :max="progress.total"
        />
        <span class="text-xs opacity-60 tabular-nums shrink-0">第</span>
        <input
          v-model.number="jumpInput"
          type="number"
          min="1"
          :max="progress.total"
          :placeholder="String(progress.current)"
          class="input input-bordered input-xs w-14 text-center tabular-nums"
          @keyup.enter="jumpToLine"
          @blur="jumpToLine"
          title="输入句号后回车跳转"
        />
        <span class="text-xs opacity-60 tabular-nums shrink-0">/ {{ progress.total }} 句</span>
      </div>
      <span class="text-xs opacity-60 ml-auto shrink-0">{{ status }}</span>
    </div>

    <main class="flex-1 min-h-0">
      <Live2DStage
        ref="stageRef"
        :source="story.selectedSource"
        :auto-play="autoPlay"
        :voice-volume="voiceVolume"
        :bgm-volume="bgmVolume"
        @loaded="onLoaded"
        @error="onError"
        @progress="onProgress"
        @ended="onEnded"
      />
    </main>
  </div>
</template>
