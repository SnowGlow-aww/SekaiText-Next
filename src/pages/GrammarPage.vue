<script setup lang="ts">
import { ref, onMounted, onActivated, watch } from 'vue'
import { Search, SearchX, Languages } from 'lucide-vue-next'
import { useGlossaryStore } from '../stores/glossary'
import AppPageHeader from '../components/ui/AppPageHeader.vue'

const glossary = useGlossaryStore()

const query = ref('')
let debounceTimer: ReturnType<typeof setTimeout> | null = null

watch(query, () => {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => glossary.searchGrammar(query.value, query.value.trim() ? 0 : 200), 200)
})

function reload() {
  // Re-run the current query (or load the first 200) so freshly imported
  // grammar shows up. Runs on every (re)entry, not just first mount, because
  // this page is kept alive — importing on the glossary page then coming here
  // would otherwise show a stale empty list.
  glossary.searchGrammar(query.value, query.value.trim() ? 0 : 200)
}

onMounted(reload)
onActivated(reload)
</script>

<template>
  <div class="h-full min-h-0 overflow-y-auto page-bg text-[var(--color-text)]">
    <AppPageHeader title="语法用例" subtitle="从团队语料中快速查找接续与例句" width="3xl" />

    <main class="max-w-3xl mx-auto px-6 py-8 space-y-6">
      <section class="app-card p-5">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-primary/12 text-primary"><Languages :size="15" /></span>
          <div class="section-title">
            用例索引
            <span v-if="glossary.grammar.length" class="text-[var(--color-text-tertiary)] font-normal">· {{ glossary.grammar.length }}</span>
          </div>
        </div>

        <div class="relative mb-4">
          <Search :size="16" class="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-text-tertiary)] pointer-events-none" />
          <input
            v-model="query"
            type="text"
            placeholder="搜索语法项目 / 接续 / 例句"
            class="app-input pl-9"
          />
        </div>

        <div v-if="glossary.grammarLoading" class="flex items-center justify-center gap-2 py-12 text-sm text-[var(--color-text-secondary)]">
          <span class="loading loading-spinner loading-sm" /> 加载中…
        </div>
        <div v-else-if="glossary.grammar.length === 0" class="app-empty-state min-h-48">
          <span class="grid place-items-center w-11 h-11 rounded-2xl bg-primary/10 text-primary mb-3"><SearchX :size="22" /></span>
          <strong class="text-sm font-semibold text-[var(--color-text)]">没有匹配的语法</strong>
          <span class="app-help mt-1.5">试试语法项目、接续形式或例句中的关键词。</span>
        </div>
        <ul v-else class="space-y-2">
          <li
            v-for="g in glossary.grammar"
            :key="g.id"
            class="rounded-[var(--radius-control)] border border-[var(--color-border)] bg-[var(--color-bg)] px-4 py-3 transition-colors hover:border-[var(--color-border-strong)]"
          >
            <div class="flex items-baseline gap-2 flex-wrap">
              <span class="text-sm font-semibold text-primary">{{ g.item }}</span>
              <span v-if="g.connection" class="text-xs text-[var(--color-text-secondary)]">{{ g.connection }}</span>
              <span v-if="g.unit" class="app-chip bg-[color-mix(in_oklch,var(--color-base-content)_8%,transparent)] text-[var(--color-text-secondary)] ml-auto">{{ g.unit }}</span>
            </div>
            <div v-if="g.example" class="text-sm leading-relaxed mt-2 whitespace-pre-wrap">{{ g.example }}</div>
            <div v-if="g.reference" class="text-xs text-[var(--color-text-secondary)] mt-1.5 whitespace-pre-wrap">参考：{{ g.reference }}</div>
            <div class="flex items-center gap-2 mt-1.5">
              <span v-if="g.note" class="text-xs text-[var(--color-text-secondary)]">{{ g.note }}</span>
              <span v-if="g.location" class="text-[10px] text-[var(--color-text-tertiary)] ml-auto">{{ g.location }}</span>
            </div>
          </li>
        </ul>
      </section>
    </main>
  </div>
</template>
