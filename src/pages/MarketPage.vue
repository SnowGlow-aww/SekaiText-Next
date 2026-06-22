<script setup lang="ts">
import { onMounted, computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowLeft, RefreshCw, Search, Package } from 'lucide-vue-next'
import { useMarketStore } from '../stores/market'
import { useToast } from '../composables/useToast'

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
  <div class="h-full flex flex-col bg-[var(--color-bg)]">
    <header class="border-b border-[var(--color-border)] bg-[var(--color-surface)] px-6 py-3 flex items-center justify-between">
      <div class="flex items-center gap-3">
        <button @click="router.push('/')" class="text-[var(--color-text-secondary)] hover:text-[var(--color-text)]">
          <ArrowLeft :size="18" />
        </button>
        <h1 class="text-base font-semibold">插件市场</h1>
      </div>
      <button
        @click="market.refresh()"
        :disabled="market.loading"
        class="flex items-center gap-1.5 text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]"
      >
        <RefreshCw :size="16" :class="{ 'animate-spin': market.loading }" /> 刷新
      </button>
    </header>

    <div class="px-6 py-3 border-b border-[var(--color-border)] bg-[var(--color-surface)]">
      <div class="relative max-w-md">
        <Search :size="16" class="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-text-secondary)]" />
        <input
          v-model="query"
          type="text"
          placeholder="搜索插件…"
          class="w-full pl-9 pr-3 py-2 text-sm rounded-lg bg-[var(--color-bg)] border border-[var(--color-border)] focus:outline-none focus:border-[var(--color-primary)]"
        />
      </div>
    </div>

    <main class="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full">
      <div v-if="market.loading" class="text-sm text-[var(--color-text-secondary)]">加载中…</div>
      <div v-else-if="market.error" class="text-sm text-red-500">
        {{ market.error }}
        <button @click="market.refresh()" class="ml-2 underline">重试</button>
      </div>
      <div v-else-if="filtered.length === 0" class="flex flex-col items-center justify-center py-16 text-[var(--color-text-secondary)]">
        <Package :size="40" class="mb-3 opacity-40" />
        <p class="text-sm">{{ query ? '没有匹配的插件' : '插件市场暂无内容' }}</p>
      </div>
      <div v-else class="grid gap-3 sm:grid-cols-2">
        <div
          v-for="p in filtered"
          :key="p.id"
          class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-xl p-4 flex flex-col"
        >
          <div class="flex items-start justify-between gap-2 mb-1">
            <div class="min-w-0">
              <h3 class="text-sm font-medium truncate">{{ p.name }}</h3>
              <p class="text-xs text-[var(--color-text-secondary)] mt-0.5">
                <span class="font-mono">v{{ p.version }}</span>
                <span v-if="p.author"> · {{ p.author }}</span>
              </p>
            </div>
          </div>
          <p class="text-xs text-[var(--color-text-secondary)] flex-1 line-clamp-3 mb-3">{{ p.description }}</p>
          <div class="flex items-center justify-between">
            <a
              v-if="p.homepage"
              :href="p.homepage"
              target="_blank"
              rel="noopener"
              class="text-xs text-[var(--color-text-secondary)] hover:underline"
            >主页</a>
            <span v-else />
            <button
              v-if="p.updateAvailable"
              @click="install(p.id, p.name)"
              :disabled="market.busyId === p.id"
              class="text-xs px-3 py-1.5 rounded-lg bg-[var(--color-primary)] text-white disabled:opacity-50"
            >{{ market.busyId === p.id ? '更新中…' : `更新到 v${p.version}` }}</button>
            <span
              v-else-if="p.installed"
              class="text-xs px-3 py-1.5 rounded-lg bg-[var(--color-bg)] border border-[var(--color-border)] text-[var(--color-text-secondary)]"
            >已安装</span>
            <button
              v-else
              @click="install(p.id, p.name)"
              :disabled="market.busyId === p.id"
              class="text-xs px-3 py-1.5 rounded-lg bg-[var(--color-primary)] text-white disabled:opacity-50"
            >{{ market.busyId === p.id ? '安装中…' : '安装' }}</button>
          </div>
        </div>
      </div>
    </main>
  </div>
</template>

