<script setup lang="ts">
const props = defineProps<{
  x: number
  y: number
}>()

const emit = defineEmits<{
  select: [brackets: string]
  close: []
}>()

const bracketOptions = [
  { label: '替换为「」', value: '「」' },
  { label: '替换为『』', value: '『』' },
  { label: '替换为（）', value: '（）' },
  { label: '替换为“”', value: '“”' },
  { label: '替换为‘’', value: '‘’' },
]
</script>

<template>
  <div
    class="fixed z-[var(--z-dropdown)] min-w-[170px] p-1.5 bg-[var(--color-surface)] border border-[var(--color-border)] rounded-[var(--radius-control)] shadow-[var(--shadow-lg)]"
    :style="{ left: x + 'px', top: y + 'px' }"
    @click.self="emit('close')"
  >
    <button
      v-for="opt in bracketOptions"
      :key="opt.value"
      class="w-full text-left px-2.5 py-1.5 text-sm text-[var(--color-text)] rounded-md transition-colors hover:bg-[color-mix(in_oklch,var(--color-base-content)_8%,transparent)] hover:text-[var(--color-primary)]"
      @click="emit('select', opt.value)"
    >
      {{ opt.label }}
    </button>
  </div>
</template>
