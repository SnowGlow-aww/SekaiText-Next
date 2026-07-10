<script setup lang="ts">
import { onMounted, computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowLeft, RefreshCw, Search, Package, Download, CircleCheck, ExternalLink } from 'lucide-vue-next'
import { useMarketStore } from '../stores/market'
import { useToast } from '../composables/useToast'
import { openExternal } from '../utils/openExternal'

const router = useRouter()
const market = useMarketStore()
const toast = useToast()
const query = ref('')

onMounted(() => { market.refresh() })

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return market.listings
  return market.listings.filter(
    (p) =>
      p.name.toLowerCase().includes(q) ||
      p.id.toLowerCase().includes(q) ||
      (p.description || '').toLowerCase().includes(q),
  )
})

async function install(id: string, name: string) {
  try {
    await market.install(id)
    toast.show(`插件「${name}」安装成功`, 'success')
  } catch (e: any) {
    toast.show('安装失败: ' + (e?.message || '未知错误'), 'error')
  }
}
</script>

<template>
  <div class="min-h-screen page-bg text-[var(--color-text)]">
    <header class="sticky top-0 z-[var(--z-sticky)] bg-[color-mix(in_oklch,var(--color-bg)_82%,transparent)] backdrop-blur-md border-b border-[var(--color-border)]">
      <div class="max-w-5xl mx-auto px-6 h-14 flex items-center gap-3">
        <button @click="router.push('/')" class="icon-btn -ml-1"><ArrowLeft :size="18" /></button>
        <h1 class="text-base font-bold tracking-tight">插件市场</h1>
        <button
          @click="market.refresh()"
          :disabled="market.loading"
          class="ml-auto btn btn-sm btn-ghost border border-[var(--color-border)] gap-1.5"
        >
          <RefreshCw :size="15" :class="{ 'animate-spin': market.loading }" /> 刷新
        </button>
      </div>
    </header>

    <main class="max-w-5xl mx-auto px-6 py-8 space-y-6">
      <div class="relative max-w-md">
        <Search :size="16" class="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-text-tertiary)] pointer-events-none" />
        <input
          v-model="query"
          type="text"
          placeholder="搜索插件…"
          class="app-input pl-9"
        />
      </div>

      <div v-if="market.loading" class="flex items-center justify-center gap-2 py-16 text-sm text-[var(--color-text-secondary)]">
        <span class="loading loading-spinner loading-sm" /> 加载中…
      </div>
      <div v-else-if="market.error" class="app-card p-5 flex items-center gap-3 text-sm text-error">
        <span class="flex-1">{{ market.error }}</span>
        <button @click="market.refresh()" class="btn btn-sm btn-ghost border border-[var(--color-border)] shrink-0">重试</button>
      </div>
      <div v-else-if="filtered.length === 0" class="flex flex-col items-center justify-center py-20 text-[var(--color-text-tertiary)]">
        <Package :size="40" class="mb-3 opacity-50" />
        <p class="text-sm">{{ query ? '没有匹配的插件' : '插件市场暂无内容' }}</p>
      </div>
      <div v-else class="grid gap-3 sm:grid-cols-2">
        <div
          v-for="p in filtered"
          :key="p.id"
          class="app-card app-card-hover p-4 flex flex-col"
        >
          <div class="flex items-start justify-between gap-2 mb-1">
            <div class="min-w-0">
              <h3 class="text-sm font-semibold truncate">{{ p.name }}</h3>
              <p class="text-xs text-[var(--color-text-secondary)] mt-0.5">
                <span class="font-mono">v{{ p.version }}</span>
                <span v-if="p.author"> · {{ p.author }}</span>
              </p>
            </div>
          </div>
          <p class="text-xs text-[var(--color-text-secondary)] flex-1 line-clamp-3 mb-3">{{ p.description }}</p>
          <div class="flex items-center justify-between gap-2">
            <!-- Tauri webview 不处理 target=_blank，必须经 openExternal 调系统浏览器 -->
            <button
              v-if="p.homepage"
              @click="openExternal(p.homepage)"
              class="text-xs text-[var(--color-text-secondary)] hover:text-[var(--color-text)] inline-flex items-center gap-1 transition-colors"
            ><ExternalLink :size="12" /> 主页</button>
            <span v-else />
            <button
              v-if="p.updateAvailable"
              @click="install(p.id, p.name)"
              :disabled="market.busyId === p.id"
              class="btn btn-sm btn-brand gap-1"
            ><Download :size="14" /> {{ market.busyId === p.id ? '更新中…' : `更新到 v${p.version}` }}</button>
            <span
              v-else-if="p.installed"
              class="app-chip bg-success/12 text-success"
            ><CircleCheck :size="12" /> 已安装</span>
            <button
              v-else
              @click="install(p.id, p.name)"
              :disabled="market.busyId === p.id"
              class="btn btn-sm btn-brand gap-1"
            ><Download :size="14" /> {{ market.busyId === p.id ? '安装中…' : '安装' }}</button>
          </div>
        </div>
      </div>
    </main>
  </div>
</template>
