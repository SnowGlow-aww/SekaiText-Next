<script setup lang="ts">
import { CheckCircle, XCircle, Info, AlertTriangle } from 'lucide-vue-next'
import { useToast } from '../composables/useToast'

const { toasts } = useToast()

const iconMap: Record<string, typeof CheckCircle> = {
  success: CheckCircle,
  error: XCircle,
  info: Info,
  warn: AlertTriangle,
}
</script>

<template>
  <div class="fixed top-4 right-4 z-[var(--z-toast)] flex flex-col gap-2 pointer-events-none">
    <div
      v-for="t in toasts"
      :key="t.id"
      class="app-card pointer-events-auto flex items-center gap-2.5 pl-2.5 pr-4 py-2 shadow-[var(--shadow-lg)] text-sm text-[var(--color-text)] transition-all duration-300 animate-in"
    >
      <span
        class="grid place-items-center w-7 h-7 rounded-lg shrink-0"
        :class="{
          'bg-success/12 text-success': t.type === 'success',
          'bg-error/15 text-error': t.type === 'error',
          'bg-info/12 text-info': t.type === 'info',
          'bg-warning/15 text-warning': t.type === 'warn',
        }"
      >
        <component :is="iconMap[t.type]" :size="15" />
      </span>
      <span class="leading-snug">{{ t.message }}</span>
    </div>
  </div>
</template>

<style scoped>
@keyframes toast-in {
  from { opacity: 0; transform: translateX(20px); }
  to { opacity: 1; transform: translateX(0); }
}
.animate-in {
  animation: toast-in 0.2s ease-out;
}
</style>
