<script setup lang="ts">
import { ref, onMounted, watch, computed, useTemplateRef } from 'vue'
import { useRouter } from 'vue-router'
import { useVirtualizer } from '@tanstack/vue-virtual'
import { ArrowLeft, Search, Upload, Plus, Trash2, Pencil, Check, X, Download, Lock, Unlock } from 'lucide-vue-next'
import { useGlossaryStore } from '../stores/glossary'
import { useTeamStore } from '../stores/team'
import { useToast } from '../composables/useToast'
import { api } from '../api/client'
import TeamProposalsPanel from '../components/glossary/TeamProposalsPanel.vue'
import type { GlossaryEntry } from '../types/glossary'

const router = useRouter()
const glossary = useGlossaryStore()
const team = useTeamStore()
const toast = useToast()
const isTauri = typeof window !== 'undefined' && !!(window as any).__TAURI_INTERNALS__

const tab = ref<'search' | 'appellation'>('search')
const showProposals = ref(false)

// --- search tab ---
const query = ref('')
const category = ref('')
let debounceTimer: ReturnType<typeof setTimeout> | null = null

watch([query, category], () => {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => glossary.search(query.value, category.value), 200)
})

// --- browse all (virtual scroll): when no query but a category is picked,
// show every entry in that category instead of requiring a search term. ---
const browseEntries = ref<GlossaryEntry[]>([])
const browsing = computed(() => !query.value.trim() && !!category.value)

watch([category, query], async () => {
  if (!query.value.trim() && category.value) {
    const r = await api.glossaryEntries(category.value, 0, 100000)
    browseEntries.value = r.items
  } else {
    browseEntries.value = []
  }
})

const scrollParent = useTemplateRef<HTMLElement>('scrollParent')
const rowVirtualizer = useVirtualizer(computed(() => ({
  count: browseEntries.value.length,
  getScrollElement: () => scrollParent.value,
  estimateSize: () => 76,
  overscan: 8,
  // Stable per-entry key so dynamic measureElement caches survive list changes
  // (e.g. after a delete reflows the rows).
  getItemKey: (index: number) => browseEntries.value[index]?.id ?? index,
})))

// vue-virtual's measureElement ref callback, wrapped so its element-only signature
// satisfies Vue's :ref VNodeRef type. Lets a row that expands into an inline edit
// form remeasure and reflow the rows below it.
function measureRow(el: any) {
  rowVirtualizer.value.measureElement(el)
}

// Refresh whatever the search tab is currently showing — the search results
// when a query is present, the browse list when only a category is chosen.
// Called after a local add/edit/delete so the change shows immediately
// (glossary.deleteEntry only filters `results`, never `browseEntries`).
async function refreshView() {
  await glossary.search(query.value, category.value)
  if (!query.value.trim() && category.value) {
    const r = await api.glossaryEntries(category.value, 0, 100000)
    browseEntries.value = r.items
  }
}

// --- export ---
const exporting = ref(false)
async function handleExport() {
  exporting.value = true
  try {
    const data = await api.glossaryExport()
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'glossary.json'
    a.click()
    URL.revokeObjectURL(url)
    toast.show('已导出 glossary.json', 'success')
  } catch (e: any) {
    toast.show(e.message || '导出失败', 'error')
  } finally {
    exporting.value = false
  }
}

// --- add / edit ---
// editUnlocked: edits to existing entries (inline edit + delete) are locked by
// default to prevent accidental changes; the lock toggle lives top-right by
// import/export. Adding new entries is always allowed.
const editUnlocked = ref(false)
const showAdd = ref(false)
const draft = ref<Partial<GlossaryEntry>>({ source: '', translation: '', note: '', category: '' })
const editingId = ref<string | null>(null)
const editDraft = ref<Partial<GlossaryEntry>>({})

// When the edit/delete controls show:
//   readonly   → never (view-only mode)
//   logged-in  → always (local entries delete directly; remote entries route to a proposal)
//   pure-local → only when the lock is unlocked (guards against accidental edits)
const canShowControls = computed(() => !team.readonly && (editUnlocked.value || team.loggedIn))

