<script setup lang="ts">
import { computed } from 'vue'
import { Monitor, Sun, Moon, Check, Sparkles } from 'lucide-vue-next'
import { useAppStore, FONT_OPTIONS } from '../../stores/app'
import { ACCENT_GROUPS, ACCENT_NAME_BY_COLOR } from '../../data/characterColors'
import type { ThemeMode } from '../../stores/app'
import SkSelect from './SkSelect.vue'

const app = useAppStore()

const modes: { value: ThemeMode; label: string; icon: typeof Monitor }[] = [
  { value: 'system', label: '跟随系统', icon: Monitor },
  { value: 'light', label: '浅色', icon: Sun },
  { value: 'dark', label: '深色', icon: Moon },
]

const currentName = computed(() => {
  if (app.accentColor === 'rainbow') return 'PJSK 多彩'
  return ACCENT_NAME_BY_COLOR[app.accentColor.toLowerCase()] ?? '自定义'
})

function isActive(color: string) {
  return app.accentColor.toLowerCase() === color.toLowerCase()
}

// Selected swatch gets an offset ring in its own colour (no tailwind ring utils,
// so no custom-property style keys that vue-tsc rejects).
function swatchStyle(color: string): Record<string, string> {
  const s: Record<string, string> = { backgroundColor: color }
  if (isActive(color)) s.boxShadow = `0 0 0 2px var(--color-surface), 0 0 0 4px ${color}`
  return s
}
</script>

<template>
  <div class="space-y-5">
    <!-- Theme mode -->
    <div>
      <div class="app-label mb-2">主题模式</div>
      <div class="inline-flex p-1 rounded-[var(--radius-control)] bg-[var(--color-bg)] border border-[var(--color-border)] gap-1">
        <button
          v-for="m in modes"
          :key="m.value"
          class="flex items-center gap-1.5 px-3 py-1.5 rounded-[0.45rem] text-xs font-medium transition-colors"
          :class="app.themeMode === m.value
            ? 'bg-[var(--color-surface)] text-[var(--color-text)] shadow-[var(--shadow-sm)]'
            : 'text-[var(--color-text-secondary)] hover:text-[var(--color-text)]'"
          @click="app.themeMode = m.value"
        >
          <component :is="m.icon" :size="14" />
          {{ m.label }}
        </button>
      </div>
    </div>

    <!-- Font -->
    <div>
      <div class="app-label mb-2">字体</div>
      <SkSelect
        class="max-w-xs"
        :model-value="app.fontFamily"
        @update:model-value="app.fontFamily = $event as string"
        :options="FONT_OPTIONS.map(f => ({ value: f.value, label: f.label }))"
      />
    </div>

    <!-- Accent / oshi colour -->
    <div>
      <div class="flex items-center justify-between mb-2">
        <span class="app-label">主题色 · 推しカラー</span>
        <span class="app-help">当前：{{ currentName }}</span>
      </div>

      <!-- Rainbow default -->
      <button
        class="group flex items-center gap-3 w-full p-2.5 rounded-[var(--radius-control)] border transition-colors mb-3"
        :class="app.accentColor === 'rainbow'
          ? 'border-[var(--accent)] bg-[color-mix(in_oklch,var(--accent)_10%,transparent)]'
          : 'border-[var(--color-border)] hover:border-[var(--color-border-strong)]'"
        @click="app.setAccent('rainbow')"
      >
        <span
          class="grid place-items-center w-8 h-8 rounded-full text-white shrink-0 shadow-[var(--shadow-sm)]"
          style="background-image: linear-gradient(135deg,#33ccbb,#4455dd 35%,#ff66bb 65%,#ff9900)"
        >
          <Sparkles :size="15" />
        </span>
        <div class="min-w-0 flex-1 text-left">
          <div class="text-sm font-medium text-[var(--color-text)]">PJSK 多彩渐变</div>
          <div class="app-help">默认 · 跨色相的活泼渐变</div>
        </div>
        <Check v-if="app.accentColor === 'rainbow'" :size="16" class="text-[var(--accent)] shrink-0" />
      </button>

      <!-- Character swatches by unit -->
      <div class="space-y-3">
        <div v-for="g in ACCENT_GROUPS" :key="g.unitId">
          <div class="flex items-center gap-1.5 mb-1.5">
            <span class="w-2.5 h-2.5 rounded-full shrink-0" :style="{ backgroundColor: g.unitColor }" />
            <span class="text-[0.68rem] font-semibold text-[var(--color-text-secondary)] tracking-wide">{{ g.unit }}</span>
          </div>
          <div class="flex flex-wrap gap-2">
            <button
              v-for="c in g.members"
              :key="c.id"
              :title="`${c.name} · ${c.color}`"
              class="relative w-8 h-8 rounded-full transition-transform hover:scale-110 focus-visible:scale-110"
              :style="swatchStyle(c.color)"
              @click="app.setAccent(c.color)"
            >
              <Check
                v-if="isActive(c.color)"
                :size="15"
                class="absolute inset-0 m-auto"
                :style="{ color: 'white', filter: 'drop-shadow(0 1px 1px rgba(0,0,0,.45))' }"
              />
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
