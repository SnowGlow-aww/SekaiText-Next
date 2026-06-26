<script setup lang="ts">
import { Loader2, CheckCircle2, XCircle } from 'lucide-vue-next'
import { useDownloadFloat } from '../composables/useDownloadFloat'

export interface DownloadTask {
  id: number
  name: string
  status: 'pending' | 'downloading' | 'done' | 'error'
  result?: string
  error?: string
  read?: number
  total?: number
  percent?: number
}

const { tasks } = useDownloadFloat()
</script>

<template>
  <div class="fixed top-4 right-4 z-[var(--z-toast)] flex flex-col gap-2 pointer-events-none">
    <div
      v-for="t in tasks"
      :key="t.id"
      class="app-card shadow-[var(--shadow-lg)] pointer-events-auto px-4 py-3 text-sm min-w-64 animate-in"
    >
      <div class="flex items-center gap-2.5">
        <span
          v-if="t.status === 'downloading' || t.status === 'pending'"
          class="grid place-items-center w-7 h-7 rounded-lg bg-accent/12 text-accent shrink-0"
        ><Loader2 :size="15" class="animate-spin" /></span>
        <span
          v-else-if="t.status === 'done'"
          class="grid place-items-center w-7 h-7 rounded-lg bg-success/12 text-success shrink-0"
        ><CheckCircle2 :size="15" /></span>
        <span
          v-else-if="t.status === 'error'"
          class="grid place-items-center w-7 h-7 rounded-lg bg-error/15 text-error shrink-0"
        ><XCircle :size="15" /></span>
        <div class="flex-1 min-w-0">
          <div class="font-medium truncate text-[var(--color-text)]">{{ t.name }}</div>
          <div v-if="t.status === 'downloading'" class="mt-1.5">
            <div class="flex justify-between text-xs text-[var(--color-text-secondary)] mb-1">
              <span>正在下载...</span>
              <span v-if="t.percent !== undefined">{{ t.percent }}%</span>
            </div>
            <div class="w-full h-1 rounded-full overflow-hidden bg-[color-mix(in_oklch,var(--color-base-content)_10%,transparent)]">
              <div
                class="h-full rounded-full bg-accent transition-all duration-300"
                :style="{ width: (t.percent || 0) + '%' }"
              />
            </div>
          </div>
          <div v-else-if="t.status === 'done' && t.result" class="text-xs text-[var(--color-text-tertiary)] mt-0.5 truncate">{{ t.result }}</div>
          <div v-else-if="t.status === 'error' && t.error" class="text-xs text-error mt-0.5">{{ t.error }}</div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
@keyframes float-in {
  from { opacity: 0; transform: translateX(20px); }
  to { opacity: 1; transform: translateX(0); }
}
.animate-in {
  animation: float-in 0.2s ease-out;
}
</style>