function toggleEditLock() {
  editUnlocked.value = !editUnlocked.value
  if (!editUnlocked.value) editingId.value = null // closing lock cancels any open edit
  toast.show(editUnlocked.value ? '已解锁编辑，注意避免误改' : '已锁定编辑', editUnlocked.value ? 'warn' : 'info')
}

async function submitAdd() {
  if (team.readonly) { toast.show('只读模式：登录后才能新增', 'warn'); return }
  if (!draft.value.source?.trim() || !draft.value.translation?.trim()) {
    toast.show('原文和译文都要填', 'warn'); return
  }
  try {
    if (team.loggedIn) {
      await team.submitProposal({
        kind: 'add',
        category: draft.value.category || '自定义',
        payload: { ...draft.value, category: draft.value.category || '自定义' },
      })
      toast.show('已提交新增提案，待管理员审核', 'success')
    } else {
      await glossary.addEntry({ ...draft.value, category: draft.value.category || '自定义' })
      toast.show('已添加', 'success')
    }
    draft.value = { source: '', translation: '', note: '', category: '' }
    showAdd.value = false
    await refreshView()
  } catch (e: any) { toast.show(e.message || '操作失败', 'error') }
}

function startEdit(e: GlossaryEntry) {
  editingId.value = e.id
  editDraft.value = { source: e.source, translation: e.translation, note: e.note, aliases: e.aliases, category: e.category, version: e.version }
}

async function saveEdit(id: string) {
  if (team.readonly) { toast.show('只读模式：登录后才能修改', 'warn'); return }
  const target = [...glossary.results, ...browseEntries.value].find(x => x.id === id)
  // Remote (server) entries: route edits through the team proposal queue.
  if (team.loggedIn && target?.origin === 'remote') {
    try {
      await team.submitProposal({
        kind: 'edit',
        targetId: id,
        category: editDraft.value.category || target?.category || '自定义',
        payload: { ...editDraft.value, id, category: editDraft.value.category || target?.category },
        baseVersion: target?.version,
      })
      editingId.value = null
      toast.show('已提交修改提案，待管理员审核', 'success')
    } catch (e: any) { toast.show(e.message || '提交失败', 'error') }
    return
  }
  // Local entries (import/user) — edit directly, even when logged in.
  if (!confirm('确定保存对这条词条的修改吗？')) return
  try {
    await glossary.updateEntry(id, editDraft.value)
    editingId.value = null
    await refreshView()
    toast.show('已保存', 'success')
  } catch (e: any) { toast.show(e.message || '保存失败', 'error') }
}

async function removeEntry(id: string) {
  if (team.readonly) { toast.show('只读模式：登录后才能删除', 'warn'); return }
  const e = [...glossary.results, ...browseEntries.value].find(x => x.id === id)
  // Remote (server) entries: a local delete would be reverted by the next 60s
  // sync, so route them through the team delete-proposal queue (admin self-approves).
  if (team.loggedIn && e?.origin === 'remote') {
    if (!confirm(`确定提交删除「${e?.source ?? ''}」的提案吗？需管理员审核通过后生效。`)) return
    try {
      await team.submitProposal({
        kind: 'delete', targetId: id, category: e?.category || '自定义', payload: { id },
      })
      toast.show('已提交删除提案，待管理员审核', 'success')
    } catch (err: any) { toast.show(err.message || '提交失败', 'error') }
    return
  }
  // Local entries (import/user) — delete directly, even when logged in.
  if (!confirm(`确定删除「${e?.source ?? ''}」这条词条吗？此操作不可撤销。`)) return
  try { await glossary.deleteEntry(id); await refreshView(); toast.show('已删除', 'success') }
  catch (err: any) { toast.show(err.message || '删除失败', 'error') }
}

// --- import Excel ---
const importing = ref(false)

async function handleImport() {
  if (!isTauri) {
    toast.show('导入需要桌面版（浏览器预览不支持选择本地 xlsx 路径）', 'warn')
    return
  }
  const { open } = await import('@tauri-apps/plugin-dialog')
  const path = await open({
    title: '选择术语库 Excel',
    filters: [{ name: 'Excel', extensions: ['xlsx'] }],
  })
  if (!path) return
  importing.value = true
  try {
    const report = await glossary.importExcel(path as string)
    const ok = report.sheets.filter(s => s.kind !== 'skipped').map(s => `${s.sheet}(${s.count})`).join('、')
    toast.show(`导入完成：${report.totalEntries} 词条 / ${report.totalAppellations} 称呼 / ${report.totalGrammar} 语法 — ${ok}`, 'success', 6000)
    await glossary.search(query.value, category.value)
  } catch (e: any) {
    toast.show(e.message || '导入失败', 'error')
  } finally {
    importing.value = false
  }
}

