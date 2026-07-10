<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Monitor, Sun, Moon, Check, Upload, X, ImagePlus, Trash2 } from 'lucide-vue-next'
import { useAppStore, FONT_OPTIONS, BG_VEIL_MIN } from '../../stores/app'
import { useSettingsStore } from '../../stores/settings'
import { usePluginRegistry } from '../../plugin-host/registry'
import { ACCENT_GROUPS, ACCENT_NAME_BY_COLOR } from '../../data/characterColors'
import type { ThemeMode } from '../../stores/app'
import SkSelect from './SkSelect.vue'

const app = useAppStore()
const settings = useSettingsStore()
const pluginRegistry = usePluginRegistry()

// Commit the UI zoom on release (@change), not on every drag tick — applying the
// root font-size live would reflow the whole page (and this slider) under the
// cursor, which feels jittery/over-sensitive. The draft drives the live number
// readout while dragging; the zoom commits when the user lets go.
const uiFontDraft = ref(settings.settings.uiFontSize)
watch(() => settings.settings.uiFontSize, (v) => { uiFontDraft.value = v })

const modes: { value: ThemeMode; label: string; icon: typeof Monitor }[] = [
  { value: 'system', label: '跟随系统', icon: Monitor },
  { value: 'light', label: '浅色', icon: Sun },
  { value: 'dark', label: '深色', icon: Moon },
]

// Live2D dock placement (left intentionally omitted — that edge is the nav).
// Effective only with the Live2D plugin installed; harmless otherwise.
const live2dPositionOptions = [
  { value: 'top', label: '顶部' },
  { value: 'bottom', label: '底部' },
  { value: 'window', label: '独立窗口' },
]

const currentName = computed(() => ACCENT_NAME_BY_COLOR[app.accentColor.toLowerCase()] ?? '自定义')

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

// ── Font import ────────────────────────────────────────────────────────────
const fontInput = ref<HTMLInputElement | null>(null)
const fontError = ref('')

const fontOptions = computed(() => [
  ...FONT_OPTIONS.map((f) => ({ value: f.value, label: f.label })),
  ...app.customFonts.map((f) => ({ value: f.id, label: `${f.label}（导入）` })),
])

async function onFontFile(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  fontError.value = ''
  try {
    await app.importFont(file)
  } catch {
    fontError.value = '无法加载该字体文件（仅支持 .ttf / .otf / .woff / .woff2）'
  }
}

// ── Background image ───────────────────────────────────────────────────────
const bgInput = ref<HTMLInputElement | null>(null)
const bgError = ref('')

async function onBgFile(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  bgError.value = ''
  try {
    await app.importBackground(file)
  } catch {
    bgError.value = '请选择有效的图片文件'
  }
}
</script>

