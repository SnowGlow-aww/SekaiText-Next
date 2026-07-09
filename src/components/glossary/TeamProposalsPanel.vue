<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RefreshCw, Inbox, ClipboardCheck, Check, X, Undo2 } from 'lucide-vue-next'
import { useTeamStore } from '../../stores/team'
import { useGlossaryStore } from '../../stores/glossary'
import { useGlossaryNotifyStore } from '../../stores/glossaryNotify'
import { useToast } from '../../composables/useToast'
import { useConfirm } from '../../composables/useConfirm'
import { api } from '../../api/client'
import type { Proposal } from '../../types/glossary'

const team = useTeamStore()
const glossary = useGlossaryStore()
const glossaryNotify = useGlossaryNotifyStore()
const { show } = useToast()
const { confirm, prompt } = useConfirm()

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
  if (!(await confirm({ title: '撤回提案', message: '撤回这条提案吗？', tone: 'danger', confirmText: '撤回' }))) return
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
  const note = await prompt({
    title: '驳回提案',
    message: '请填写驳回理由（必填）',
    placeholder: '驳回理由',
    minLength: 1,
    tone: 'danger',
    confirmText: '驳回',
  })
  if (note == null) return
  try { await api.teamRejectProposal(p.id, note); show('已驳回', 'success'); await loadPending(); void glossaryNotify.poll() }
  catch (e) { show(msg(e), 'error') }
}

async function afterReview() {
  await loadPending()
  await team.sync(true).catch(() => {})
  await glossary.fetchCategories()
  await glossary.loadAllEntries(true)
  void glossaryNotify.poll() // 审完立即刷新侧栏呼吸灯（待审数归零就熄灭）
}

function parsePayload(p: Proposal): any {
  try { return JSON.parse(p.payload) } catch { return {} }
}
function kindLabel(k: string) { return k === 'add' ? '新增' : k === 'edit' ? '修改' : '删除' }
function statusLabel(s: string) { return s === 'pending' ? '待审核' : s === 'approved' ? '已通过' : '已驳回' }
function kindBadgeClass(k: string) {
  return k === 'add' ? 'bg-success/15 text-success' : k === 'edit' ? 'bg-info/15 text-info' : 'bg-error/15 text-error'
}
function statusBadgeClass(s: string) {
  return s === 'approved' ? 'bg-success/15 text-success' : s === 'rejected' ? 'bg-error/15 text-error' : 'bg-warning/15 text-warning'
}
function msg(e: unknown) { return e instanceof Error ? e.message : String(e) }

onMounted(() => { void loadMine() })
function switchTab(t: 'mine' | 'review') {
  tab.value = t
  if (t === 'mine') void loadMine(); else void loadPending()
}
</script>

<template>
  <div class="app-card p-4">
    <div class="flex items-center justify-between mb-4">
      <div class="flex gap-1">
        <button @click="switchTab('mine')"
          :class="['px-3 py-1.5 rounded-[var(--radius-control)] text-xs font-medium transition-colors', tab === 'mine' ? 'bg-primary text-primary-content' : 'text-[var(--color-text-secondary)] hover:text-[var(--color-text)] hover:bg-[color-mix(in_oklch,var(--color-base-content)_8%,transparent)]']">
          我的提案
        </button>
        <button v-if="team.isReviewer" @click="switchTab('review')"
          :class="['px-3 py-1.5 rounded-[var(--radius-control)] text-xs font-medium transition-colors', tab === 'review' ? 'bg-primary text-primary-content' : 'text-[var(--color-text-secondary)] hover:text-[var(--color-text)] hover:bg-[color-mix(in_oklch,var(--color-base-content)_8%,transparent)]']">
          待我审核
        </button>
      </div>
      <button @click="tab === 'mine' ? loadMine() : loadPending()" class="icon-btn" :class="{ 'animate-spin': loading }" title="刷新"><RefreshCw :size="15" /></button>
    </div>

    <div v-if="loading" class="flex items-center justify-center gap-2 py-8 text-sm text-[var(--color-text-secondary)]">
      <span class="loading loading-spinner loading-sm" /> 加载中…
    </div>

    <!-- 我的提案 -->
    <ul v-else-if="tab === 'mine'" class="space-y-2">
      <li v-if="mine.length === 0" class="flex flex-col items-center gap-2 py-10 text-sm text-[var(--color-text-tertiary)]">
        <Inbox :size="26" class="opacity-70" />
        <span>还没有提交过提案</span>
      </li>
      <li v-for="p in mine" :key="p.id"
        class="flex items-center gap-3 rounded-[var(--radius-control)] border border-[var(--color-border)] bg-[var(--color-bg)] px-3 py-2.5 transition-colors hover:border-[var(--color-border-strong)]">
        <div class="min-w-0 flex-1">
          <div class="flex items-center gap-2 text-sm">
            <span class="app-chip" :class="kindBadgeClass(p.kind)">{{ kindLabel(p.kind) }}</span>
            <span class="font-medium truncate">{{ parsePayload(p).source || parsePayload(p).id || p.targetId }}</span>
            <span v-if="parsePayload(p).translation" class="text-[var(--color-text-secondary)] truncate">→ {{ parsePayload(p).translation }}</span>
          </div>
          <div class="text-[11px] text-[var(--color-text-tertiary)] mt-1 flex flex-wrap items-center gap-x-1.5 gap-y-1">
            <span>{{ p.category }}</span>
            <span class="app-chip" :class="statusBadgeClass(p.status)">{{ statusLabel(p.status) }}</span>
            <span v-if="p.reviewNote">· 理由：{{ p.reviewNote }}</span>
          </div>
        </div>
        <button v-if="p.status === 'pending'" @click="withdraw(p.id)" class="btn btn-ghost btn-xs gap-1 shrink-0 text-[var(--color-text-secondary)] hover:text-error" title="撤回">
          <Undo2 :size="13" /> 撤回
        </button>
      </li>
    </ul>

    <!-- 待审核 -->
    <ul v-else class="space-y-2">
      <li v-if="pending.length === 0" class="flex flex-col items-center gap-2 py-10 text-sm text-[var(--color-text-tertiary)]">
        <ClipboardCheck :size="26" class="opacity-70" />
        <span>没有待审核的提案</span>
      </li>
      <li v-for="p in pending" :key="p.id"
        class="rounded-[var(--radius-control)] border border-[var(--color-border)] bg-[var(--color-bg)] px-3 py-2.5 transition-colors hover:border-[var(--color-border-strong)]">
        <div class="flex items-center gap-2 text-sm">
          <span class="app-chip" :class="kindBadgeClass(p.kind)">{{ kindLabel(p.kind) }}</span>
          <span class="font-medium truncate">{{ parsePayload(p).source || parsePayload(p).id || p.targetId }}</span>
          <span v-if="parsePayload(p).translation" class="text-[var(--color-text-secondary)] truncate">→ {{ parsePayload(p).translation }}</span>
        </div>
        <div class="text-[11px] text-[var(--color-text-tertiary)] mt-1">
          {{ p.category }} · 提交人：{{ p.authorName || p.authorId }}
          <span v-if="parsePayload(p).note"> · 备注：{{ parsePayload(p).note }}</span>
        </div>
        <div class="flex justify-end gap-2 mt-2.5">
          <button @click="reject(p)" class="btn btn-xs btn-ghost gap-1 text-error hover:bg-error/10"><X :size="13" /> 驳回</button>
          <button @click="approve(p)" class="btn btn-xs btn-brand gap-1"><Check :size="13" /> 通过</button>
        </div>
      </li>
    </ul>
  </div>
</template>
