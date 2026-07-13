<script lang="ts">
// Module scope runs once and is shared by every VoicePlayButton instance, unlike
// <script setup> whose top-level runs per instance. Holds a stopper for whichever
// button is currently sounding so a new play() can silence it first — without this
// singleton, clicking a second line's button stacked its audio over the first one.
let stopActive: (() => void) | null = null
// Monotonic ticket for the most recent play() across all buttons. Each call claims
// the next value synchronously before its first await, then re-checks after every
// await that it is still the latest — a call that has been superseded by a later
// click bails instead of stacking a second voice, since stopActive is registered
// too late (after the awaits) to pre-empt an in-flight play.
let playSeq = 0
</script>

<script setup lang="ts">
import { ref } from 'vue'
import { Play, Square } from 'lucide-vue-next'
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

// Fully silence and reset this button, and relinquish the module-level singleton
// if this instance currently owns it.
function stop() {
  if (audioRef.value) {
    audioRef.value.pause()
    audioRef.value = null
  }
  playing.value = false
  loading.value = false
  if (stopActive === stop) stopActive = null
}

async function play() {
  if (playing.value && audioRef.value) {
    stop()
    return
  }

  // Silence any button currently playing, then claim this call's ticket — both
  // synchronously, before any await — so a rapid click on another line pre-empts
  // us: it bumps playSeq while we await, and the re-checks below make this (now
  // stale) call bail instead of sounding on top of the newest one.
  stopActive?.()
  const mySeq = ++playSeq

  // Immediate feedback so the click is visibly acknowledged during the async window.
  loading.value = true
  try {
    const result = await api.voiceUrl(props.scenarioId, props.voiceIds[0], props.source || 'sekai.best', props.chara2d)
    if (mySeq !== playSeq) return
    if (result.url) {
      const audio = new Audio(result.url)
      // talk.volume carries the Unity linear gain multiplier (typically ~1), not a
      // 0-100 percentage — dividing by 100 made every voiced line near-silent.
      // Apply it directly, clamped to the HTMLAudioElement [0,1] range. Use `!= null`
      // (not truthiness) so an explicit 0 — a silenced/faded line — stays silent
      // instead of being mistaken for "unspecified" and forced to full volume.
      audio.volume = props.volume?.[0] != null ? Math.min(1, Math.max(0, props.volume[0])) : 1
      audio.onended = () => { stop() }
      audio.onerror = () => {
        console.error('[VoicePlayButton] 音频加载失败:', result.url)
        stop()
        toast.show('语音加载失败，请检查网络或更换源', 'error')
      }
      audioRef.value = audio
      await audio.play()
      if (mySeq !== playSeq) {
        audio.pause()
        if (audioRef.value === audio) audioRef.value = null
        return
      }
      playing.value = true
      // Claim the singleton so the next play() on any button stops this one first.
      stopActive = stop
    } else {
      toast.show('未找到该语音', 'warn')
    }
  } catch (e) {
    console.error('[VoicePlayButton] 播放失败:', e)
    stop()
    toast.show('语音播放失败：' + (e instanceof Error ? e.message : String(e)), 'error')
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <!-- Flat 扁长方形 (wide+short) control: icon + tiny label, full-width within the
       per-line button stack (items-stretch makes it match the Live2D button). -->
  <button
    @click="play"
    :disabled="loading"
    class="flex items-center justify-center gap-1.5 h-7 px-2.5 w-full rounded-[var(--radius-control)] border border-[var(--color-border)] text-xs leading-none text-[var(--color-text-secondary)] transition-colors hover:border-[var(--color-primary)] hover:text-[var(--color-primary)] disabled:opacity-50 disabled:cursor-not-allowed"
    :class="{ 'bg-[var(--color-primary)] text-[var(--color-primary-content)] border-[var(--color-primary)] hover:text-[var(--color-primary-content)]': playing }"
    :title="loading ? '加载中...' : playing ? '停止' : '播放语音'"
  >
    <span v-if="loading" class="loading loading-spinner loading-xs" />
    <Square v-else-if="playing" :size="12" fill="currentColor" />
    <Play v-else :size="12" fill="currentColor" />
    <span>{{ playing ? '停止' : '语音' }}</span>
  </button>
</template>
