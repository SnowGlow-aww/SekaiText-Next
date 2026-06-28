<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted, onActivated, onDeactivated, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowLeft, Terminal, Save, Trash2 } from 'lucide-vue-next'
import { useDebugLog } from '../composables/useDebugLog'
import { BASE_URL } from '../api/client'

const router = useRouter()
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

function scrollToBottom() {
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
  <div class="h-screen bg-[var(--color-bg)] text-[var(--color-text)] flex flex-col">
    <!-- Header -->
    <header class="flex items-center justify-between px-4 py-2.5 bg-[var(--color-surface)] border-b border-[var(--color-border)] flex-shrink-0 select-none">
      <div class="flex items-center gap-3">
        <button @click="router.push('/')" class="icon-btn -ml-1" title="返回"><ArrowLeft :size="16" /></button>
        <span class="grid place-items-center w-6 h-6 rounded-md bg-primary/12 text-primary"><Terminal :size="13" /></span>
        <span class="text-sm font-bold tracking-tight">调试日志</span>
        <span class="font-mono text-[11px] text-[var(--color-text-tertiary)]">debug.log</span>
      </div>
      <div class="flex items-center gap-2">
        <span class="text-xs text-[var(--color-text-tertiary)]">{{ mergedLogs.length }} lines</span>
        <button @click="saveLogs" class="btn btn-ghost btn-xs gap-1">
          <Save :size="13" /> 保存日志
        </button>
        <button @click="clearAll" class="btn btn-ghost btn-xs gap-1">
          <Trash2 :size="13" /> 清空
        </button>
      </div>
    </header>

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