// --- appellation tab ---
const speaker = ref('')
const target = ref('')
const result = ref<{ jp?: string; cn?: string; found: boolean } | null>(null)
const editingAppell = ref(false)
const appellDraft = ref({ jp: '', cn: '' })

// matrix view: speaker (rows) × target (cols) grid, click a cell to edit.
const appellMode = ref<'lookup' | 'matrix'>('lookup')
const matrixData = ref<import('../types/glossary').Appellation[]>([])
const matrixSpeakers = ref<string[]>([])
const matrixTargets = ref<string[]>([])
const matrixCell = ref<Record<string, { jp?: string; cn?: string }>>({})
const editingCell = ref<string | null>(null)
const cellDraft = ref({ jp: '', cn: '' })

function cellKey(s: string, t: string) { return `${s}\x00${t}` }

async function loadMatrix() {
  // The export payload carries the full appellation list; reuse it.
  const data = await api.glossaryExport()
  const aps = data.appellations || []
  matrixData.value = aps
  const sSeen = new Set<string>(), tSeen = new Set<string>()
  const cells: Record<string, { jp?: string; cn?: string }> = {}
  for (const a of aps) {
    sSeen.add(a.speaker)
    tSeen.add(a.target)
    cells[cellKey(a.speaker, a.target)] = { jp: a.jp, cn: a.cn }
  }
  matrixSpeakers.value = [...sSeen]
  matrixTargets.value = [...tSeen]
  matrixCell.value = cells
}

watch(appellMode, (m) => { if (m === 'matrix' && matrixData.value.length === 0) loadMatrix() })

function startEditCell(s: string, t: string) {
  editingCell.value = cellKey(s, t)
  const c = matrixCell.value[cellKey(s, t)]
  cellDraft.value = { jp: c?.jp || '', cn: c?.cn || '' }
}

async function saveCell(s: string, t: string) {
  if (!confirm(`确定保存「${s} → ${t}」的称呼修改吗？`)) return
  try {
    await glossary.saveAppellation(s, t, cellDraft.value.jp, cellDraft.value.cn)
    matrixCell.value[cellKey(s, t)] = { jp: cellDraft.value.jp, cn: cellDraft.value.cn }
    editingCell.value = null
    toast.show('已保存', 'success')
  } catch (e: any) { toast.show(e.message || '保存失败', 'error') }
}

watch(speaker, async (s) => {
  target.value = ''
  result.value = null
  await glossary.loadTargets(s)
})

watch([speaker, target], async () => {
  if (speaker.value && target.value) {
    result.value = await glossary.lookupAppellation(speaker.value, target.value)
  } else {
    result.value = null
  }
})

function startEditAppell() {
  editingAppell.value = true
  appellDraft.value = { jp: result.value?.jp || '', cn: result.value?.cn || '' }
}

async function saveAppell() {
  if (!confirm('确定保存对这条称呼的修改吗？')) return
  try {
    await glossary.saveAppellation(speaker.value, target.value, appellDraft.value.jp, appellDraft.value.cn)
    result.value = { found: true, jp: appellDraft.value.jp, cn: appellDraft.value.cn }
    editingAppell.value = false
    toast.show('已保存', 'success')
  } catch (e: any) { toast.show(e.message || '保存失败', 'error') }
}

