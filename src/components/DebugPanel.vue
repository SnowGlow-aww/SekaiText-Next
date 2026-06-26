<script setup lang="ts">
import { onMounted, watch } from 'vue'
import { Bug, Trash2, Inbox } from 'lucide-vue-next'
import { useDebugLog } from '../composables/useDebugLog'
import { useSettingsStore } from '../stores/settings'

const { logs, enabled, clear } = useDebugLog()
const settings = useSettingsStore()

onMounted(() => {
  enabled.value = settings.settings.debugEnabled
})

watch(enabled, (v) => {
  settings.settings.debugEnabled = v
})
</script>

<template>
  <div
    v-show="enabled"
    class="fixed bottom-0 right-0 w-[420px] h-60 bg-[var(--color-surface)] border border-[var(--color-border)] rounded-tl-xl shadow-[var(--shadow-lg)] flex flex-col z-[9998] text-xs font-mono"
  >
    <div class="flex items-center justify-between px-3 py-1.5 border-b border-[var(--color-border)] bg-[color-mix(in_oklch,var(--color-base-content)_5%,transparent)] rounded-tl-xl">
      <span class="flex items-center gap-1.5 font-semibold text-[var(--color-text-secondary)]">
        <Bug :size="13" /> Debug Log
      </span>
      <button @click="clear" class="btn btn-ghost btn-xs gap-1 text-[var(--color-text-secondary)]">
        <Trash2 :size="12" /> 清空
      </button>
    </div>
    <div class="flex-1 overflow-y-auto p-2 space-y-0.5">
      <div v-if="logs.length === 0" class="flex flex-col items-center justify-center gap-1.5 pt-10 text-[var(--color-text-tertiary)]">
        <Inbox :size="20" />
        <span>暂无日志</span>
      </div>
      <div v-for="(entry, i) in logs" :key="i" class="flex gap-2 leading-5">
        <span class="text-[var(--color-text-tertiary)] flex-shrink-0 w-16">{{ entry.ts }}</span>
        <span :class="{
          'text-success': entry.type === 'info',
          'text-warning': entry.type === 'warn',
          'text-error': entry.type === 'error',
        }">{{ entry.msg }}</span>
      </div>
    </div>
  </div>
</template>
