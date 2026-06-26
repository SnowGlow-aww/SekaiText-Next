<script setup lang="ts">
import { ref } from 'vue'
import { api } from '../../api/client'
import { useToast } from '../../composables/useToast'

const props = defineProps<{
  scenarioId: string
  voiceIds: string[]
  volume?: number[]
  source?: string
  chara2d?: number
}>()

const playing = ref(false)
const loading = ref(false)
const audioRef = ref<HTMLAudioElement | null>(null)
const toast = useToast()

async function play() {
  if (playing.value && audioRef.value) {
    audioRef.value.pause()
    audioRef.value = null
    playing.value = false
    return
  }

  // Immediate feedback so the click is visibly acknowledged during the async window.
  loading.value = true
  try {
    const result = await api.voiceUrl(props.scenarioId, props.voiceIds[0], props.source || 'sekai.best', props.chara2d)
    if (result.url) {
      const audio = new Audio(result.url)
      // talk.volume carries the Unity linear gain multiplier (typically ~1), not a
      // 0-100 percentage — dividing by 100 made every voiced line near-silent.
      // Apply it directly, clamped to the HTMLAudioElement [0,1] range; treat a
      // falsy/unspecified value as the default 1.
      audio.volume = props.volume?.[0] ? Math.min(1, Math.max(0, props.volume[0])) : 1
      audio.onended = () => { playing.value = false; loading.value = false }
      audio.onerror = () => {
        console.error('[VoicePlayButton] 音频加载失败:', result.url)
        playing.value = false
        loading.value = false
        audioRef.value = null
        toast.show('语音加载失败，请检查网络或更换源', 'error')
      }
      audioRef.value = audio
      await audio.play()
      playing.value = true
    } else {
      toast.show('未找到该语音', 'warn')
    }
  } catch (e) {
    console.error('[VoicePlayButton] 播放失败:', e)
    playing.value = false
    audioRef.value = null
    toast.show('语音播放失败：' + (e instanceof Error ? e.message : String(e)), 'error')
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <button
    @click="play"
    :disabled="loading"
    class="w-8 h-8 rounded-full border border-[var(--color-border)] flex items-center justify-center hover:text-[var(--color-primary)] transition-colors text-xs disabled:opacity-50 disabled:cursor-not-allowed"
    :class="{ 'bg-[var(--color-primary)] text-white border-[var(--color-primary)]': playing }"
    :title="loading ? '加载中...' : playing ? '停止' : '播放语音'"
  >
    {{ loading ? '…' : playing ? '⏹' : '▶' }}
  </button>
</template>