onMounted(async () => {
  await glossary.fetchCategories()
  await glossary.loadSpeakers()
})
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
        <span class="text-sm font-medium">术语库</span>
      </div>
      <div class="flex items-center gap-2">
        <button
          v-if="!team.readonly"
          @click="toggleEditLock"
          :class="['flex items-center gap-1.5 text-sm px-3 py-1.5 rounded-lg border transition-colors', editUnlocked ? 'border-amber-400 text-amber-500 bg-amber-400/10' : 'border-[var(--color-border)] text-[var(--color-text-secondary)] hover:bg-[var(--color-bg)]']"
          :title="editUnlocked ? '编辑已解锁，点击重新锁定' : '编辑已锁定，点击解锁以修改/删除词条'"
        >
          <component :is="editUnlocked ? Unlock : Lock" :size="16" />
          {{ editUnlocked ? '编辑中' : '锁定' }}
        </button>
        <button
          @click="handleExport"
          :disabled="exporting"
          class="flex items-center gap-1.5 text-sm px-3 py-1.5 rounded-lg border border-[var(--color-border)] hover:bg-[var(--color-bg)] transition-colors disabled:opacity-50"
        >
          <Download :size="16" />
          {{ exporting ? '导出中…' : '导出' }}
        </button>
        <button
          v-if="tab === 'search'"
          @click="handleImport"
          :disabled="importing"
          class="flex items-center gap-1.5 text-sm px-3 py-1.5 rounded-lg border border-[var(--color-border)] hover:bg-[var(--color-bg)] transition-colors disabled:opacity-50"
        >
          <Upload :size="16" />
          {{ importing ? '导入中…' : '导入 Excel' }}
        </button>
      </div>
    </header>

    <div class="max-w-6xl mx-auto px-6 pt-4">
      <div class="flex gap-1 border-b border-[var(--color-border)]">
        <button
          @click="tab = 'search'"
          :class="['px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors', tab === 'search' ? 'border-[var(--color-primary)] text-[var(--color-text)]' : 'border-transparent text-[var(--color-text-secondary)] hover:text-[var(--color-text)]']"
        >术语检索</button>
        <button
          @click="tab = 'appellation'"
          :class="['px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors', tab === 'appellation' ? 'border-[var(--color-primary)] text-[var(--color-text)]' : 'border-transparent text-[var(--color-text-secondary)] hover:text-[var(--color-text)]']"
        >称呼查询</button>
      </div>
    </div>

    <main class="max-w-6xl mx-auto p-6">
      <!-- search tab -->
      <div v-show="tab === 'search'" class="space-y-4">
        <!-- 团队模式:提案/审核入口 -->
        <div v-if="team.loggedIn">
          <button @click="showProposals = !showProposals"
            class="flex items-center gap-2 text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-text)]">
            <span class="px-1.5 py-0.5 rounded text-[10px] bg-[var(--color-primary)]/15 text-[var(--color-primary)]">团队模式</span>
            {{ showProposals ? '收起提案面板' : '我的提案 / 审核' }}
          </button>
          <div v-if="showProposals" class="mt-2">
            <TeamProposalsPanel />
          </div>
        </div>
        <!-- 只读模式提示 -->
        <div v-else-if="team.readonly" class="flex items-center gap-2 text-xs text-[var(--color-text-secondary)] bg-[var(--color-surface)] border border-[var(--color-border)] rounded-lg px-3 py-2">
          <span class="px-1.5 py-0.5 rounded text-[10px] bg-amber-400/15 text-amber-500">只读模式</span>
          正在从服务器同步术语库，登录后才能新增/修改。
        </div>
        <div class="flex gap-2">
          <div class="relative flex-1">
            <Search :size="16" class="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-text-secondary)]" />
            <input
              v-model="query"
              type="text"
              placeholder="输入原文或译文（日⇄中双向）"
              class="w-full pl-9 pr-3 py-2 rounded-lg bg-[var(--color-surface)] border border-[var(--color-border)] text-sm focus:outline-none focus:border-[var(--color-primary)]"
            />
          </div>
          <select
            v-model="category"
            class="px-3 py-2 rounded-lg bg-[var(--color-surface)] border border-[var(--color-border)] text-sm focus:outline-none focus:border-[var(--color-primary)]"
          >
            <option value="">全部分类</option>
            <option v-for="c in glossary.categories" :key="c.category" :value="c.category">
              {{ c.category }} ({{ c.count }})
            </option>
          </select>
          <button
            v-if="!team.readonly"
            @click="showAdd = !showAdd"
            class="flex items-center gap-1 px-3 py-2 rounded-lg border border-[var(--color-border)] text-sm hover:bg-[var(--color-surface)] transition-colors"
          >
            <Plus :size="16" /> 添加
          </button>
        </div>

        <!-- shared category suggestions for the add / edit forms (always in the DOM
             so the edit forms can reference it even when the add form is closed) -->
        <datalist id="glossary-cats">
          <option v-for="c in glossary.categories" :key="c.category" :value="c.category" />
        </datalist>

        <!-- add form -->
        <div v-if="showAdd" class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-xl p-4 space-y-3">
          <div class="grid grid-cols-2 gap-3">
            <input v-model="draft.source" placeholder="原文" class="px-3 py-2 rounded-lg bg-[var(--color-bg)] border border-[var(--color-border)] text-sm focus:outline-none focus:border-[var(--color-primary)]" />
            <input v-model="draft.translation" placeholder="译文" class="px-3 py-2 rounded-lg bg-[var(--color-bg)] border border-[var(--color-border)] text-sm focus:outline-none focus:border-[var(--color-primary)]" />
          </div>
          <div class="grid grid-cols-2 gap-3">
            <input v-model="draft.category" list="glossary-cats" placeholder="分类（可选，可新建，默认自定义）" class="px-3 py-2 rounded-lg bg-[var(--color-bg)] border border-[var(--color-border)] text-sm focus:outline-none focus:border-[var(--color-primary)]" />
            <input v-model="draft.note" placeholder="备注（可选）" class="px-3 py-2 rounded-lg bg-[var(--color-bg)] border border-[var(--color-border)] text-sm focus:outline-none focus:border-[var(--color-primary)]" />
          </div>
          <div class="flex justify-end gap-2">
            <button @click="showAdd = false" class="px-3 py-1.5 rounded-lg text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-text)]">取消</button>
            <button @click="submitAdd" class="px-3 py-1.5 rounded-lg text-sm bg-[var(--color-primary)] text-white hover:opacity-90">保存</button>
          </div>
        </div>

        <!-- results -->
        <div v-if="glossary.searching" class="text-sm text-[var(--color-text-secondary)] py-8 text-center">搜索中…</div>
        <!-- browse all (virtual scroll) when no query but a category is chosen -->
        <template v-else-if="browsing">
          <div class="text-xs text-[var(--color-text-secondary)]">{{ category }} · 共 {{ browseEntries.length }} 条</div>
          <div ref="scrollParent" class="overflow-auto" style="height: calc(100vh - 240px)">
            <div :style="{ height: rowVirtualizer.getTotalSize() + 'px', position: 'relative', width: '100%' }">
              <div
                v-for="vr in rowVirtualizer.getVirtualItems()"
                :key="browseEntries[vr.index]?.id ?? vr.index"
                :data-index="vr.index"
                :ref="measureRow"
                :style="{ position: 'absolute', top: 0, left: 0, width: '100%', transform: `translateY(${vr.start}px)` }"
                class="pb-2"
              >
                <div class="group bg-[var(--color-surface)] border border-[var(--color-border)] rounded-lg px-4 py-3">
                  <!-- inline edit (browse mode) -->
                  <template v-if="editingId === browseEntries[vr.index].id">
                    <div class="grid grid-cols-2 gap-2 mb-2">
                      <input v-model="editDraft.source" class="px-2 py-1 rounded bg-[var(--color-bg)] border border-[var(--color-border)] text-sm" />
                      <input v-model="editDraft.translation" class="px-2 py-1 rounded bg-[var(--color-bg)] border border-[var(--color-border)] text-sm" />
                    </div>
                    <input v-model="editDraft.category" list="glossary-cats" placeholder="分类" class="w-full px-2 py-1 rounded bg-[var(--color-bg)] border border-[var(--color-border)] text-sm mb-2" />
                    <input v-model="editDraft.note" placeholder="备注" class="w-full px-2 py-1 rounded bg-[var(--color-bg)] border border-[var(--color-border)] text-sm mb-2" />
                    <div class="flex justify-end gap-2">
                      <button @click="editingId = null" class="flex items-center gap-1 text-sm text-[var(--color-text-secondary)]"><X :size="14" />取消</button>
                      <button @click="saveEdit(browseEntries[vr.index].id)" class="flex items-center gap-1 text-sm text-[var(--color-primary)]"><Check :size="14" />保存</button>
                    </div>
                  </template>
                  <template v-else>
                    <div class="flex items-start justify-between gap-3">
                      <div class="min-w-0 flex-1">
                        <div class="flex items-baseline gap-2 flex-wrap">
                          <span class="text-sm font-medium">{{ browseEntries[vr.index].source }}</span>
                          <span class="text-[var(--color-text-secondary)]">→</span>
                          <span class="text-sm">{{ browseEntries[vr.index].translation }}</span>
                        </div>
                        <div v-if="browseEntries[vr.index].aliases?.length" class="text-xs text-[var(--color-text-secondary)] mt-1">别称：{{ browseEntries[vr.index].aliases!.join('、') }}</div>
                        <div v-if="browseEntries[vr.index].note" class="text-xs text-[var(--color-text-secondary)] mt-1 whitespace-pre-wrap">{{ browseEntries[vr.index].note }}</div>
                        <div v-if="browseEntries[vr.index].subCategory" class="text-[10px] text-[var(--color-text-secondary)] mt-1">{{ browseEntries[vr.index].subCategory }}</div>
                      </div>
                      <div v-if="canShowControls" class="flex items-center gap-1 md:opacity-0 md:group-hover:opacity-100 transition-opacity">
                        <button @click="startEdit(browseEntries[vr.index])" class="p-1.5 rounded hover:bg-[var(--color-bg)] text-[var(--color-text-secondary)]"><Pencil :size="14" /></button>
                        <button @click="removeEntry(browseEntries[vr.index].id)" class="p-1.5 rounded hover:bg-[var(--color-bg)] text-[var(--color-text-secondary)] hover:text-red-500"><Trash2 :size="14" /></button>
                      </div>
                    </div>
                  </template>
                </div>
              </div>
            </div>
          </div>
        </template>
        <div v-else-if="query && glossary.results.length === 0" class="text-sm text-[var(--color-text-secondary)] py-8 text-center">没有匹配的词条</div>
        <div v-else-if="!query" class="text-sm text-[var(--color-text-secondary)] py-8 text-center">输入关键词检索，或选一个分类浏览全部</div>
        <ul v-else class="grid grid-cols-1 md:grid-cols-2 gap-2">
          <li
            v-for="e in glossary.results"
            :key="e.id"
            class="group bg-[var(--color-surface)] border border-[var(--color-border)] rounded-lg px-4 py-3"
          >
            <template v-if="editingId === e.id">
              <div class="grid grid-cols-2 gap-2 mb-2">
                <input v-model="editDraft.source" class="px-2 py-1 rounded bg-[var(--color-bg)] border border-[var(--color-border)] text-sm" />
                <input v-model="editDraft.translation" class="px-2 py-1 rounded bg-[var(--color-bg)] border border-[var(--color-border)] text-sm" />
              </div>
              <input v-model="editDraft.category" list="glossary-cats" placeholder="分类" class="w-full px-2 py-1 rounded bg-[var(--color-bg)] border border-[var(--color-border)] text-sm mb-2" />
              <input v-model="editDraft.note" placeholder="备注" class="w-full px-2 py-1 rounded bg-[var(--color-bg)] border border-[var(--color-border)] text-sm mb-2" />
              <div class="flex justify-end gap-2">
                <button @click="editingId = null" class="flex items-center gap-1 text-sm text-[var(--color-text-secondary)]"><X :size="14" />取消</button>
                <button @click="saveEdit(e.id)" class="flex items-center gap-1 text-sm text-[var(--color-primary)]"><Check :size="14" />保存</button>
              </div>
            </template>
            <template v-else>
              <div class="flex items-start justify-between gap-3">
                <div class="min-w-0 flex-1">
                  <div class="flex items-baseline gap-2 flex-wrap">
                    <span class="text-sm font-medium">{{ e.source }}</span>
                    <span class="text-[var(--color-text-secondary)]">→</span>
                    <span class="text-sm">{{ e.translation }}</span>
                  </div>
                  <div v-if="e.aliases?.length" class="text-xs text-[var(--color-text-secondary)] mt-1">别称：{{ e.aliases.join('、') }}</div>
                  <div v-if="e.note" class="text-xs text-[var(--color-text-secondary)] mt-1 whitespace-pre-wrap">{{ e.note }}</div>
                  <div class="flex items-center gap-2 mt-1.5">
                    <span class="text-[10px] px-1.5 py-0.5 rounded bg-[var(--color-bg)] text-[var(--color-text-secondary)]">{{ e.category }}</span>
                    <span v-if="e.subCategory" class="text-[10px] text-[var(--color-text-secondary)]">{{ e.subCategory }}</span>
                    <span v-if="e.origin === 'user'" class="text-[10px] px-1.5 py-0.5 rounded bg-[var(--color-primary)]/10 text-[var(--color-primary)]">自添加</span>
                    <span v-if="e.contributorName" class="text-[10px] text-[var(--color-text-secondary)]">添加者：{{ e.contributorName }}</span>
                  </div>
                </div>
                <div v-if="canShowControls" class="flex items-center gap-1 md:opacity-0 md:group-hover:opacity-100 transition-opacity">
                  <button @click="startEdit(e)" class="p-1.5 rounded hover:bg-[var(--color-bg)] text-[var(--color-text-secondary)]"><Pencil :size="14" /></button>
                  <button @click="removeEntry(e.id)" class="p-1.5 rounded hover:bg-[var(--color-bg)] text-[var(--color-text-secondary)] hover:text-red-500"><Trash2 :size="14" /></button>
                </div>
              </div>
            </template>
          </li>
        </ul>
      </div>

      <!-- appellation tab -->
      <div v-show="tab === 'appellation'" class="space-y-4">
        <div class="flex items-center justify-between">
          <p class="text-sm text-[var(--color-text-secondary)]">
            {{ appellMode === 'lookup' ? '选「说话人」和「对象」，直接查出称呼（来自人称表）。' : (editUnlocked ? '说话人（行）× 对象（列），点格子就地编辑。' : '说话人（行）× 对象（列）。解锁编辑后可点格子修改。') }}
          </p>
          <div class="flex gap-1 text-xs">
            <button @click="appellMode = 'lookup'" :class="['px-2.5 py-1 rounded', appellMode === 'lookup' ? 'bg-[var(--color-primary)] text-white' : 'border border-[var(--color-border)] text-[var(--color-text-secondary)]']">逐对查询</button>
            <button @click="appellMode = 'matrix'" :class="['px-2.5 py-1 rounded', appellMode === 'matrix' ? 'bg-[var(--color-primary)] text-white' : 'border border-[var(--color-border)] text-[var(--color-text-secondary)]']">矩阵视图</button>
          </div>
        </div>

        <!-- matrix view -->
        <div v-if="appellMode === 'matrix'" class="overflow-auto border border-[var(--color-border)] rounded-lg" style="max-height: calc(100vh - 240px)">
          <table class="text-xs border-collapse">
            <thead>
              <tr>
                <th class="sticky top-0 left-0 z-20 bg-[var(--color-surface)] border border-[var(--color-border)] px-2 py-1.5 min-w-[80px]">说话人＼对象</th>
                <th v-for="t in matrixTargets" :key="t" class="sticky top-0 z-10 bg-[var(--color-surface)] border border-[var(--color-border)] px-2 py-1.5 whitespace-nowrap font-medium">{{ t }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="s in matrixSpeakers" :key="s">
                <th class="sticky left-0 z-10 bg-[var(--color-surface)] border border-[var(--color-border)] px-2 py-1.5 whitespace-nowrap font-medium text-left">{{ s }}</th>
                <td
                  v-for="t in matrixTargets"
                  :key="t"
                  :class="['border border-[var(--color-border)] px-2 py-1 align-top min-w-[64px]', editUnlocked ? 'cursor-pointer hover:bg-[var(--color-primary)]/5' : '']"
                  @click="editUnlocked && startEditCell(s, t)"
                >
                  <template v-if="editingCell === cellKey(s, t)">
                    <input v-model="cellDraft.jp" placeholder="日" class="w-full px-1 py-0.5 rounded bg-[var(--color-bg)] border border-[var(--color-border)] mb-1" @click.stop @keydown.enter="saveCell(s, t)" />
                    <input v-model="cellDraft.cn" placeholder="中" class="w-full px-1 py-0.5 rounded bg-[var(--color-bg)] border border-[var(--color-border)] mb-1" @click.stop @keydown.enter="saveCell(s, t)" />
                    <div class="flex gap-1 justify-end" @click.stop>
                      <button @click="editingCell = null" class="text-[var(--color-text-secondary)]"><X :size="12" /></button>
                      <button @click="saveCell(s, t)" class="text-[var(--color-primary)]"><Check :size="12" /></button>
                    </div>
                  </template>
                  <template v-else>
                    <div v-if="matrixCell[cellKey(s, t)]?.jp" class="whitespace-nowrap">{{ matrixCell[cellKey(s, t)]?.jp }}</div>
                    <div v-if="matrixCell[cellKey(s, t)]?.cn" class="whitespace-nowrap text-[var(--color-primary)]">{{ matrixCell[cellKey(s, t)]?.cn }}</div>
                  </template>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- lookup view -->
        <template v-else>
        <div class="flex items-center gap-3">
          <select
            v-model="speaker"
            class="flex-1 px-3 py-2 rounded-lg bg-[var(--color-surface)] border border-[var(--color-border)] text-sm focus:outline-none focus:border-[var(--color-primary)]"
          >
            <option value="">说话人 A</option>
            <option v-for="s in glossary.speakers" :key="s" :value="s">{{ s }}</option>
          </select>
          <span class="text-[var(--color-text-secondary)] text-sm">称呼</span>
          <select
            v-model="target"
            :disabled="!speaker"
            class="flex-1 px-3 py-2 rounded-lg bg-[var(--color-surface)] border border-[var(--color-border)] text-sm focus:outline-none focus:border-[var(--color-primary)] disabled:opacity-50"
          >
            <option value="">对象 B</option>
            <option v-for="t in glossary.targets" :key="t" :value="t">{{ t }}</option>
          </select>
        </div>

        <div v-if="result" class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-xl p-5">
          <template v-if="result.found && !editingAppell">
            <div class="text-sm text-[var(--color-text-secondary)] mb-2">
              <span class="text-[var(--color-text)] font-medium">{{ speaker }}</span> 称呼
              <span class="text-[var(--color-text)] font-medium">{{ target }}</span> 为：
            </div>
            <div class="flex items-baseline gap-4">
              <div v-if="result.jp"><span class="text-xs text-[var(--color-text-secondary)] mr-1">日</span><span class="text-lg font-medium">{{ result.jp }}</span></div>
              <div v-if="result.cn"><span class="text-xs text-[var(--color-text-secondary)] mr-1">中</span><span class="text-lg font-medium">{{ result.cn }}</span></div>
            </div>
            <button v-if="editUnlocked" @click="startEditAppell" class="flex items-center gap-1 text-xs text-[var(--color-text-secondary)] hover:text-[var(--color-text)] mt-3"><Pencil :size="12" />编辑</button>
          </template>
          <template v-else-if="editingAppell">
            <div class="grid grid-cols-2 gap-3 mb-3">
              <label class="text-xs text-[var(--color-text-secondary)]">日文<input v-model="appellDraft.jp" class="mt-1 w-full px-2 py-1.5 rounded bg-[var(--color-bg)] border border-[var(--color-border)] text-sm" /></label>
              <label class="text-xs text-[var(--color-text-secondary)]">中文<input v-model="appellDraft.cn" class="mt-1 w-full px-2 py-1.5 rounded bg-[var(--color-bg)] border border-[var(--color-border)] text-sm" /></label>
            </div>
            <div class="flex justify-end gap-2">
              <button @click="editingAppell = false" class="px-3 py-1.5 rounded-lg text-sm text-[var(--color-text-secondary)]">取消</button>
              <button @click="saveAppell" class="px-3 py-1.5 rounded-lg text-sm bg-[var(--color-primary)] text-white hover:opacity-90">保存</button>
            </div>
          </template>
          <template v-else>
            <div class="text-sm text-[var(--color-text-secondary)]">人称表里暂无这对组合的记录。<button v-if="editUnlocked" @click="startEditAppell" class="text-[var(--color-primary)] hover:underline ml-1">手动补充</button><span v-else class="opacity-60 ml-1">（解锁编辑后可补充）</span></div>
          </template>
        </div>
        <div v-else-if="speaker && target" class="text-sm text-[var(--color-text-secondary)] py-8 text-center">查询中…</div>
        </template>
      </div>
    </main>
  </div>
</template>