<template>
  <div class="space-y-5">
    <!-- Theme mode + UI font size -->
    <div class="flex flex-wrap items-start gap-x-8 gap-y-4">
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

      <!-- UI zoom: scales the whole interface; committed on release (see uiFontDraft) -->
      <div>
        <div class="app-label mb-2">界面缩放</div>
        <div class="flex items-center gap-2 h-9">
          <input v-model.number="uiFontDraft" type="range" min="12" max="25" step="1" @change="settings.settings.uiFontSize = uiFontDraft" class="range range-primary range-xs w-[200px]" />
          <span class="text-sm w-8 text-center font-mono">{{ uiFontDraft }}</span>
        </div>
      </div>

      <!-- Live2D dock placement: where the player sits relative to the editor.
           Only meaningful (and only shown) while the Live2D plugin is enabled. -->
      <div v-if="pluginRegistry.isLoaded('live2d')">
        <div class="app-label mb-2">Live2D 对照位置</div>
        <div class="h-9 flex items-center">
          <SkSelect
            class="w-[200px]"
            :model-value="settings.settings.live2dPosition || 'window'"
            @update:model-value="settings.settings.live2dPosition = $event as string"
            :options="live2dPositionOptions"
          />
        </div>
      </div>

      <!-- Font — 与 Live2D 对照位置同排（选择框宽度对齐 200px） -->
      <div>
        <div class="app-label mb-2">字体</div>
        <div class="flex items-center gap-2 h-9">
          <SkSelect
            class="w-[200px]"
            :model-value="app.fontFamily"
            @update:model-value="app.fontFamily = $event as string"
            :options="fontOptions"
          />
          <input ref="fontInput" type="file" accept=".ttf,.otf,.woff,.woff2,font/*" class="sr-only" @change="onFontFile" />
          <button class="btn btn-sm btn-ghost border border-[var(--color-border)] gap-1.5 shrink-0" @click="fontInput?.click()">
            <Upload :size="14" /> 导入字体
          </button>
        </div>
        <div v-if="fontError" class="app-help mt-1.5 text-[var(--color-error,#e5484d)]">{{ fontError }}</div>
        <div v-else class="app-help mt-1.5">支持 .ttf / .otf / .woff / .woff2，导入后即可选用</div>

        <!-- Imported fonts (manage / remove) -->
        <div v-if="app.customFonts.length" class="flex flex-wrap gap-2 mt-2.5 max-w-[320px]">
          <span
            v-for="f in app.customFonts"
            :key="f.id"
            class="inline-flex items-center gap-1.5 pl-2.5 pr-1.5 py-1 rounded-[var(--radius-pill)] text-xs border border-[var(--color-border)] bg-[var(--color-bg)]"
          >
            {{ f.label }}
            <button
              class="grid place-items-center w-4 h-4 rounded-full text-[var(--color-text-tertiary)] hover:text-[var(--color-text)] hover:bg-[var(--color-border)] transition-colors"
              title="移除此字体"
              @click="app.removeCustomFont(f.id)"
            >
              <X :size="12" />
            </button>
          </span>
        </div>
      </div>
    </div>

    <!-- Accent / oshi colour -->
    <div>
      <div class="flex items-center justify-between mb-2">
        <span class="app-label">主题色 · 推しカラー</span>
        <span class="app-help">当前：{{ currentName }}</span>
      </div>

      <!-- Character swatches by unit (Miku's teal is the default — first swatch) -->
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

    <!-- Background image -->
    <div>
      <div class="app-label mb-2">背景图片</div>
      <input ref="bgInput" type="file" accept="image/*" class="sr-only" @change="onBgFile" />

      <template v-if="!app.bgEnabled">
        <button class="btn btn-sm btn-ghost border border-[var(--color-border)] gap-1.5" @click="bgInput?.click()">
          <ImagePlus :size="14" /> 导入背景图
        </button>
        <div v-if="bgError" class="app-help mt-1.5 text-[var(--color-error,#e5484d)]">{{ bgError }}</div>
        <div v-else class="app-help mt-1.5">为界面设置个性化壁纸 · 文本可读性自动保障</div>
      </template>

      <template v-else>
        <div class="flex items-center gap-3 mb-3">
          <div
            class="w-20 h-12 rounded-[var(--radius-control)] border border-[var(--color-border)] bg-center bg-cover shrink-0"
            :style="{ backgroundImage: app.bgThumb ? `url(${app.bgThumb})` : '' }"
          />
          <button class="btn btn-sm btn-ghost border border-[var(--color-border)] gap-1.5" @click="bgInput?.click()">
            <ImagePlus :size="14" /> 更换
          </button>
          <button class="btn btn-sm btn-ghost gap-1.5 text-[var(--color-text-secondary)] hover:text-[var(--color-text)]" @click="app.removeBackground()">
            <Trash2 :size="14" /> 移除
          </button>
        </div>

        <!-- Readability veil (floored) -->
        <div class="flex items-center justify-between gap-3 mb-2">
          <div>
            <div class="text-sm font-medium">蒙版强度</div>
            <div class="app-help mt-0.5">越高文字越清晰（不可低于 {{ BG_VEIL_MIN }}% 以保证可读性）</div>
          </div>
          <div class="flex items-center gap-2 shrink-0">
            <input v-model.number="app.bgVeil" type="range" :min="BG_VEIL_MIN" max="95" step="1" class="range range-primary range-xs w-28" />
            <span class="text-sm w-10 text-center font-mono">{{ app.bgVeil }}%</span>
          </div>
        </div>

        <!-- Blur -->
        <div class="flex items-center justify-between gap-3">
          <div>
            <div class="text-sm font-medium">背景模糊</div>
            <div class="app-help mt-0.5">柔化壁纸，进一步提升前景文字可读性</div>
          </div>
          <div class="flex items-center gap-2 shrink-0">
            <input v-model.number="app.bgBlur" type="range" min="0" max="24" step="1" class="range range-primary range-xs w-28" />
            <span class="text-sm w-10 text-center font-mono">{{ app.bgBlur }}px</span>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>
