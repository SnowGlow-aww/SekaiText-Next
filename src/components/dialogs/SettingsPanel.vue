<script setup lang="ts">
import { Settings, Palette, X, Check } from 'lucide-vue-next'
import { useSettingsStore } from '../../stores/settings'
import ThemePicker from '../ui/ThemePicker.vue'
import SkSelect from '../ui/SkSelect.vue'

const settings = useSettingsStore()

const emit = defineEmits<{
  close: []
}>()
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
        class="app-card app-glass relative w-full max-w-md p-5 max-h-[85vh] overflow-y-auto"
        style="box-shadow: var(--shadow-lg)"
      >
        <!-- header -->
        <div class="flex items-center gap-2 mb-5">
          <span class="grid place-items-center w-8 h-8 rounded-lg bg-primary/12 text-primary"><Settings :size="16" /></span>
          <h2 class="text-base font-bold tracking-tight">设置</h2>
          <button class="icon-btn ml-auto -mr-1" title="关闭" @click="emit('close')"><X :size="18" /></button>
        </div>

        <!-- 常规 -->
        <div class="space-y-3.5">
          <div class="flex items-center justify-between gap-4">
            <span class="text-sm">字号</span>
            <input
              v-model.number="settings.settings.fontSize"
              type="number"
              min="10"
              max="48"
              class="app-input w-20 text-center"
            />
          </div>

          <div class="flex items-center justify-between gap-4">
            <span class="text-sm">下载源</span>
            <SkSelect
              class="w-44"
              :model-value="settings.settings.downloadSource"
              @update:model-value="settings.settings.downloadSource = $event as string"
              :options="[
                { value: 'best', label: 'sekai.best' },
                { value: 'unipjsk', label: 'unipjsk.com' },
                { value: 'haruki', label: 'haruki (CN)' },
                { value: 'moesekai-jp', label: 'Moesekai (JP)' },
                { value: 'moesekai-cn', label: 'Moesekai (CN)' },
              ]"
            />
          </div>

          <div class="flex items-center justify-between gap-4">
            <span class="text-sm">保存 \\N 换行符</span>
            <input v-model="settings.settings.saveN" type="checkbox" class="toggle toggle-primary toggle-sm" />
          </div>

          <div class="flex items-center justify-between gap-4">
            <span class="text-sm">SSL 验证</span>
            <input v-model="settings.settings.disableSSL" type="checkbox" class="toggle toggle-primary toggle-sm" />
          </div>
        </div>

        <div class="app-divider my-5" />

        <!-- 外观 -->
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-secondary/12 text-secondary"><Palette :size="15" /></span>
          <div class="section-title">外观</div>
        </div>
        <ThemePicker />

        <!-- footer -->
        <div class="flex justify-end gap-2 mt-6">
          <button class="btn btn-sm btn-ghost border border-[var(--color-border)]" @click="emit('close')">取消</button>
          <button class="btn btn-sm btn-brand" @click="settings.saveSettings(); emit('close')">
            <Check :size="15" /> 保存
          </button>
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
