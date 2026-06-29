<script setup lang="ts">
import { computed, ref } from 'vue'
import { X, Drama } from 'lucide-vue-next'
import { usePluginRegistry } from '../../plugin-host/registry'
import { useLive2dDockStore } from '../../stores/live2dDock'

// A framed, resizable region docked to one edge of the editor workspace. It hosts
// whatever dock panel the Live2D plugin registered (host.registerDockPanel) — the
// host stays agnostic about the player internals. Placement decides the resize
// axis: right → width, top/bottom → height. 'left' is intentionally unsupported.
const props = defineProps<{ placement: 'top' | 'right' | 'bottom' }>()

const registry = usePluginRegistry()
const dock = useLive2dDockStore()

// The first dock panel any plugin contributed (Live2D is the only one today).
const panel = computed(() => registry.dockPanels[0] ?? null)

const isVertical = computed(() => props.placement === 'right') // resize along x

const boxStyle = computed(() =>
  isVertical.value
    ? { width: dock.size + 'px' }
    : { height: dock.size + 'px' },
)

// ── Drag-to-resize ──────────────────────────────────────────────────────────
const MIN = 240
const MAX = 1000
const dragging = ref(false)

function onResizeStart(e: PointerEvent) {
  dragging.value = true
  const startPos = isVertical.value ? e.clientX : e.clientY
  const startSize = dock.size
  // right: dock is to the RIGHT of the workspace, so dragging left (smaller x)
  // grows it → invert dx. top: dragging down grows it. bottom: dragging up grows.
  const sign = props.placement === 'right' || props.placement === 'bottom' ? -1 : 1
  const move = (ev: PointerEvent) => {
    const cur = isVertical.value ? ev.clientX : ev.clientY
    const next = startSize + sign * (cur - startPos)
    dock.size = Math.min(MAX, Math.max(MIN, Math.round(next)))
  }
  const up = () => {
    dragging.value = false
    window.removeEventListener('pointermove', move)
    window.removeEventListener('pointerup', up)
  }
  window.addEventListener('pointermove', move)
  window.addEventListener('pointerup', up)
  e.preventDefault()
}

// Resize handle sits on the edge facing the workspace.
const handleClass = computed(() => {
  switch (props.placement) {
    case 'right': return 'absolute left-0 top-0 h-full w-1.5 cursor-col-resize'
    case 'top': return 'absolute left-0 bottom-0 w-full h-1.5 cursor-row-resize'
    case 'bottom': return 'absolute left-0 top-0 w-full h-1.5 cursor-row-resize'
  }
  return ''
})

// Border on the edge facing the workspace, so the dock reads as attached.
const edgeBorder = computed(() => {
  switch (props.placement) {
    case 'right': return 'border-l'
    case 'top': return 'border-b'
    case 'bottom': return 'border-t'
  }
  return ''
})
</script>

<template>
  <div
    class="relative flex flex-col flex-shrink-0 bg-[var(--color-surface)] overflow-hidden border-[var(--color-border)]"
    :class="edgeBorder"
    :style="boxStyle"
  >
    <!-- Resize grip on the workspace-facing edge -->
    <div
      :class="handleClass"
      class="z-10 hover:bg-[var(--color-primary)]/40 transition-colors"
      @pointerdown="onResizeStart"
    />

    <!-- Header -->
    <div class="flex items-center justify-between px-2.5 h-8 flex-shrink-0 border-b border-[var(--color-border)] bg-[var(--color-bg)]">
      <span class="inline-flex items-center gap-1.5 text-xs font-medium text-[var(--color-text-secondary)]">
        <Drama :size="13" /> Live2D 对照
      </span>
      <button
        class="grid place-items-center w-5 h-5 rounded text-[var(--color-text-tertiary)] hover:text-[var(--color-text)] hover:bg-[var(--color-border)] transition-colors"
        title="关闭对照"
        @click="dock.hide()"
      >
        <X :size="13" />
      </button>
    </div>

    <!-- Panel body: the plugin's contributed component, or a fallback hint -->
    <div class="flex-1 min-h-0 min-w-0">
      <component :is="panel.component" v-if="panel" />
      <div v-else class="h-full grid place-items-center p-4 text-center text-xs text-[var(--color-text-tertiary)]">
        Live2D 插件未提供对照面板，请将插件更新到最新版本。
      </div>
    </div>
  </div>
</template>
