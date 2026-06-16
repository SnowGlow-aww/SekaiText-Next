<script setup lang="ts">
import { useAppStore } from '../../stores/app'
import { useSettingsStore } from '../../stores/settings'

const app = useAppStore()
const settings = useSettingsStore()

const emit = defineEmits<{
  close: []
}>()
</script>

<template>
  <div class="modal modal-open" @click.self="emit('close')">
    <div class="modal-box max-w-md">
      <h2 class="text-lg font-semibold mb-4">设置</h2>

      <div class="space-y-4">
        <div class="flex items-center justify-between">
          <label class="text-sm">字号</label>
          <input
            v-model.number="settings.settings.fontSize"
            type="number"
            min="10"
            max="48"
            class="input input-bordered input-sm w-20 text-center"
          />
        </div>

        <div class="flex items-center justify-between">
          <label class="text-sm">下载源</label>
          <select
            v-model="settings.settings.downloadSource"
            class="select select-bordered select-sm"
          >
            <option value="best">sekai.best</option>
            <option value="unipjsk">unipjsk.com</option>
            <option value="haruki">haruki (CN)</option>
            <option value="moesekai-jp">Moesekai (JP)</option>
            <option value="moesekai-cn">Moesekai (CN)</option>
          </select>
        </div>

        <div class="flex items-center justify-between">
          <label class="text-sm">保存 \\N 换行符</label>
          <input v-model="settings.settings.saveN" type="checkbox" class="toggle toggle-primary toggle-sm" />
        </div>

        <div class="flex items-center justify-between">
          <label class="text-sm">SSL 验证</label>
          <input v-model="settings.settings.disableSSL" type="checkbox" class="toggle toggle-primary toggle-sm" />
        </div>

        <div class="flex items-center justify-between">
          <label class="text-sm">外观模式</label>
          <select
            v-model="app.themeMode"
            class="select select-bordered select-sm"
          >
            <option value="system">跟随系统</option>
            <option value="light">浅色</option>
            <option value="dark">深色</option>
          </select>
        </div>
      </div>

      <div class="modal-action">
        <button
          class="btn btn-ghost btn-sm"
          @click="emit('close')"
        >
          取消
        </button>
        <button
          class="btn btn-primary btn-sm"
          @click="settings.saveSettings(); emit('close')"
        >
          保存
        </button>
      </div>
    </div>
  </div>
</template>
