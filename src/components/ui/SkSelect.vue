<script setup lang="ts">
import { ref, computed, nextTick, watch } from 'vue'
import { onClickOutside, useEventListener } from '@vueuse/core'
import { ChevronDown, Check } from 'lucide-vue-next'

interface SkOption {
  value: string | number
  label: string
  disabled?: boolean
}

const props = withDefaults(defineProps<{
  modelValue: string | number
  options: SkOption[]
  placeholder?: string
  disabled?: boolean
  size?: 'sm' | 'md'
}>(), {
  placeholder: '请选择',
  disabled: false,
  size: 'md',
})

const emit = defineEmits<{ 'update:modelValue': [value: string | number] }>()

const root = ref<HTMLElement | null>(null)
const panel = ref<HTMLElement | null>(null)
const open = ref(false)
const activeIdx = ref(-1)

// Fixed-position coords for the teleported panel (viewport-based, matches the
// trigger width and flips above when there's no room below). Upward placement
// anchors via `bottom` so it never needs an inline transform that would fight
// the enter/leave transition.
const pos = ref({ left: 0, top: 0, bottom: 0, minWidth: 0, placement: 'bottom' as 'bottom' | 'top' })

const selected = computed(() => props.options.find(o => o.value === props.modelValue))
const display = computed(() => selected.value?.label ?? props.placeholder)
const isPlaceholder = computed(() => !selected.value)

function measure() {
  const el = root.value
  if (!el) return
  const r = el.getBoundingClientRect()
  const room = window.innerHeight - r.bottom
  const placement = room < 240 && r.top > room ? 'top' : 'bottom'
  pos.value = {
    left: r.left,
    top: r.bottom + 6,
    bottom: window.innerHeight - r.top + 6,
    minWidth: r.width,
    placement,
  }
}

// The panel sizes to its widest option (width: max-content, floored at the
// trigger width). Once it's mounted we know its real width, so nudge it back
// onto the screen if growing rightward would push it past the viewport edge.
function clampHoriz() {
  const p = panel.value
  if (!p) return
  const w = p.offsetWidth
  const max = window.innerWidth - 8
  const left = pos.value.left + w > max ? Math.max(8, max - w) : pos.value.left
  if (left !== pos.value.left) pos.value = { ...pos.value, left }
}

async function toggle() {
  if (props.disabled) return
  if (open.value) { close(); return }
  measure()
  open.value = true
  activeIdx.value = props.options.findIndex(o => o.value === props.modelValue)
  await nextTick()
  clampHoriz()
  panel.value?.querySelector('[data-active="true"]')?.scrollIntoView({ block: 'nearest' })
}

function close() { open.value = false; activeIdx.value = -1 }

function pick(opt: SkOption) {
  if (opt.disabled) return
  emit('update:modelValue', opt.value)
  close()
}

function move(delta: number) {
  const n = props.options.length
  if (!n) return
  let i = activeIdx.value
  for (let step = 0; step < n; step++) {
    i = (i + delta + n) % n
    if (!props.options[i]?.disabled) { activeIdx.value = i; break }
  }
  nextTick(() => {
    panel.value?.querySelectorAll('[role=option]')[activeIdx.value]?.scrollIntoView({ block: 'nearest' })
  })
}

function onKeydown(e: KeyboardEvent) {
  if (props.disabled) return
  if (!open.value) {
    if (e.key === 'Enter' || e.key === ' ' || e.key === 'ArrowDown') { e.preventDefault(); toggle() }
    return
  }
  if (e.key === 'Escape') { e.preventDefault(); close() }
  else if (e.key === 'ArrowDown') { e.preventDefault(); move(1) }
  else if (e.key === 'ArrowUp') { e.preventDefault(); move(-1) }
  else if (e.key === 'Enter' || e.key === ' ') {
    e.preventDefault()
    const opt = props.options[activeIdx.value]
    if (opt) pick(opt)
  }
}

onClickOutside(root, () => close(), { ignore: [panel] })
useEventListener(window, 'scroll', () => { if (open.value) { measure(); nextTick(clampHoriz) } }, true)
useEventListener(window, 'resize', () => { if (open.value) { measure(); nextTick(clampHoriz) } })
watch(() => props.disabled, (d) => { if (d) close() })
</script>

