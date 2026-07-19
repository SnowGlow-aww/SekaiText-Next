<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted, onActivated, onDeactivated, nextTick } from 'vue'
import { Save, Trash2, Pause, Play } from 'lucide-vue-next'
import { useDebugLog } from '../composables/useDebugLog'
import { BASE_URL } from '../api/client'
import AppPageHeader from '../components/ui/AppPageHeader.vue'

const { logs, clear: clearFrontend } = useDebugLog()

interface ServerLog {
  timestamp: string
  message: string
}

const serverLogs = ref<ServerLog[]>([])
let pollTimer: ReturnType<typeof setInterval> | null = null
const logContainer = ref<HTMLElement | null>(null)

interface MergedEntry {
  id: string
  ts: string
  msg: string
  type: 'info' | 'warn' | 'error' | 'server'
}

const mergedLogs = ref<MergedEntry[]>([])

function mergeLogs() {
  const all: MergedEntry[] = []

  for (const entry of logs.value) {
    all.push({ id: `f-${all.length}`, ts: entry.ts, msg: entry.msg, type: entry.type })
  }

  for (const entry of serverLogs.value) {
    all.push({ id: `s-${all.length}`, ts: entry.timestamp, msg: entry.message, type: 'server' })
  }

  // Sort by timestamp (approximate - just use position to keep order roughly)
  mergedLogs.value = all
}

async function fetchServerLogs() {
  try {
    const res = await fetch(`${BASE_URL}/debug/logs`)
    if (res.ok) {
      serverLogs.value = await res.json()
      mergeLogs()
      scrollToBottom()
    }
  } catch {
    // server not available
  }
}

// 自动滚动可暂停：复现问题时新日志会把现场刷出视野，暂停后位置钉住不动。
const autoScroll = ref(true)
function toggleAutoScroll() {
  autoScroll.value = !autoScroll.value
  if (autoScroll.value) scrollToBottom()
}
function scrollToBottom() {
  if (!autoScroll.value) return
  nextTick(() => {
    if (logContainer.value) {
      logContainer.value.scrollTop = logContainer.value.scrollHeight
    }
  })
}

watch(logs, () => {
  mergeLogs()
  scrollToBottom()
}, { deep: true })

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

onMounted(mergeLogs)
// Under <keep-alive> (App.vue) onUnmounted does not fire on navigation, so the
// 2s poll is tied to activation — otherwise it keeps hitting /debug/logs forever
// after the page is left once.
onActivated(() => {
  fetchServerLogs()
  if (!pollTimer) pollTimer = setInterval(fetchServerLogs, 2000)
})
onDeactivated(stopPolling)
onUnmounted(stopPolling)

async function saveLogs() {
  const lines = mergedLogs.value.map(e => {
    const tag = e.type === 'server' ? 'server' : 'front'
    return `[${e.ts}] [${tag}] ${e.msg}`
  }).join('\n')
  try {
    const res = await fetch(`${BASE_URL}/debug/save`, { method: 'POST' })
    if (res.ok) {
      const data = await res.json()
      addLogLine(`日志已保存 (${data.lines} 行 → debug.log)`, 'info')
    }
  } catch {
    // Fallback: download as blob
    const blob = new Blob([lines], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `sekai-debug-${Date.now()}.log`
    a.click()
    URL.revokeObjectURL(url)
  }
}

let lineId = 0
function addLogLine(msg: string, type: 'info' | 'warn' | 'error') {
  const ts = new Date().toLocaleTimeString()
  mergedLogs.value.push({ id: `log-${lineId++}`, ts, msg, type })
  scrollToBottom()
}

function clearAll() {
  clearFrontend()
  serverLogs.value = []
  mergedLogs.value = []
}
</script>

<template>
  <div class="h-full min-h-0 page-bg text-[var(--color-text)] flex flex-col">
    <AppPageHeader title="调试日志" subtitle="前端与后端运行记录 · debug.log" width="6xl">
      <div class="flex items-center gap-2">
        <span class="text-xs text-[var(--color-text-tertiary)]">{{ mergedLogs.length }} lines</span>
        <button
          @click="toggleAutoScroll"
          class="btn btn-ghost btn-xs gap-1"
          :class="autoScroll ? '' : 'text-warning'"
          :title="autoScroll ? '暂停自动滚动，钉住当前位置' : '恢复自动滚动到底部'"
        >
          <component :is="autoScroll ? Pause : Play" :size="13" /> {{ autoScroll ? '停止滚动' : '恢复滚动' }}
        </button>
        <button @click="saveLogs" class="btn btn-ghost btn-xs gap-1">
          <Save :size="13" /> 保存日志
        </button>
        <button @click="clearAll" class="btn btn-ghost btn-xs gap-1">
          <Trash2 :size="13" /> 清空
        </button>
      </div>
    </AppPageHeader>

    <!-- Terminal Output -->
    <main
      ref="logContainer"
      class="flex-1 overflow-y-auto p-3 font-mono text-xs leading-relaxed"
    >
      <div v-if="mergedLogs.length === 0" class="text-[var(--color-text-tertiary)] py-4">
        <div class="mb-1"><span class="text-[var(--color-text-secondary)]">$</span> <span class="text-[var(--color-text-secondary)]">waiting for logs...</span></div>
        <div>[sekai:debug] console capture active</div>
        <div>[sekai:debug] polling server logs at /api/v1/debug/logs</div>
      </div>

      <div v-else class="space-y-0">
        <div
          v-for="entry in mergedLogs"
          :key="entry.id"
          class="flex gap-2"
        >
          <span class="text-[var(--color-text-tertiary)] flex-shrink-0 w-16 select-none">{{ entry.ts }}</span>
          <span v-if="entry.type === 'server'" class="text-info flex-shrink-0">[server]</span>
          <span v-else class="text-[var(--color-text-tertiary)] flex-shrink-0">[front]</span>
          <span :class="{
            'text-[var(--color-text)]': entry.type === 'info',
            'text-warning': entry.type === 'warn',
            'text-error': entry.type === 'error',
            'text-success': entry.type === 'server',
          }" class="whitespace-pre-wrap break-all">{{ entry.msg }}</span>
        </div>
      </div>
    </main>
  </div>
</template>
