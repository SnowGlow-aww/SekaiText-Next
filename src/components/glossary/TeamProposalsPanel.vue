<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useTeamStore } from '../../stores/team'
import { useGlossaryStore } from '../../stores/glossary'
import { useToast } from '../../composables/useToast'
import { api } from '../../api/client'
import type { Proposal } from '../../types/glossary'

const team = useTeamStore()
const glossary = useGlossaryStore()
const { show } = useToast()

const tab = ref<'mine' | 'review'>('mine')
const mine = ref<Proposal[]>([])
const pending = ref<Proposal[]>([])
const loading = ref(false)

async function loadMine() {
  loading.value = true
  try { mine.value = await api.teamMyProposals() }
  catch (e) { show(msg(e), 'error') }
  finally { loading.value = false }
}

async function loadPending() {
  loading.value = true
  try { pending.value = await api.teamPendingProposals() }
  catch (e) { show(msg(e), 'error') }
  finally { loading.value = false }
}

async function withdraw(id: string) {
  if (!confirm('撤回这条提案吗？')) return
  try { await api.teamWithdrawProposal(id); show('已撤回', 'success'); await loadMine() }
  catch (e) { show(msg(e), 'error') }
}

async function approve(p: Proposal) {
  try {
    await api.teamApproveProposal(p.id)
    show('已通过，术语库已更新', 'success')
    await afterReview()
  } catch (e) { show(msg(e), 'error') }
}

async function reject(p: Proposal) {
  const note = prompt('驳回理由（必填）：')
  if (!note) return
  try { await api.teamRejectProposal(p.id, note); show('已驳回', 'success'); await loadPending() }
  catch (e) { show(msg(e), 'error') }
}

async function afterReview() {
  await loadPending()
  await team.sync(true).catch(() => {})
  await glossary.fetchCategories()
  await glossary.loadAllEntries(true)
}

function parsePayload(p: Proposal): any {
  try { return JSON.parse(p.payload) } catch { return {} }
}
function kindLabel(k: string) { return k === 'add' ? '新增' : k === 'edit' ? '修改' : '删除' }
function statusLabel(s: string) { return s === 'pending' ? '待审核' : s === 'approved' ? '已通过' : '已驳回' }
function msg(e: unknown) { return e instanceof Error ? e.message : String(e) }

onMounted(() => { void loadMine() })
function switchTab(t: 'mine' | 'review') {
  tab.value = t
  if (t === 'mine') void loadMine(); else void loadPending()
}
</script>

<template>
  <div class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-lg p-4">
    <div class="flex items-center justify-between mb-3">
      <div class="flex gap-1 text-xs">
        <button @click="switchTab('mine')"
          :class="['px-2.5 py-1 rounded', tab === 'mine' ? 'bg-[var(--color-primary)] text-white' : 'border border-[var(--color-border)] text-[var(--color-text-secondary)]']">
          我的提案
        </button>
        <button v-if="team.isReviewer" @click="switchTab('review')"
          :class="['px-2.5 py-1 rounded', tab === 'review' ? 'bg-[var(--color-primary)] text-white' : 'border border-[var(--color-border)] text-[var(--color-text-secondary)]']">
          待我审核
        </button>
      </div>
      <button @click="tab === 'mine' ? loadMine() : loadPending()" class="text-xs text-[var(--color-text-secondary)] hover:text-[var(--color-text)]">刷新</button>
    </div>

    <div v-if="loading" class="text-sm text-[var(--color-text-secondary)] py-6 text-center">加载中…</div>

    <!-- 我的提案 -->
    <ul v-else-if="tab === 'mine'" class="space-y-2">
      <li v-if="mine.length === 0" class="text-sm text-[var(--color-text-secondary)] py-6 text-center">还没有提交过提案</li>
      <li v-for="p in mine" :key="p.id" class="flex items-center justify-between gap-3 border border-[var(--color-border)] rounded px-3 py-2">
        <div class="min-w-0 flex-1">
          <div class="flex items-center gap-2 text-sm">
            <span class="px-1.5 py-0.5 rounded text-[10px] bg-[var(--color-bg)]">{{ kindLabel(p.kind) }}</span>
            <span class="font-medium truncate">{{ parsePayload(p).source || parsePayload(p).id || p.targetId }}</span>
            <span v-if="parsePayload(p).translation" class="text-[var(--color-text-secondary)] truncate">→ {{ parsePayload(p).translation }}</span>
          </div>
          <div class="text-[11px] text-[var(--color-text-secondary)] mt-0.5">
            {{ p.category }} ·
            <span :class="p.status === 'approved' ? 'text-[var(--color-primary)]' : p.status === 'rejected' ? 'text-error' : ''">{{ statusLabel(p.status) }}</span>
            <span v-if="p.reviewNote"> · 理由：{{ p.reviewNote }}</span>
          </div>
        </div>
        <button v-if="p.status === 'pending'" @click="withdraw(p.id)" class="text-xs text-[var(--color-text-secondary)] hover:text-error shrink-0">撤回</button>
      </li>
    </ul>

    <!-- 待审核 -->
    <ul v-else class="space-y-2">
      <li v-if="pending.length === 0" class="text-sm text-[var(--color-text-secondary)] py-6 text-center">没有待审核的提案</li>
      <li v-for="p in pending" :key="p.id" class="border border-[var(--color-border)] rounded px-3 py-2">
        <div class="flex items-center gap-2 text-sm">
          <span class="px-1.5 py-0.5 rounded text-[10px] bg-[var(--color-bg)]">{{ kindLabel(p.kind) }}</span>
          <span class="font-medium truncate">{{ parsePayload(p).source || parsePayload(p).id || p.targetId }}</span>
          <span v-if="parsePayload(p).translation" class="text-[var(--color-text-secondary)] truncate">→ {{ parsePayload(p).translation }}</span>
        </div>
        <div class="text-[11px] text-[var(--color-text-secondary)] mt-0.5">
          {{ p.category }} · 提交人：{{ p.authorName || p.authorId }}
          <span v-if="parsePayload(p).note"> · 备注：{{ parsePayload(p).note }}</span>
        </div>
        <div class="flex justify-end gap-2 mt-2">
          <button @click="reject(p)" class="btn btn-ghost btn-xs">驳回</button>
          <button @click="approve(p)" class="btn btn-primary btn-xs">通过</button>
        </div>
      </li>
    </ul>
  </div>
</template>
