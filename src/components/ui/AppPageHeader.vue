<script setup lang="ts">
import { computed } from 'vue'
import { ArrowLeft } from 'lucide-vue-next'

const props = withDefaults(defineProps<{
  title: string
  subtitle?: string
  width?: '3xl' | '4xl' | '5xl' | '6xl'
  backTitle?: string
  showBack?: boolean
}>(), {
  subtitle: '',
  width: '4xl',
  backTitle: '返回编辑器',
  showBack: false,
})

const emit = defineEmits<{ back: [] }>()

const widthClass = computed(() => ({
  '3xl': 'max-w-3xl',
  '4xl': 'max-w-4xl',
  '5xl': 'max-w-5xl',
  '6xl': 'max-w-6xl',
})[props.width])
</script>

<template>
  <header class="app-page-header">
    <div class="w-full mx-auto px-6 min-h-16 py-2.5 flex items-center gap-3" :class="widthClass">
      <button v-if="showBack" @click="emit('back')" class="icon-btn -ml-1 shrink-0" :title="backTitle" :aria-label="backTitle">
        <ArrowLeft :size="18" />
      </button>
      <div class="min-w-0">
        <h1 class="text-[1.02rem] font-bold tracking-tight leading-tight">{{ title }}</h1>
        <p v-if="subtitle" class="mt-0.5 text-[0.7rem] leading-tight text-[var(--color-text-tertiary)] truncate">
          {{ subtitle }}
        </p>
      </div>
      <div v-if="$slots.default" class="ml-auto flex items-center gap-2 shrink-0">
        <slot />
      </div>
    </div>
  </header>
</template>
