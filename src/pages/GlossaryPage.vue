<script setup lang="ts">
import { ref, onMounted, onActivated, onDeactivated, watch, watchEffect, computed, useTemplateRef } from 'vue'
import { useRouter } from 'vue-router'
import { useVirtualizer } from '@tanstack/vue-virtual'
import { ArrowLeft, Search, Plus, Trash2, Pencil, Check, X, Lock, Unlock, UploadCloud, BookMarked } from 'lucide-vue-next'
import { useGlossaryStore } from '../stores/glossary'
import { useDictStore } from '../stores/dict'
import { useTeamStore } from '../stores/team'
import { useToast } from '../composables/useToast'
import { useConfirm } from '../composables/useConfirm'
import { api } from '../api/client'
import TeamProposalsPanel from '../components/glossary/TeamProposalsPanel.vue'
import SkSelect from '../components/ui/SkSelect.vue'
import type { GlossaryEntry, DictEntry } from '../types/glossary'
import { useGlossaryNotifyStore } from '../stores/glossaryNotify'
import { useTour } from '../onboarding/useTour'
import { glossaryTour } from '../onboarding/tours'

const router = useRouter()
const glossary = useGlossaryStore()
const dictStore = useDictStore()
const team = useTeamStore()
const toast = useToast()
const { confirm } = useConfirm()
const glossaryNotify = useGlossaryNotifyStore()
const tour = useTour()

// 打开术语库页 = 已读「我的提案已通过」，侧栏呼吸灯熄灭（待审核数不在此清零，
// 那盏灯审完才灭）。先轮询拿到最新水位再标记，避免把没看到的通过也标掉。
// 首次打开术语库页的分层功能导览：未登录先引导去账号中心登录；已登录只播
// 当前角色可见且没看过的层。被 promote（翻译→校对→管理员）后再进本页时，
// 单独补播新增权限那一层。startOnce 保证有别的导览在放时不插队。
const pageActive = ref(false)
function maybeStartGlossaryTour() {
  const def = glossaryTour({
    loggedIn: team.loggedIn,
    isReviewer: team.isReviewer,
    isAdmin: team.isAdmin,
    seen: tour.seen,
  })
  if (def) tour.startOnce(def)
}
// 在本页期间登录 / 被晋升也即时补播（守 pageActive：kept-alive 的 watcher 在
// 离开页面后仍会跑，不能在别的页面凭空弹导览）。
watch(() => [team.loggedIn, team.isReviewer, team.isAdmin], () => {
  if (pageActive.value) maybeStartGlossaryTour()
})
onDeactivated(() => { pageActive.value = false })

onActivated(async () => {
  pageActive.value = true
  maybeStartGlossaryTour()
  await glossaryNotify.poll()
  glossaryNotify.markApprovedSeen()
})

const tab = ref<'search' | 'appellation'>('search')
const showProposals = ref(false)

// --- search tab ---
const query = ref('')
const category = ref('')
let debounceTimer: ReturnType<typeof setTimeout> | null = null

// 字典分类：下拉里 value 加 `dict:` 前缀与术语分类区分；选中后浏览/搜索都走
// dictEntries（只读，不进主库检索）。
const dictName = computed(() => (category.value.startsWith('dict:') ? category.value.slice(5) : ''))
const isDictCategory = computed(() => dictName.value !== '')

watch([query, category], () => {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => {
    if (isDictCategory.value) { loadDictEntries(true); return }
    glossary.search(query.value, category.value)
  }, 200)
})

// --- browse all (virtual scroll): when no query but a category is picked,
// show every entry in that category instead of requiring a search term. ---
const browseEntries = ref<GlossaryEntry[]>([])
const browsing = computed(() => !query.value.trim() && !!category.value)

