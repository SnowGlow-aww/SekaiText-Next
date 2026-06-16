<script setup lang="ts">
import { reactive } from 'vue'
import { useAppStore } from '../../stores/app'
import { useSettingsStore } from '../../stores/settings'
import { useToast } from '../../composables/useToast'
import { useDebugLog } from '../../composables/useDebugLog'
import type { Settings } from '../../types/api'

const app = useAppStore()
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
  <div class="modal modal-open" @click.self="emit('close')">
    <div class="modal-box max-w-md max-h-[85vh]">
      <div class="flex items-center justify-between mb-5">
        <h2 class="text-lg font-semibold">设置</h2>
        <button
          @click="emit('close')"
          class="btn btn-ghost btn-sm btn-circle text-lg leading-none"
        >✕</button>
      </div>

      <div class="space-y-5">
        <!-- Font Size -->
        <div class="flex items-center justify-between">
          <div>
            <div class="text-sm font-medium">字号</div>
            <div class="text-xs opacity-60 mt-0.5">编辑器文本显示大小</div>
          </div>
          <div class="flex items-center gap-2">
            <input
              v-model.number="local.fontSize"
              type="range" min="10" max="48" step="1"
              class="range range-primary range-xs w-28"
            />
            <span class="text-sm w-8 text-center font-mono">{{ local.fontSize }}</span>
          </div>
        </div>

        <div class="divider my-0" />

        <!-- Download Source -->
        <div class="flex items-center justify-between">
          <div>
            <div class="text-sm font-medium">下载源</div>
            <div class="text-xs opacity-60 mt-0.5">故事 JSON 数据来源</div>
          </div>
          <span class="text-sm opacity-60">HarukiBot NEO</span>
        </div>

        <div class="divider my-0" />

        <!-- Save \N -->
        <label class="flex items-center justify-between cursor-pointer">
          <div>
            <div class="text-sm font-medium">保存 \\N 换行符</div>
            <div class="text-xs opacity-60 mt-0.5">翻译文件中保留 \\N 换行标记</div>
          </div>
          <input v-model="local.saveN" type="checkbox" class="toggle toggle-primary toggle-sm" />
        </label>

        <div class="divider my-0" />

        <!-- Save Voice -->
        <label class="flex items-center justify-between cursor-pointer">
          <div>
            <div class="text-sm font-medium">保存语音文件</div>
            <div class="text-xs opacity-60 mt-0.5">下载并保存语音文件到本地</div>
          </div>
          <input v-model="local.saveVoice" type="checkbox" class="toggle toggle-primary toggle-sm" />
        </label>

        <div class="divider my-0" />

        <!-- SSL Verification -->
        <label class="flex items-center justify-between cursor-pointer">
          <div>
            <div class="text-sm font-medium">SSL 验证</div>
            <div class="text-xs opacity-60 mt-0.5">禁用 SSL 证书验证（某些网络环境需要）</div>
          </div>
          <input v-model="local.disableSSL" type="checkbox" class="toggle toggle-primary toggle-sm" />
        </label>

        <div class="divider my-0" />

        <!-- Index Order -->
        <div class="flex items-center justify-between">
          <div>
            <div class="text-sm font-medium">索引排序</div>
            <div class="text-xs opacity-60 mt-0.5">故事索引下拉列表的显示顺序</div>
          </div>
          <select v-model="local.indexOrder" class="select select-bordered select-sm w-44">
            <option value="desc">降序（最新的在底部）</option>
            <option value="asc">升序（最新的在顶部）</option>
          </select>
        </div>

        <div class="divider my-0" />

        <!-- Appearance -->
        <div class="flex items-center justify-between">
          <div>
            <div class="text-sm font-medium">外观模式</div>
            <div class="text-xs opacity-60 mt-0.5">跟随系统或手动指定亮暗色</div>
          </div>
          <select
            v-model="app.themeMode"
            class="select select-bordered select-sm w-44"
          >
            <option value="system">跟随系统</option>
            <option value="light">浅色</option>
            <option value="dark">深色</option>
          </select>
        </div>

        <div class="divider my-0" />

        <!-- Debug -->
        <label class="flex items-center justify-between cursor-pointer">
          <div>
            <div class="text-sm font-medium">调试日志</div>
            <div class="text-xs opacity-60 mt-0.5">在底部显示调试日志窗口</div>
          </div>
          <input v-model="local.debugEnabled" type="checkbox" class="toggle toggle-primary toggle-sm" />
        </label>
      </div>

      <div class="modal-action">
        <button
          @click="emit('close')"
          class="btn btn-ghost btn-sm"
        >
          取消
        </button>
        <button
          @click="save()"
          class="btn btn-primary btn-sm"
        >
          保存设置
        </button>
      </div>
    </div>
  </div>
</template>
