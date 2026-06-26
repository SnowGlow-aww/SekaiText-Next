<script setup lang="ts">
import { reactive } from 'vue'
import { X, SlidersHorizontal } from 'lucide-vue-next'
import { useSettingsStore } from '../../stores/settings'
import { useToast } from '../../composables/useToast'
import { useDebugLog } from '../../composables/useDebugLog'
import type { Settings } from '../../types/api'
import ThemePicker from '../ui/ThemePicker.vue'
import SkSelect from '../ui/SkSelect.vue'

const settings = useSettingsStore()
const toast = useToast()
const debug = useDebugLog()

const emit = defineEmits<{
  close: []
}>()

const local = reactive<Settings>(JSON.parse(JSON.stringify(settings.settings)))

async function save() {
  Object.assign(settings.settings, local)
  debug.enabled.value = local.debugEnabled
  try {
    await settings.saveSettings()
    toast.show('设置已保存', 'success')
    emit('close')
  } catch {
    toast.show('保存失败', 'error')
  }
}
</script>

<template>
  <Transition name="settings-fade" appear>
    <div
      class="fixed inset-0 flex items-center justify-center p-4 z-[var(--z-modal)]"
      @keydown.esc="emit('close')"
    >
      <!-- scrim -->
      <div class="absolute inset-0 bg-black/45 backdrop-blur-[2px]" @click="emit('close')" />

      <!-- panel -->
      <div
        class="app-card app-glass relative w-full max-w-md max-h-[85vh] flex flex-col"
        style="box-shadow: var(--shadow-lg)"
      >
        <!-- header -->
        <div class="flex items-center justify-between gap-3 px-5 pt-5 pb-4 border-b border-[var(--color-border)]">
          <div class="flex items-center gap-2">
            <span class="grid place-items-center w-7 h-7 rounded-lg bg-primary/12 text-primary">
              <SlidersHorizontal :size="15" />
            </span>
            <h2 class="section-title">设置</h2>
          </div>
          <button @click="emit('close')" class="icon-btn -mr-1" title="关闭"><X :size="18" /></button>
        </div>

        <!-- body (scrolls when the theme picker makes it tall) -->
        <div class="overflow-y-auto px-5 py-4 space-y-5">
          <!-- Font Size -->
          <div class="flex items-center justify-between gap-4">
            <div>
              <div class="text-sm font-medium text-[var(--color-text)]">字号</div>
              <div class="app-help mt-0.5">编辑器文本显示大小</div>
            </div>
            <div class="flex items-center gap-2">
              <input
                v-model.number="local.fontSize"
                type="range" min="10" max="48" step="1"
                class="range range-primary range-xs w-28"
              />
              <span class="text-sm w-8 text-center font-mono text-[var(--color-text)]">{{ local.fontSize }}</span>
            </div>
          </div>

          <div class="app-divider" />

          <!-- Download Source -->
          <div class="flex items-center justify-between gap-4">
            <div>
              <div class="text-sm font-medium text-[var(--color-text)]">下载源</div>
              <div class="app-help mt-0.5">故事 JSON 数据来源</div>
            </div>
            <span class="text-sm text-[var(--color-text-secondary)]">HarukiBot NEO</span>
          </div>

          <div class="app-divider" />

          <!-- Save \N -->
          <label class="flex items-center justify-between gap-4 cursor-pointer">
            <div>
              <div class="text-sm font-medium text-[var(--color-text)]">保存 \\N 换行符</div>
              <div class="app-help mt-0.5">翻译文件中保留 \\N 换行标记</div>
            </div>
            <input v-model="local.saveN" type="checkbox" class="toggle toggle-primary toggle-sm" />
          </label>

          <div class="app-divider" />

          <!-- Save Voice -->
          <label class="flex items-center justify-between gap-4 cursor-pointer">
            <div>
              <div class="text-sm font-medium text-[var(--color-text)]">保存语音文件</div>
              <div class="app-help mt-0.5">下载并保存语音文件到本地</div>
            </div>
            <input v-model="local.saveVoice" type="checkbox" class="toggle toggle-primary toggle-sm" />
          </label>

          <div class="app-divider" />

          <!-- SSL Verification -->
          <label class="flex items-center justify-between gap-4 cursor-pointer">
            <div>
              <div class="text-sm font-medium text-[var(--color-text)]">SSL 验证</div>
              <div class="app-help mt-0.5">禁用 SSL 证书验证（某些网络环境需要）</div>
            </div>
            <input v-model="local.disableSSL" type="checkbox" class="toggle toggle-primary toggle-sm" />
          </label>

          <div class="app-divider" />

          <!-- Index Order -->
          <div class="flex items-center justify-between gap-4">
            <div>
              <div class="text-sm font-medium text-[var(--color-text)]">索引排序</div>
              <div class="app-help mt-0.5">故事索引下拉列表的显示顺序</div>
            </div>
            <SkSelect
              class="w-44"
              :model-value="local.indexOrder"
              @update:model-value="local.indexOrder = $event as 'asc' | 'desc'"
              :options="[{ value: 'desc', label: '降序（最新的在底部）' }, { value: 'asc', label: '升序（最新的在顶部）' }]"
            />
          </div>

          <div class="app-divider" />

          <!-- Appearance -->
          <div>
            <div class="text-sm font-medium text-[var(--color-text)]">外观模式</div>
            <div class="app-help mt-0.5 mb-3">跟随系统或手动指定亮暗色，并选择推しカラー主题色</div>
            <ThemePicker />
          </div>

          <div class="app-divider" />

          <!-- Debug -->
          <label class="flex items-center justify-between gap-4 cursor-pointer">
            <div>
              <div class="text-sm font-medium text-[var(--color-text)]">调试日志</div>
              <div class="app-help mt-0.5">在底部显示调试日志窗口</div>
            </div>
            <input v-model="local.debugEnabled" type="checkbox" class="toggle toggle-primary toggle-sm" />
          </label>
        </div>

        <!-- footer -->
        <div class="flex justify-end gap-2 px-5 py-4 border-t border-[var(--color-border)]">
          <button @click="emit('close')" class="btn btn-sm btn-ghost border border-[var(--color-border)]">取消</button>
          <button @click="save()" class="btn btn-sm btn-brand">保存设置</button>
        </div>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.settings-fade-enter-active,
.settings-fade-leave-active {
  transition: opacity var(--dur) var(--ease-out);
}
.settings-fade-enter-from,
.settings-fade-leave-to {
  opacity: 0;
}
.settings-fade-enter-active .app-card,
.settings-fade-leave-active .app-card {
  transition: transform var(--dur) var(--ease-out);
}
.settings-fade-enter-from .app-card {
  transform: translateY(8px) scale(0.97);
}
</style>
