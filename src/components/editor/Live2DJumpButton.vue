<script setup lang="ts">
import { Drama } from 'lucide-vue-next'
import { usePluginRegistry } from '../../plugin-host/registry'
import { useLive2dDockStore } from '../../stores/live2dDock'
import { useStoryStore } from '../../stores/story'

// Per-line "在 Live2D 中播放" control. Sits directly below the voice button in the
// editor's per-line button stack, sharing its flat 扁长方形 (wide+short) shape.
// Renders ONLY when the Live2D plugin is installed; clicking publishes a jump on
// the host-owned live2dDock store, which the plugin's docked panel / detached
// player watches and applies.
const props = defineProps<{
  scenarioId: string
  // 0-based ordinal of this dialogue among the story's spoken/Talk lines, in
  // display order — the FALLBACK anchor (see voiceId below). Computed by the
  // editor (EditorWorkspace.talkIndexFor).
  talkIndex: number
  // The clicked line's first voice clip id when it has one. PREFERRED anchor:
  // the plugin matches the snippet by voice id first (exact, no index math) and
  // only falls back to talkIndex for voiceless lines.
  voiceId?: string
}>()

const registry = usePluginRegistry()
const dock = useLive2dDockStore()
const story = useStoryStore()

async function jump() {
  // Guard: a scenario must be supplied and a story must actually be loaded
  // (the player reads the shared host story store). Without these there is
  // nothing to seek to.
  if (!props.scenarioId || !story.scenarioId) return
  await dock.requestJump(props.scenarioId, props.talkIndex, props.voiceId)
}
</script>

<template>
  <button
    v-if="registry.isLoaded('live2d')"
    @click="jump"
    class="flex items-center justify-center gap-1.5 h-7 px-2.5 w-full rounded-[var(--radius-control)] border border-[var(--color-border)] text-xs leading-none text-[var(--color-text-secondary)] transition-colors hover:border-[var(--color-primary)] hover:text-[var(--color-primary)]"
    title="在 Live2D 中播放"
  >
    <Drama :size="12" />
    <span>Live2D</span>
  </button>
</template>