<template>
  <div ref="root" class="sk-select-root" :class="{ 'sk-is-full': size === 'md' }">
    <button
      type="button"
      class="sk-trigger"
      :class="[size, { open, placeholder: isPlaceholder }]"
      :disabled="disabled"
      @click="toggle"
      @keydown="onKeydown"
    >
      <span class="truncate">{{ display }}</span>
      <ChevronDown :size="size === 'sm' ? 13 : 15" class="sk-chevron" :class="{ flip: open }" />
    </button>

    <Teleport to="body">
      <Transition name="pop">
        <div
          v-if="open"
          ref="panel"
          class="sk-panel"
          :data-placement="pos.placement"
          :style="{
            left: pos.left + 'px',
            minWidth: pos.minWidth + 'px',
            ...(pos.placement === 'bottom' ? { top: pos.top + 'px' } : { bottom: pos.bottom + 'px' }),
          }"
        >
          <button
            v-for="(opt, i) in options"
            :key="opt.value"
            role="option"
            type="button"
            class="sk-opt"
            :data-active="i === activeIdx"
            :class="{ selected: opt.value === modelValue, active: i === activeIdx, disabled: opt.disabled }"
            :disabled="opt.disabled"
            @click="pick(opt)"
            @mousemove="activeIdx = i"
          >
            <span class="truncate">{{ opt.label }}</span>
            <Check v-if="opt.value === modelValue" :size="14" class="shrink-0" />
          </button>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>

<style scoped>
.sk-select-root {
  display: inline-flex;
  position: relative;
}
/* The md full-width default lives in the global components layer (.sk-is-full)
   so caller width utilities can override it — see style.css. */

.sk-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  width: 100%;
  color: var(--color-text);
  background: var(--color-bg);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  cursor: pointer;
  transition: border-color var(--dur-fast) var(--ease-out), box-shadow var(--dur-fast) var(--ease-out);
}
.sk-trigger.md {
  min-height: 2.25rem;
  padding: 0.5rem 0.75rem;
  font-size: 0.875rem;
}
.sk-trigger.sm {
  min-height: 2rem;
  width: auto;
  min-width: 6rem;
  padding: 0.25rem 0.6rem;
  font-size: 0.8125rem;
}
.sk-trigger:hover:not(:disabled) {
  border-color: var(--color-border-strong);
}
.sk-trigger.open {
  border-color: var(--accent, var(--color-primary));
  box-shadow: 0 0 0 3px color-mix(in oklch, var(--accent, var(--color-primary)) 22%, transparent);
}
.sk-trigger.placeholder {
  color: var(--color-text-tertiary);
}
.sk-trigger:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.sk-chevron {
  flex-shrink: 0;
  color: var(--color-text-tertiary);
  transition: transform var(--dur) var(--ease-out);
}
.sk-chevron.flip {
  transform: rotate(180deg);
}

.sk-panel {
  position: fixed;
  z-index: 1000;
  width: max-content;
  max-width: calc(100vw - 16px);
  max-height: 16rem;
  overflow-y: auto;
  scrollbar-gutter: stable;
  padding: 0.25rem;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  box-shadow: var(--shadow-lg);
  background: color-mix(in oklch, var(--color-surface) 82%, transparent);
  backdrop-filter: blur(14px) saturate(1.2);
  -webkit-backdrop-filter: blur(14px) saturate(1.2);
  transform-origin: top center;
}
.sk-panel[data-placement="top"] {
  transform-origin: bottom center;
}
@supports not ((backdrop-filter: blur(1px)) or (-webkit-backdrop-filter: blur(1px))) {
  .sk-panel { background: var(--color-surface); }
}

.sk-opt {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  width: 100%;
  text-align: left;
  padding: 0.4rem 0.55rem;
  border-radius: calc(var(--radius-control) - 2px);
  font-size: 0.8125rem;
  color: var(--color-text);
  cursor: pointer;
  transition: background-color var(--dur-fast) var(--ease-out), color var(--dur-fast) var(--ease-out);
}
.sk-opt.active {
  background: color-mix(in oklch, var(--color-base-content) 8%, transparent);
}
.sk-opt.selected {
  color: var(--accent, var(--color-primary));
  font-weight: 600;
}
.sk-opt.selected.active {
  background: color-mix(in oklch, var(--accent, var(--color-primary)) 14%, transparent);
}
.sk-opt.disabled {
  color: var(--color-text-tertiary);
  cursor: default;
}

.pop-enter-active {
  transition: opacity var(--dur-fast) var(--ease-out), transform var(--dur-fast) var(--ease-out);
}
.pop-leave-active {
  transition: opacity 0.1s ease, transform 0.1s ease;
}
.pop-enter-from,
.pop-leave-to {
  opacity: 0;
  transform: translateY(-4px) scale(0.98);
}
@media (prefers-reduced-motion: reduce) {
  .pop-enter-active, .pop-leave-active { transition: opacity 0.01ms !important; }
}
</style>
