<script setup lang="ts">
import { onMounted, computed, ref } from 'vue'
import { RefreshCw, Search, Package, Download, CircleCheck, ExternalLink } from 'lucide-vue-next'
import { useMarketStore } from '../stores/market'
import { useToast } from '../composables/useToast'
import { openExternal } from '../utils/openExternal'
import AppPageHeader from '../components/ui/AppPageHeader.vue'

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
      (p.author || '').toLowerCase().includes(q) ||
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
  <div class="h-full min-h-0 overflow-y-auto page-bg text-[var(--color-text)]">
    <AppPageHeader title="插件市场" subtitle="发现并管理 SekaiText 扩展" width="5xl">
      <button
        @click="market.refresh()"
        :disabled="market.loading"
        class="btn btn-sm btn-ghost border border-[var(--color-border)] gap-1.5"
      >
        <RefreshCw :size="15" :class="{ 'animate-spin': market.loading }" /> 刷新
      </button>
    </AppPageHeader>

    <main class="max-w-5xl mx-auto px-6 py-7 space-y-5">
      <div class="flex flex-col sm:flex-row sm:items-end justify-between gap-3">
        <div>
          <h2 class="section-title">
            可用扩展
            <span v-if="!market.loading && !market.error" class="font-normal text-[var(--color-text-tertiary)]">· {{ filtered.length }}</span>
          </h2>
          <p class="app-help mt-1">安装后即可在侧栏或设置页使用；更新会保留现有配置。</p>
        </div>
        <div class="relative w-full sm:w-80">
          <Search :size="16" class="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-text-tertiary)] pointer-events-none" />
          <input
            v-model="query"
            type="text"
            placeholder="按名称、作者或功能搜索"
            class="app-input pl-9"
          />
        </div>
      </div>

      <div v-if="market.loading" class="flex items-center justify-center gap-2 py-16 text-sm text-[var(--color-text-secondary)]">
        <span class="loading loading-spinner loading-sm" /> 加载中…
      </div>
      <div v-else-if="market.error" class="app-card p-5 flex items-center gap-3 text-sm text-error">
        <span class="flex-1">{{ market.error }}</span>
        <button @click="market.refresh()" class="btn btn-sm btn-ghost border border-[var(--color-border)] shrink-0">重试</button>
      </div>
      <div v-else-if="filtered.length === 0" class="app-empty-state">
        <span class="grid place-items-center w-12 h-12 rounded-2xl bg-primary/10 text-primary mb-3"><Package :size="24" /></span>
        <strong class="text-sm font-semibold text-[var(--color-text)]">{{ query ? '没有找到匹配项' : '暂时没有可用扩展' }}</strong>
        <p class="app-help mt-1.5">{{ query ? '试试更短的关键词，或清空搜索条件。' : '稍后刷新页面即可查看新插件。' }}</p>
      </div>
      <div v-else class="grid gap-3 sm:grid-cols-2">
        <div
          v-for="p in filtered"
          :key="p.id"
          class="app-card app-card-hover p-4 flex flex-col"
        >
          <div class="flex items-start gap-3 mb-2.5">
            <span class="grid place-items-center w-9 h-9 rounded-xl bg-primary/10 text-primary shrink-0"><Package :size="18" /></span>
            <div class="min-w-0">
              <h3 class="text-sm font-semibold truncate">{{ p.name }}</h3>
              <p class="text-xs text-[var(--color-text-secondary)] mt-0.5">
                <span class="font-mono">v{{ p.version }}</span>
                <span v-if="p.author"> · {{ p.author }}</span>
              </p>
            </div>
          </div>
          <p class="text-xs leading-relaxed text-[var(--color-text-secondary)] flex-1 line-clamp-3 mb-3">{{ p.description || '暂无插件说明' }}</p>
          <p v-if="!p.signatureVerified" class="text-xs leading-relaxed text-warning mb-3">
            {{ p.signatureError || '官方签名不可用，已禁止安装' }}
          </p>
          <div class="flex items-center justify-between gap-2">
            <!-- Tauri webview 不处理 target=_blank，必须经 openExternal 调系统浏览器 -->
            <button
              v-if="p.homepage"
              @click="openExternal(p.homepage)"
              class="text-xs text-[var(--color-text-secondary)] hover:text-[var(--color-text)] inline-flex items-center gap-1 transition-colors"
            ><ExternalLink :size="12" /> 主页</button>
            <span v-else />
            <button
              v-if="p.updateAvailable || p.reinstallAvailable"
              @click="install(p.id, p.name)"
              :disabled="market.busyId === p.id || !p.signatureVerified"
              class="btn btn-sm btn-brand gap-1"
            ><Download :size="14" /> {{ market.busyId === p.id ? '安装中…' : p.updateAvailable ? `更新到 v${p.version}` : '重新验证并安装' }}</button>
            <span
              v-else-if="p.installed"
              class="app-chip bg-success/12 text-success"
            ><CircleCheck :size="12" /> 已安装</span>
            <button
              v-else
              @click="install(p.id, p.name)"
              :disabled="market.busyId === p.id || !p.signatureVerified"
              class="btn btn-sm btn-brand gap-1"
            ><Download :size="14" /> {{ market.busyId === p.id ? '安装中…' : '安装' }}</button>
          </div>
        </div>
      </div>
    </main>
  </div>
</template>