// Monotonic guard so only the latest browse request writes browseEntries —
// a slow response for an earlier category must not overwrite a newer one.
// Shared with refreshView() below so the two never clobber each other either.
let browseSeq = 0
watch([category, query], async () => {
  if (!query.value.trim() && category.value && !isDictCategory.value) {
    const seq = ++browseSeq
    const r = await api.glossaryEntries(category.value, 0, 100000)
    if (seq !== browseSeq) return
    browseEntries.value = r.items
  } else {
    ++browseSeq // invalidate any in-flight browse request
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
    const seq = ++browseSeq
    const r = await api.glossaryEntries(category.value, 0, 100000)
    if (seq !== browseSeq) return
    browseEntries.value = r.items
  }
}

// --- 字典分类（只读）：分页浏览 + 搜索 + 导入 + 删除 ---
// 4 万多条不能像术语那样一次全拉：走 dictEntries 分页（每页 200，后端上限），
// 滚近尾部自动追加下一页；q 非空时同一接口做子串搜索（后端排序）。
const DICT_PAGE = 200
const dictItems = ref<DictEntry[]>([])
const dictTotal = ref(0)
const dictLoading = ref(false)
// 释义默认 line-clamp 3 行，点击行展开/收起全文。
const expandedDict = ref(new Set<string>())

// Monotonic guard，同 browseSeq：慢响应不许盖掉新分类/新搜索词的结果。
let dictSeq = 0
async function loadDictEntries(reset: boolean) {
  const name = dictName.value
  if (!name) return
  const seq = ++dictSeq
  if (reset) {
    dictItems.value = []
    dictTotal.value = 0
    expandedDict.value = new Set()
  }
  dictLoading.value = true
  try {
    const r = await api.dictEntries(name, query.value.trim(), reset ? 0 : dictItems.value.length, DICT_PAGE)
    if (seq !== dictSeq) return
    dictItems.value = reset ? r.items : [...dictItems.value, ...r.items]
    dictTotal.value = r.total
  } finally {
    if (seq === dictSeq) dictLoading.value = false
  }
}

const dictScrollParent = useTemplateRef<HTMLElement>('dictScrollParent')
const dictVirtualizer = useVirtualizer(computed(() => ({
  count: dictItems.value.length,
  getScrollElement: () => dictScrollParent.value,
  estimateSize: () => 96,
  overscan: 8,
  // 词条 id（page.index）在单本字典内唯一；换字典时列表整个重置。
  getItemKey: (index: number) => dictItems.value[index]?.id ?? index,
})))

// 同 measureRow：展开/收起释义后动态重测行高（measureElement 挂 ResizeObserver）。
function measureDictRow(el: any) {
  dictVirtualizer.value.measureElement(el)
}

// 滚动接近已加载末尾且还有剩余时自动拉下一页（虚拟滚动天然只渲染视口行）。
watchEffect(() => {
  if (!isDictCategory.value || dictLoading.value) return
  if (dictItems.value.length >= dictTotal.value) return
  const vis = dictVirtualizer.value.getVirtualItems()
  const last = vis[vis.length - 1]
  if (last && last.index >= dictItems.value.length - 10) loadDictEntries(false)
})

function toggleDictRow(id: string) {
  const s = new Set(expandedDict.value)
  if (s.has(id)) s.delete(id)
  else s.add(id)
  expandedDict.value = s
}

// 导入字典：本地 JSON 文件 → multipart 上传，后端归一化后存 dicts/ 子目录。
const dictFileInput = useTemplateRef<HTMLInputElement>('dictFileInput')
const dictImporting = ref(false)
async function onDictFileChosen(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = '' // 允许再次选择同一文件
  if (!file || dictImporting.value) return
  dictImporting.value = true
  try {
    const info = await dictStore.importDict(file)
    toast.show(`已导入字典「${info.name}」（${info.count} 条）`, 'success')
    if (isDictCategory.value) await loadDictEntries(true) // 正看字典时刷新（覆盖导入）
  } catch (err: any) {
    toast.show('导入字典失败: ' + (err?.message || err), 'error')
  } finally {
    dictImporting.value = false
  }
}

async function removeCurrentDict() {
  const name = dictName.value
  if (!name) return
  if (!(await confirm({
    title: '删除字典',
    message: `确定删除字典「${name}」吗？`,
    detail: '将从本地移除该字典，编辑器取词与此处浏览随之失效。',
    tone: 'danger',
    confirmText: '删除',
  }))) return
  try {
    await dictStore.removeDict(name)
    category.value = ''
    toast.show('已删除字典', 'success')
  } catch (err: any) {
    toast.show(err?.message || '删除失败', 'error')
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

// The edit/delete controls on existing entries show only while the lock is OPEN —
// for everyone, logged in or not — so the lock genuinely guards against accidental
// changes (its whole point). Read-only mode never shows them. With the lock open, a
// remote (server) entry still routes its edit/delete through a team proposal; a
// local (import/user) entry is changed directly.
const canShowControls = computed(() => !team.readonly && editUnlocked.value)

// 管理员整库分发：本地术语库完全替换线上（上传里没有的线上条目会被删除）。
// 下行同步每次拉到更新都会在本地 backups/ 滚动留档 10 份，误传可回滚。
const uploading = ref(false)
async function uploadToServer() {
  if (uploading.value) return
  try {
    const data = await api.glossaryExport()
    const nE = data.entries?.length ?? 0
    const nA = data.appellations?.length ?? 0
    const nG = data.grammar?.length ?? 0
    if (nE === 0) { toast.show('本地术语库为空，拒绝上传（会清空线上库）', 'error'); return }
    if (!(await confirm({
      title: '上传至线上术语库',
      message: '将用本地术语库【完全替换】线上术语库。',
      detail: `本地内容：词条 ${nE} / 人称 ${nA} / 语法 ${nG}。线上多出的条目将被删除，全员将在 1 分钟内同步到此版本。`,
      tone: 'danger',
      confirmText: '完全替换',
    }))) return
    uploading.value = true
    const r = await api.teamGlossaryReplace(data)
    toast.show(`已完全替换线上术语库：写入 ${r.written}、删除 ${r.deleted}（v${r.version}）`, 'success', 8000)
    await team.sync(true)
    await refreshView()
  } catch (e: any) {
    toast.show('上传失败: ' + (e?.message || e), 'error')
  } finally {
    uploading.value = false
  }
}

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
  if (!(await confirm({ title: '保存修改', message: '确定保存对这条词条的修改吗？', tone: 'primary', confirmText: '保存' }))) return
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
    if (!(await confirm({ title: '提交删除提案', message: `确定提交删除「${e?.source ?? ''}」的提案吗？`, detail: '需管理员审核通过后生效。', tone: 'danger', confirmText: '提交' }))) return
    try {
      await team.submitProposal({
        kind: 'delete', targetId: id, category: e?.category || '自定义', payload: { id },
      })
      toast.show('已提交删除提案，待管理员审核', 'success')
    } catch (err: any) { toast.show(err.message || '提交失败', 'error') }
    return
  }
  // Local entries (import/user) — delete directly, even when logged in.
  if (!(await confirm({ title: '删除词条', message: `确定删除「${e?.source ?? ''}」这条词条吗？`, detail: '此操作不可撤销。', tone: 'danger', confirmText: '删除' }))) return
  try { await glossary.deleteEntry(id); await refreshView(); toast.show('已删除', 'success') }
  catch (err: any) { toast.show(err.message || '删除失败', 'error') }
}

// --- appellation tab ---
const speaker = ref('')
const target = ref('')
const result = ref<{ jp?: string; cn?: string; found: boolean } | null>(null)
const editingAppell = ref(false)
const appellDraft = ref({ jp: '', cn: '' })

// Appellation editing is local-only (no proposal `kind` for it), and the 60s
// team sync's MergeImport wholesale-replaces the appellation table — so any edit
// a logged-in user makes is silently wiped on the next poll. Gate editing off in
// team mode; pure-local users still edit via the lock toggle.
const appellEditable = computed(() => editUnlocked.value && !team.loggedIn)

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

// Always reload on entering matrix view so an external import/team-sync that
// changed the appellation table is reflected (not just the first-ever open).
watch(appellMode, (m) => { if (m === 'matrix') loadMatrix() })

function startEditCell(s: string, t: string) {
  editingCell.value = cellKey(s, t)
  const c = matrixCell.value[cellKey(s, t)]
  cellDraft.value = { jp: c?.jp || '', cn: c?.cn || '' }
}

async function saveCell(s: string, t: string) {
  if (team.loggedIn) { toast.show('团队模式下称呼表由服务器同步，暂不支持本地修改', 'warn'); return }
  if (!(await confirm({ title: '保存称呼', message: `确定保存「${s} → ${t}」的称呼修改吗？`, tone: 'primary', confirmText: '保存' }))) return
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

// Monotonic guard: out-of-order lookups (e.g. rapid target switches) must not
// let a stale response overwrite the result for the current combination.
let lookupSeq = 0
watch([speaker, target], async () => {
  if (speaker.value && target.value) {
    const seq = ++lookupSeq
    const r = await glossary.lookupAppellation(speaker.value, target.value)
    if (seq !== lookupSeq) return
    result.value = r
  } else {
    ++lookupSeq // invalidate any in-flight lookup
    result.value = null
  }
})

function startEditAppell() {
  editingAppell.value = true
  appellDraft.value = { jp: result.value?.jp || '', cn: result.value?.cn || '' }
}

async function saveAppell() {
  if (team.loggedIn) { toast.show('团队模式下称呼表由服务器同步，暂不支持本地修改', 'warn'); return }
  if (!(await confirm({ title: '保存称呼', message: '确定保存对这条称呼的修改吗？', tone: 'primary', confirmText: '保存' }))) return
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
  // 字典列表（分类下拉合并展示）；后端不支持字典模块时静默忽略。
  await dictStore.fetchDicts().catch(() => {})
})
</script>

<template>
  <div class="min-h-screen page-bg text-[var(--color-text)]">
    <header class="sticky top-0 z-[var(--z-sticky)] bg-[color-mix(in_oklch,var(--color-bg)_82%,transparent)] backdrop-blur-md border-b border-[var(--color-border)]">
      <div class="max-w-6xl mx-auto px-6 h-14 flex items-center gap-3">
        <button @click="router.push('/')" class="icon-btn -ml-1" title="返回编辑器"><ArrowLeft :size="18" /></button>
        <h1 class="text-base font-bold tracking-tight">术语库</h1>
        <div class="ml-auto flex items-center gap-2">
          <button
            v-if="!team.readonly"
            data-tour="glo-lock"
            @click="toggleEditLock"
            :class="['btn btn-sm gap-1.5', editUnlocked ? 'border border-warning/40 bg-warning/15 text-warning hover:bg-warning/20' : 'btn-ghost border border-[var(--color-border)]']"
            :title="editUnlocked ? '编辑已解锁，点击重新锁定' : '编辑已锁定，点击解锁以修改/删除词条'"
          >
            <component :is="editUnlocked ? Unlock : Lock" :size="16" />
            {{ editUnlocked ? '编辑中' : '锁定' }}
          </button>
          <button
            v-if="team.loggedIn && team.isAdmin && !isDictCategory"
            data-tour="glo-upload"
            @click="uploadToServer"
            :disabled="uploading"
            class="btn btn-sm btn-ghost border border-[var(--color-border)] gap-1.5"
            title="把本地术语库完整上传并完全替换线上术语库（仅管理员；线上多出的条目会被删除）"
          >
            <UploadCloud :size="16" />
            {{ uploading ? '上传中…' : '上传至线上术语库' }}
          </button>
          <button
            @click="dictFileInput?.click()"
            :disabled="dictImporting"
            class="btn btn-sm btn-ghost border border-[var(--color-border)] gap-1.5"
            title="导入 JSON 字典为只读分类（独立于主术语库，不参与团队同步）"
          >
            <BookMarked :size="16" />
            {{ dictImporting ? '导入中…' : '导入字典' }}
          </button>
          <input ref="dictFileInput" type="file" accept=".json,application/json" class="hidden" @change="onDictFileChosen" />
        </div>
      </div>
    </header>

    <div class="max-w-6xl mx-auto px-6 pt-4">
      <div class="flex gap-1 border-b border-[var(--color-border)]" data-tour="glo-tabs">
        <button
          @click="tab = 'search'"
          :class="['px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors', tab === 'search' ? 'border-primary text-[var(--color-text)]' : 'border-transparent text-[var(--color-text-secondary)] hover:text-[var(--color-text)]']"
        >术语检索</button>
        <button
          @click="tab = 'appellation'"
          :class="['px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors', tab === 'appellation' ? 'border-primary text-[var(--color-text)]' : 'border-transparent text-[var(--color-text-secondary)] hover:text-[var(--color-text)]']"
        >称呼查询</button>
      </div>
    </div>

    <main class="max-w-6xl mx-auto p-6">
      <!-- search tab -->
      <div v-show="tab === 'search'" class="space-y-4">
        <!-- 团队模式:提案/审核入口 -->
        <div v-if="team.loggedIn" data-tour="glo-proposals">
          <button @click="showProposals = !showProposals"
            class="flex items-center gap-2 text-sm text-[var(--color-text-secondary)] hover:text-[var(--color-text)]">
            <span class="app-chip bg-primary/15 text-primary">团队模式</span>
            {{ showProposals ? '收起提案面板' : '我的提案 / 审核' }}
          </button>
          <div v-if="showProposals" class="mt-2">
            <TeamProposalsPanel />
          </div>
        </div>
        <!-- 只读模式提示 -->
        <div v-else-if="team.readonly" class="flex items-center gap-2 text-xs text-[var(--color-text-secondary)] app-card px-3 py-2">
          <span class="app-chip bg-warning/15 text-warning">只读模式</span>
          正在从服务器同步术语库，登录后才能新增/修改。
        </div>
        <div class="flex gap-2" data-tour="glo-search">
          <div class="relative flex-1">
            <Search :size="16" class="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-text-tertiary)] pointer-events-none" />
            <input
              v-model="query"
              type="text"
              :placeholder="isDictCategory ? '搜索字典（见出语/假名/释义）' : '输入原文或译文（日⇄中双向）'"
              class="app-input pl-9"
            />
          </div>
          <SkSelect
            class="w-auto"
            :model-value="category"
            @update:model-value="category = $event as string"
            :options="[
              { value: '', label: '全部分类' },
              ...glossary.categories.map(c => ({ value: c.category, label: `${c.category} (${c.count})` })),
              ...dictStore.dicts.map(d => ({ value: 'dict:' + d.name, label: `${d.name}（字典 · 只读）` })),
            ]"
          />
          <button
            v-if="!team.readonly && !isDictCategory"
            @click="showAdd = !showAdd"
            class="btn btn-brand btn-control gap-1 whitespace-nowrap"
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
        <div v-if="showAdd" class="app-card p-4 space-y-3">
          <div class="grid grid-cols-2 gap-3">
            <input v-model="draft.source" placeholder="原文" class="app-input" />
            <input v-model="draft.translation" placeholder="译文" class="app-input" />
          </div>
          <div class="grid grid-cols-2 gap-3">
            <input v-model="draft.category" list="glossary-cats" placeholder="分类（可选，可新建，默认自定义）" class="app-input" />
            <input v-model="draft.note" placeholder="备注（可选）" class="app-input" />
          </div>
          <div class="flex justify-end gap-2">
            <button @click="showAdd = false" class="btn btn-sm btn-ghost">取消</button>
            <button @click="submitAdd" class="btn btn-sm btn-brand">保存</button>
          </div>
        </div>

        <!-- 字典分类（只读）：分页 + 虚拟滚动浏览；搜索同一接口。行内无任何
             编辑/删除控件；删除整本字典的入口在表头右侧。 -->
        <template v-if="isDictCategory">
          <div class="flex items-center justify-between gap-3">
            <div class="text-xs text-[var(--color-text-secondary)]">
              {{ dictName }} · 共 {{ dictTotal }} 条{{ query.trim() ? '（匹配）' : '' }}
            </div>
            <button
              @click="removeCurrentDict"
              class="btn btn-xs btn-ghost text-error hover:bg-error/10 gap-1"
              title="从本地删除这本字典（不影响主术语库）"
            ><Trash2 :size="12" />删除此字典</button>
          </div>
          <div ref="dictScrollParent" class="overflow-auto" style="height: calc(100vh - 240px)">
            <div :style="{ height: dictVirtualizer.getTotalSize() + 'px', position: 'relative', width: '100%' }">
              <div
                v-for="vr in dictVirtualizer.getVirtualItems()"
                :key="dictItems[vr.index]?.id ?? vr.index"
                :data-index="vr.index"
                :ref="measureDictRow"
                :style="{ position: 'absolute', top: 0, left: 0, width: '100%', transform: `translateY(${vr.start}px)` }"
                class="pb-2"
              >
                <div
                  class="rounded-[var(--radius-control)] border border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-3 transition-colors hover:border-[var(--color-border-strong)] cursor-pointer"
                  :title="expandedDict.has(dictItems[vr.index].id) ? '点击收起' : '点击展开全文'"
                  @click="toggleDictRow(dictItems[vr.index].id)"
                >
                  <div class="flex items-baseline gap-2 flex-wrap">
                    <span class="text-sm font-medium">{{ dictItems[vr.index].key }}</span>
                    <span class="text-xs text-[var(--color-text-secondary)]">{{ dictItems[vr.index].kana }}</span>
                    <span v-if="dictItems[vr.index].accent" class="text-[10px] text-[var(--color-text-tertiary)]">{{ dictItems[vr.index].accent }}</span>
                  </div>
                  <div
                    class="text-xs text-[var(--color-text-secondary)] mt-1 whitespace-pre-wrap leading-relaxed"
                    :class="expandedDict.has(dictItems[vr.index].id) ? '' : 'line-clamp-3'"
                  >{{ dictItems[vr.index].text }}</div>
                </div>
              </div>
            </div>
            <div v-if="dictLoading" class="flex items-center justify-center gap-2 text-sm text-[var(--color-text-secondary)] py-3">
              <span class="loading loading-spinner loading-sm" /> 加载中…
            </div>
            <div v-else-if="dictItems.length === 0" class="flex flex-col items-center gap-2 text-sm text-[var(--color-text-secondary)] py-12 text-center">
              <Search :size="28" class="text-[var(--color-text-tertiary)]" />
              {{ query.trim() ? '没有匹配的词条' : '字典为空' }}
            </div>
          </div>
        </template>
        <!-- results -->
        <div v-else-if="glossary.searching" class="flex items-center justify-center gap-2 text-sm text-[var(--color-text-secondary)] py-8">
          <span class="loading loading-spinner loading-sm" /> 搜索中…
        </div>
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
                <div class="group rounded-[var(--radius-control)] border border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-3 transition-colors hover:border-[var(--color-border-strong)]">
                  <!-- inline edit (browse mode) -->
                  <template v-if="editingId === browseEntries[vr.index].id">
                    <div class="grid grid-cols-2 gap-2 mb-2">
                      <input v-model="editDraft.source" class="app-input" />
                      <input v-model="editDraft.translation" class="app-input" />
                    </div>
                    <input v-model="editDraft.category" list="glossary-cats" placeholder="分类" class="app-input mb-2" />
                    <input v-model="editDraft.note" placeholder="备注" class="app-input mb-2" />
                    <div class="flex justify-end gap-2">
                      <button @click="editingId = null" class="btn btn-xs btn-ghost gap-1"><X :size="14" />取消</button>
                      <button @click="saveEdit(browseEntries[vr.index].id)" class="btn btn-xs btn-brand gap-1"><Check :size="14" />保存</button>
                    </div>
                  </template>
                  <template v-else>
                    <div class="flex items-start justify-between gap-3">
                      <div class="min-w-0 flex-1">
                        <div class="flex items-baseline gap-2 flex-wrap">
                          <span class="text-sm font-medium">{{ browseEntries[vr.index].source }}</span>
                          <span class="text-[var(--color-text-tertiary)]">→</span>
                          <span class="text-sm">{{ browseEntries[vr.index].translation }}</span>
                        </div>
                        <div v-if="browseEntries[vr.index].aliases?.length" class="text-xs text-[var(--color-text-secondary)] mt-1">别称：{{ browseEntries[vr.index].aliases!.join('、') }}</div>
                        <div v-if="browseEntries[vr.index].note" class="text-xs text-[var(--color-text-secondary)] mt-1 whitespace-pre-wrap">{{ browseEntries[vr.index].note }}</div>
                        <div v-if="browseEntries[vr.index].subCategory" class="text-[10px] text-[var(--color-text-tertiary)] mt-1">{{ browseEntries[vr.index].subCategory }}</div>
                      </div>
                      <div v-if="canShowControls" class="flex items-center gap-1 md:opacity-0 md:group-hover:opacity-100 transition-opacity">
                        <button @click="startEdit(browseEntries[vr.index])" class="icon-btn"><Pencil :size="14" /></button>
                        <button @click="removeEntry(browseEntries[vr.index].id)" class="icon-btn hover:text-error"><Trash2 :size="14" /></button>
                      </div>
                    </div>
                  </template>
                </div>
              </div>
            </div>
          </div>
        </template>
        <div v-else-if="query && glossary.results.length === 0" class="flex flex-col items-center gap-2 text-sm text-[var(--color-text-secondary)] py-12 text-center">
          <Search :size="28" class="text-[var(--color-text-tertiary)]" />
          没有匹配的词条
        </div>
        <div v-else-if="!query" class="flex flex-col items-center gap-2 text-sm text-[var(--color-text-secondary)] py-12 text-center">
          <Search :size="28" class="text-[var(--color-text-tertiary)]" />
          输入关键词检索，或选一个分类浏览全部
        </div>
        <ul v-else class="grid grid-cols-1 md:grid-cols-2 gap-2">
          <li
            v-for="e in glossary.results"
            :key="e.id"
            class="group rounded-[var(--radius-control)] border border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-3 transition-colors hover:border-[var(--color-border-strong)]"
          >
            <template v-if="editingId === e.id">
              <div class="grid grid-cols-2 gap-2 mb-2">
                <input v-model="editDraft.source" class="app-input" />
                <input v-model="editDraft.translation" class="app-input" />
              </div>
              <input v-model="editDraft.category" list="glossary-cats" placeholder="分类" class="app-input mb-2" />
              <input v-model="editDraft.note" placeholder="备注" class="app-input mb-2" />
              <div class="flex justify-end gap-2">
                <button @click="editingId = null" class="btn btn-xs btn-ghost gap-1"><X :size="14" />取消</button>
                <button @click="saveEdit(e.id)" class="btn btn-xs btn-brand gap-1"><Check :size="14" />保存</button>
              </div>
            </template>
            <template v-else>
              <div class="flex items-start justify-between gap-3">
                <div class="min-w-0 flex-1">
                  <div class="flex items-baseline gap-2 flex-wrap">
                    <span class="text-sm font-medium">{{ e.source }}</span>
                    <span class="text-[var(--color-text-tertiary)]">→</span>
                    <span class="text-sm">{{ e.translation }}</span>
                  </div>
                  <div v-if="e.aliases?.length" class="text-xs text-[var(--color-text-secondary)] mt-1">别称：{{ e.aliases.join('、') }}</div>
                  <div v-if="e.note" class="text-xs text-[var(--color-text-secondary)] mt-1 whitespace-pre-wrap">{{ e.note }}</div>
                  <div class="flex items-center gap-2 mt-1.5 flex-wrap">
                    <span class="app-chip bg-[color-mix(in_oklch,var(--color-base-content)_8%,transparent)] text-[var(--color-text-secondary)]">{{ e.category }}</span>
                    <span v-if="e.subCategory" class="text-[10px] text-[var(--color-text-tertiary)]">{{ e.subCategory }}</span>
                    <span v-if="e.origin === 'user'" class="app-chip bg-primary/12 text-primary">自添加</span>
                    <span v-if="e.contributorName" class="text-[10px] text-[var(--color-text-tertiary)]">添加者：{{ e.contributorName }}</span>
                  </div>
                </div>
                <div v-if="canShowControls" class="flex items-center gap-1 md:opacity-0 md:group-hover:opacity-100 transition-opacity">
                  <button @click="startEdit(e)" class="icon-btn"><Pencil :size="14" /></button>
                  <button @click="removeEntry(e.id)" class="icon-btn hover:text-error"><Trash2 :size="14" /></button>
                </div>
              </div>
            </template>
          </li>
        </ul>
      </div>

      <!-- appellation tab -->
      <div v-show="tab === 'appellation'" class="space-y-4">
        <div class="flex items-center justify-between gap-3">
          <p class="text-sm text-[var(--color-text-secondary)]">
            {{ appellMode === 'lookup' ? '选「说话人」和「对象」，直接查出称呼（来自人称表）。' : (appellEditable ? '说话人（行）× 对象（列），点格子就地编辑。' : '说话人（行）× 对象（列）。解锁编辑后可点格子修改。') }}
          </p>
          <div class="flex gap-1">
            <button @click="appellMode = 'lookup'" :class="['btn btn-xs', appellMode === 'lookup' ? 'btn-brand' : 'btn-ghost border border-[var(--color-border)]']">逐对查询</button>
            <button @click="appellMode = 'matrix'" :class="['btn btn-xs', appellMode === 'matrix' ? 'btn-brand' : 'btn-ghost border border-[var(--color-border)]']">矩阵视图</button>
          </div>
        </div>

        <!-- matrix view -->
        <div v-if="appellMode === 'matrix'" class="overflow-auto border border-[var(--color-border)] rounded-[var(--radius-control)]" style="max-height: calc(100vh - 240px)">
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
                  :class="['border border-[var(--color-border)] px-2 py-1 align-top min-w-[64px]', appellEditable ? 'cursor-pointer hover:bg-primary/5' : '']"
                  @click="appellEditable && startEditCell(s, t)"
                >
                  <template v-if="editingCell === cellKey(s, t)">
                    <input v-model="cellDraft.jp" placeholder="日" class="w-full px-1 py-0.5 rounded bg-[var(--color-bg)] border border-[var(--color-border)] mb-1" @click.stop @keydown.enter="saveCell(s, t)" />
                    <input v-model="cellDraft.cn" placeholder="中" class="w-full px-1 py-0.5 rounded bg-[var(--color-bg)] border border-[var(--color-border)] mb-1" @click.stop @keydown.enter="saveCell(s, t)" />
                    <div class="flex gap-1 justify-end" @click.stop>
                      <button @click="editingCell = null" class="text-[var(--color-text-secondary)] hover:text-[var(--color-text)]"><X :size="12" /></button>
                      <button @click="saveCell(s, t)" class="text-primary hover:opacity-80"><Check :size="12" /></button>
                    </div>
                  </template>
                  <template v-else>
                    <div v-if="matrixCell[cellKey(s, t)]?.jp" class="whitespace-nowrap">{{ matrixCell[cellKey(s, t)]?.jp }}</div>
                    <div v-if="matrixCell[cellKey(s, t)]?.cn" class="whitespace-nowrap text-primary">{{ matrixCell[cellKey(s, t)]?.cn }}</div>
                  </template>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- lookup view -->
        <template v-else>
        <div class="flex items-center gap-3">
          <SkSelect
            class="flex-1"
            :model-value="speaker"
            @update:model-value="speaker = $event as string"
            :options="[{ value: '', label: '说话人 A' }, ...glossary.speakers.map(s => ({ value: s, label: s }))]"
          />
          <span class="text-[var(--color-text-secondary)] text-sm">称呼</span>
          <SkSelect
            class="flex-1"
            :disabled="!speaker"
            :model-value="target"
            @update:model-value="target = $event as string"
            :options="[{ value: '', label: '对象 B' }, ...glossary.targets.map(t => ({ value: t, label: t }))]"
          />
        </div>

        <div v-if="result" class="app-card p-5">
          <template v-if="result.found && !editingAppell">
            <div class="text-sm text-[var(--color-text-secondary)] mb-2">
              <span class="text-[var(--color-text)] font-medium">{{ speaker }}</span> 称呼
              <span class="text-[var(--color-text)] font-medium">{{ target }}</span> 为：
            </div>
            <div class="flex items-baseline gap-4">
              <div v-if="result.jp"><span class="text-xs text-[var(--color-text-tertiary)] mr-1">日</span><span class="text-lg font-medium">{{ result.jp }}</span></div>
              <div v-if="result.cn"><span class="text-xs text-[var(--color-text-tertiary)] mr-1">中</span><span class="text-lg font-medium">{{ result.cn }}</span></div>
            </div>
            <button v-if="appellEditable" @click="startEditAppell" class="flex items-center gap-1 text-xs text-[var(--color-text-secondary)] hover:text-[var(--color-text)] mt-3"><Pencil :size="12" />编辑</button>
          </template>
          <template v-else-if="editingAppell">
            <div class="grid grid-cols-2 gap-3 mb-3">
              <label class="app-label">日文<input v-model="appellDraft.jp" class="app-input mt-1" /></label>
              <label class="app-label">中文<input v-model="appellDraft.cn" class="app-input mt-1" /></label>
            </div>
            <div class="flex justify-end gap-2">
              <button @click="editingAppell = false" class="btn btn-sm btn-ghost">取消</button>
              <button @click="saveAppell" class="btn btn-sm btn-brand">保存</button>
            </div>
          </template>
          <template v-else>
            <div class="text-sm text-[var(--color-text-secondary)]">人称表里暂无这对组合的记录。<button v-if="appellEditable" @click="startEditAppell" class="text-primary hover:underline ml-1">手动补充</button><span v-else class="opacity-60 ml-1">（解锁编辑后可补充）</span></div>
          </template>
        </div>
        <div v-else-if="speaker && target" class="flex items-center justify-center gap-2 text-sm text-[var(--color-text-secondary)] py-8">
          <span class="loading loading-spinner loading-sm" /> 查询中…
        </div>
        </template>
      </div>
    </main>
  </div>
</template>
