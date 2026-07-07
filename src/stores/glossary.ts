import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api/client'
import type {
  GlossaryEntry,
  CategoryCount,
  ImportReport,
  AppellationResult,
  GrammarUsage,
} from '../types/glossary'

export const useGlossaryStore = defineStore('glossary', () => {
  const results = ref<GlossaryEntry[]>([])
  const categories = ref<CategoryCount[]>([])
  const searching = ref(false)
  const lastReport = ref<ImportReport | null>(null)

  // appellation lookup state
  const speakers = ref<string[]>([])
  const targets = ref<string[]>([])

  // Monotonic token: overlapping searches (debounced watch + direct calls) can
  // resolve out of order, so only the latest request is allowed to write state.
  let searchSeq = 0
  async function search(q: string, category = '', limit = 50) {
    const seq = ++searchSeq
    searching.value = true
    try {
      const r = q.trim() ? await api.glossarySearch(q, category, limit) : []
      if (seq === searchSeq) results.value = r
    } finally {
      if (seq === searchSeq) searching.value = false
    }
  }

  async function fetchCategories() {
    categories.value = await api.glossaryCategories()
  }

  async function addEntry(entry: Partial<GlossaryEntry>) {
    const saved = await api.glossaryAddEntry(entry)
    await fetchCategories()
    await loadAllEntries(true) // refresh editor matcher cache
    return saved
  }

  async function updateEntry(id: string, entry: Partial<GlossaryEntry>) {
    const saved = await api.glossaryUpdateEntry(id, entry)
    await loadAllEntries(true) // refresh editor matcher cache
    return saved
  }

  async function deleteEntry(id: string) {
    await api.glossaryDeleteEntry(id)
    results.value = results.value.filter((e) => e.id !== id)
    await fetchCategories()
    await loadAllEntries(true) // refresh editor matcher cache
  }

  async function importExcel(srcPath: string) {
    const report = await api.glossaryImport(srcPath)
    lastReport.value = report
    await fetchCategories()
    await loadSpeakers()
    await loadAllEntries(true) // refresh matcher cache
    await searchGrammar('', 200) // refresh grammar list (same file)
    return report
  }

  async function syncRemote(remoteUrl: string) {
    const r = await api.glossarySync(remoteUrl)
    await fetchCategories()
    await loadSpeakers()
    await loadAllEntries(true)
    await searchGrammar('', 200)
    return r
  }

  async function loadSpeakers() {
    speakers.value = await api.glossaryAppellationSpeakers()
  }

  async function loadTargets(speaker: string) {
    targets.value = speaker ? await api.glossaryAppellationTargets(speaker) : []
  }

  async function lookupAppellation(speaker: string, target: string): Promise<AppellationResult> {
    return api.glossaryAppellationLookup(speaker, target)
  }

  async function saveAppellation(speaker: string, target: string, jp: string, cn: string) {
    return api.glossaryAppellationUpsert({ speaker, target, jp, cn })
  }

  // grammar (语法用例)
  const grammar = ref<GrammarUsage[]>([])
  const grammarLoading = ref(false)
  // Same out-of-order guard as `search`: the grammar page debounces but can still
  // fire overlapping requests, so only the latest one is allowed to write state.
  let grammarSeq = 0
  async function searchGrammar(q = '', limit = 0) {
    const seq = ++grammarSeq
    grammarLoading.value = true
    try {
      const r = await api.glossaryGrammar(q, limit)
      if (seq === grammarSeq) grammar.value = r
    } finally {
      if (seq === grammarSeq) grammarLoading.value = false
    }
  }

  // Full entry list cache for the editor matcher (loaded once, lazily).
  const allEntries = ref<GlossaryEntry[]>([])
  const allEntriesLoaded = ref(false)
  async function loadAllEntries(force = false) {
    if (allEntriesLoaded.value && !force) return allEntries.value
    const r = await api.glossaryEntries('', 0, 100000)
    allEntries.value = r.items
    allEntriesLoaded.value = true
    return allEntries.value
  }

  return {
    results, categories, searching, lastReport, speakers, targets,
    grammar, grammarLoading, allEntries, allEntriesLoaded,
    search, fetchCategories, addEntry, updateEntry, deleteEntry,
    importExcel, syncRemote, loadSpeakers, loadTargets, lookupAppellation, saveAppellation,
    searchGrammar, loadAllEntries,
  }
})
