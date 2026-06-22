<script setup lang="ts">
import { ref, onMounted, onActivated, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowLeft, Search } from 'lucide-vue-next'
import { useGlossaryStore } from '../stores/glossary'

const router = useRouter()
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
  <div class="min-h-screen bg-[var(--color-bg)]">
    <header class="border-b border-[var(--color-border)] bg-[var(--color-surface)] px-6 py-3 flex items-center justify-between">
      <div class="flex items-center gap-4">
        <button
          @click="router.push('/')"
          class="flex items-center gap-1.5 text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-text)] transition-colors"
        >
          <ArrowLeft :size="18" />
          返回编辑器
        </button>
        <span class="text-sm font-medium">语法用例</span>
      </div>
    </header>

    <main class="max-w-3xl mx-auto p-6 space-y-4">
      <div class="relative">
        <Search :size="16" class="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-text-secondary)]" />
        <input
          v-model="query"
          type="text"
          placeholder="搜索语法项目 / 接续 / 例句"
          class="w-full pl-9 pr-3 py-2 rounded-lg bg-[var(--color-surface)] border border-[var(--color-border)] text-sm focus:outline-none focus:border-[var(--color-primary)]"
        />
      </div>

      <div v-if="glossary.grammarLoading" class="text-sm text-[var(--color-text-secondary)] py-8 text-center">加载中…</div>
      <div v-else-if="glossary.grammar.length === 0" class="text-sm text-[var(--color-text-secondary)] py-8 text-center">没有匹配的语法</div>
      <ul v-else class="space-y-2">
        <li
          v-for="g in glossary.grammar"
          :key="g.id"
          class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-lg px-4 py-3"
        >
          <div class="flex items-baseline gap-2 flex-wrap">
            <span class="text-sm font-semibold text-[var(--color-primary)]">{{ g.item }}</span>
            <span v-if="g.connection" class="text-xs text-[var(--color-text-secondary)]">{{ g.connection }}</span>
            <span v-if="g.unit" class="text-[10px] px-1.5 py-0.5 rounded bg-[var(--color-bg)] text-[var(--color-text-secondary)] ml-auto">{{ g.unit }}</span>
          </div>
          <div v-if="g.example" class="text-sm leading-relaxed mt-2 whitespace-pre-wrap">{{ g.example }}</div>
          <div v-if="g.reference" class="text-xs text-[var(--color-text-secondary)] mt-1.5 whitespace-pre-wrap">参考：{{ g.reference }}</div>
          <div class="flex items-center gap-2 mt-1.5">
            <span v-if="g.note" class="text-xs text-[var(--color-text-secondary)]">{{ g.note }}</span>
            <span v-if="g.location" class="text-[10px] text-[var(--color-text-secondary)] ml-auto">{{ g.location }}</span>
          </div>
        </li>
      </ul>
    </main>
  </div>
</template>
