<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowLeft, RefreshCw, UserPlus, Users, IdCard, Crown, ShieldCheck, Trash2, KeyRound, Ban, CircleCheck, Palette } from 'lucide-vue-next'
import { useTeamStore } from '../stores/team'
import { useToast } from '../composables/useToast'
import { useConfirm } from '../composables/useConfirm'
import { api } from '../api/client'
import TeamModePanel from '../components/settings/TeamModePanel.vue'
import SkSelect from '../components/ui/SkSelect.vue'
import { ACCENT_GROUPS } from '../data/characterColors'
import type { TeamUser } from '../types/glossary'

const router = useRouter()
const team = useTeamStore()
const { show } = useToast()
const { confirm, prompt } = useConfirm()
const ok = (m: string) => show(m, 'success')
const err = (e: unknown) => show(e instanceof Error ? e.message : String(e), 'error')

// --- my profile ---
const displayName = ref(team.user?.displayName ?? '')
const oldPw = ref('')
const newPw = ref('')
const savingName = ref(false)
const savingPw = ref(false)

async function saveName() {
  if (!displayName.value.trim()) { show('显示名不能为空', 'warn'); return }
  savingName.value = true
  try { await api.teamUpdateProfile(displayName.value.trim()); await team.refreshStatus(); ok('显示名已更新') }
  catch (e) { err(e) } finally { savingName.value = false }
}

async function savePw() {
  if (newPw.value.length < 6) { show('新密码至少 6 位', 'warn'); return }
  savingPw.value = true
  try { await api.teamChangePassword(oldPw.value, newPw.value); oldPw.value = ''; newPw.value = ''; ok('密码已修改') }
  catch (e) { err(e) } finally { savingPw.value = false }
}

// --- user list (everyone read-only; admin can act) ---
const users = ref<TeamUser[]>([])
const loadingUsers = ref(false)
async function loadUsers() {
  loadingUsers.value = true
  try { users.value = (await api.teamAccountUsers()) || [] }
  catch (e) { err(e) } finally { loadingUsers.value = false }
}

// Refresh with a guaranteed-visible spin: the fetch is usually too fast to see
// the icon rotate, so hold the spinning state for a short minimum.
const refreshing = ref(false)
async function refreshUsers() {
  if (refreshing.value) return
  refreshing.value = true
  try { await loadUsers() }
  finally { setTimeout(() => { refreshing.value = false }, 500) }
}

async function changeRole(u: TeamUser, role: string, el?: HTMLSelectElement) {
  if (role === u.role) return
  // Plain admins may only promote (never demote); surface it before the round-trip.
  if (!iAmSuperadmin.value && roleRank(role) <= roleRank(u.role)) {
    show('管理员只能提升用户等级，不能降级', 'warn')
    if (el) el.value = u.role // native <select> already moved; snap it back
    return
  }
  try { await api.teamSetUserRole(u.id, role); u.role = role as TeamUser['role']; ok(`${u.displayName} → ${roleLabel(role)}`) }
  catch (e) {
    // Server rejected it (e.g. 403): u.role is unchanged but the native select
    // is showing the attempted value, so revert the control, then re-sync the
    // whole list from the server — a 403 usually means our permission view was
    // stale (e.g. talking to a backend with an outdated session).
    err(e)
    if (el) el.value = u.role
    await loadUsers()
  }
}
async function toggleStatus(u: TeamUser) {
  const next = u.status === 'active' ? 'disabled' : 'active'
  const disabling = next === 'disabled'
  if (!(await confirm({
    title: disabling ? '禁用账号' : '启用账号',
    message: `确定${disabling ? '禁用' : '启用'}「${u.displayName}」吗？`,
    confirmText: disabling ? '禁用' : '启用',
    tone: disabling ? 'danger' : 'primary',
  }))) return
  try { await api.teamSetUserStatus(u.id, next); u.status = next; ok(disabling ? '已禁用' : '已启用') }
  catch (e) { err(e) }
}
async function resetPw(u: TeamUser) {
  const np = await prompt({
    title: '重置密码',
    message: `为「${u.displayName}」设置新密码`,
    placeholder: '至少 6 位',
    minLength: 6,
    confirmText: '重置',
  })
  if (np == null) return
  try { await api.teamResetUserPassword(u.id, np); ok('密码已重置') }
  catch (e) { err(e) }
}
async function deleteUser(u: TeamUser) {
  if (!(await confirm({
    title: '永久删除账号',
    message: `确定永久删除账号「${u.displayName}」（@${u.username}）吗？`,
    detail: '此操作不可恢复。',
    tone: 'danger',
    confirmText: '继续',
  }))) return
  const typed = await prompt({
    title: '二次确认',
    message: `请输入该用户的用户名「${u.username}」以删除：`,
    placeholder: u.username,
    requireMatch: u.username,
    tone: 'danger',
    confirmText: '永久删除',
  })
  if (typed == null) return
  try { await api.teamDeleteUser(u.id); ok('账号已删除'); await loadUsers() }
  catch (e) { err(e) }
}

// --- create account (admin) ---
const newUser = ref({ username: '', password: '', role: 'member', displayName: '' })
const creating = ref(false)
async function createUser() {
  const u = newUser.value
  if (!u.username.trim()) { show('请填用户名', 'warn'); return }
  if (u.password.length < 6) { show('密码至少 6 位', 'warn'); return }
  creating.value = true
  try {
    await api.teamCreateUser(u.username.trim(), u.password, u.role, u.displayName.trim())
    ok(`账号「${u.username.trim()}」已创建`)
    newUser.value = { username: '', password: '', role: 'member', displayName: '' }
    await loadUsers()
  } catch (e) { err(e) } finally { creating.value = false }
}

function roleLabel(r: string) {
  return r === 'superadmin' ? '超级管理员' : r === 'admin' ? '管理员' : r === 'reviewer' ? '校对' : '翻译'
}
function roleBadgeClass(r: string) {
  return r === 'superadmin'
    ? 'bg-secondary/15 text-secondary'
    : r === 'admin'
      ? 'bg-primary/15 text-primary'
      : r === 'reviewer'
        ? 'bg-info/15 text-info'
        : 'bg-[color-mix(in_oklch,var(--color-base-content)_10%,transparent)] text-[var(--color-text-secondary)]'
}

// Privilege ranking, mirrored from the server (model.RoleRank). Used to decide
// what the current actor may do to a given row — the server is still the source
// of truth, this just keeps the UI from offering actions it will reject.
const ROLE_RANK: Record<string, number> = { member: 0, reviewer: 1, admin: 2, superadmin: 3 }
function roleRank(r: string) { return ROLE_RANK[r] ?? -1 }

// The logged-in user's CURRENT role, taken from the authoritative server list
// (teamAccountUsers re-fetches from the server on every load) rather than the
// session object — which can be stale (a server-side role change, or a backend
// still holding a pre-change session). All permission gating below keys off this
// so the UI never offers actions the server will reject. (The server remains the
// final authority and re-checks every request against the live DB.)
const myId = computed(() => team.user?.id ?? '')
const me = computed(() => users.value.find((u) => u.id === myId.value) ?? null)
// Gate STRICTLY on the authoritative self entry. We deliberately do NOT fall back
// to team.user.role here — that cached session role is exactly what may be stale
// (e.g. a pre-migration 'superadmin'), so trusting it would re-expose management
// controls the server rejects. Until the server-loaded list confirms who we are,
// assume least privilege. (Fail closed: a brief moment of *fewer* controls on
// first load is fine; flashing elevated controls is not.)
const myRole = computed(() => me.value?.role ?? 'member')
const iAmSuperadmin = computed(() => myRole.value === 'superadmin')
const iAmAdmin = computed(() => myRole.value === 'admin' || myRole.value === 'superadmin')
// Display-only role for the header label — cosmetic, so a soft fallback to the
// cached session value while the list loads is fine.
const myDisplayRole = computed(() => me.value?.role ?? team.user?.role ?? 'member')

// Self-heal: if the cached session role drifts from the authoritative value,
// patch the store so the header label and the rest of the app (bulk-upload /
// review gating in other panels) stay consistent too.
watch(me, (m) => {
  if (m && team.user && (m.role !== team.user.role || m.status !== team.user.status)) {
    team.patchUser({ role: m.role, status: m.status })
  }
})

// canManage: may the logged-in user act on this row (role/status/password)?
// Superadmin manages anyone but themselves; a plain admin manages only
// members/reviewers (never another admin, the superadmin, or themselves).
function canManage(u: TeamUser): boolean {
  if (!iAmAdmin.value) return false
  if (u.id === myId.value) return false
  if (iAmSuperadmin.value) return true
  return roleRank(u.role) < roleRank('admin')
}

// Avatar colour: the user's own chosen colour if set, else a playful
// deterministic colour from the PJSK palette (so every member still gets a
// distinct avatar even before anyone customises it).
const PALETTE = ACCENT_GROUPS.flatMap((g) => g.members.map((m) => m.color))
function hashColor(seed: string) {
  let h = 0
  for (let i = 0; i < seed.length; i++) h = (h * 31 + seed.charCodeAt(i)) >>> 0
  return PALETTE[h % PALETTE.length]
}
function avatarBg(u: { id: string; avatarColor?: string }) {
  return u.avatarColor || hashColor(u.id)
}
function avatarText(hex: string) {
  const h = hex.replace('#', '')
  const ch = (i: number) => parseInt(h.slice(i, i + 2), 16) / 255
  const lin = (c: number) => (c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4))
  const L = 0.2126 * lin(ch(0)) + 0.7152 * lin(ch(2)) + 0.0722 * lin(ch(4))
  return L > 0.55 ? '#15131f' : '#ffffff'
}
function initial(name: string) {
  return (name.trim()[0] ?? '?').toUpperCase()
}

// --- my avatar colour picker (persisted server-side via the profile update) ---
const savingAvatar = ref(false)
const meAvatarBg = computed(() => me.value?.avatarColor || hashColor(myId.value || team.user?.id || ''))
function avatarSwatchStyle(c: string): Record<string, string> {
  const s: Record<string, string> = { backgroundColor: c }
  if (me.value?.avatarColor && me.value.avatarColor.toLowerCase() === c.toLowerCase()) {
    s.boxShadow = `0 0 0 2px var(--color-surface), 0 0 0 4px ${c}`
  }
  return s
}
async function setAvatarColor(color: string) {
  if (savingAvatar.value) return
  savingAvatar.value = true
  // Re-send the current display name (server requires it); only the colour changes.
  const name = me.value?.displayName || team.user?.displayName || displayName.value.trim()
  try {
    await api.teamUpdateProfile(name, color)
    team.patchUser({ avatarColor: color })
    await loadUsers()
    ok(color ? '头像颜色已更新' : '已恢复默认头像色')
  } catch (e) { err(e) } finally { savingAvatar.value = false }
}

onMounted(async () => {
  await team.refreshStatus().catch(() => {})
  if (team.loggedIn) await loadUsers()
})

// 登录态变化时(在下方面板登录/登出)刷新用户列表
watch(() => team.loggedIn, (v) => { if (v) { loadUsers() } else { users.value = [] } })
</script>

<template>
  <div class="min-h-screen page-bg text-[var(--color-text)]">
    <header class="sticky top-0 z-[var(--z-sticky)] bg-[color-mix(in_oklch,var(--color-bg)_82%,transparent)] backdrop-blur-md border-b border-[var(--color-border)]">
      <div class="max-w-4xl mx-auto px-6 h-14 flex items-center gap-3">
        <button @click="router.back()" class="icon-btn -ml-1"><ArrowLeft :size="18" /></button>
        <h1 class="text-base font-bold tracking-tight">账号中心</h1>
        <div v-if="team.user" class="ml-auto flex items-center gap-2">
          <span class="text-sm text-[var(--color-text-secondary)]">{{ team.user.displayName }}</span>
          <span class="app-chip" :class="roleBadgeClass(myDisplayRole)">
            <Crown v-if="myDisplayRole === 'superadmin'" :size="12" />
            <ShieldCheck v-else-if="myDisplayRole === 'admin'" :size="12" />
            {{ roleLabel(myDisplayRole) }}
          </span>
        </div>
      </div>
    </header>

    <main class="max-w-4xl mx-auto px-6 py-8 space-y-6">
      <!-- 团队术语库 -->
      <section class="app-card p-5" data-tour="team-panel">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-info/12 text-info"><Users :size="15" /></span>
          <div class="section-title">团队术语库</div>
        </div>
        <TeamModePanel />
      </section>

      <!-- 我的资料 -->
      <section v-if="team.loggedIn" class="app-card p-5">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-secondary/12 text-secondary"><IdCard :size="15" /></span>
          <div class="section-title">我的资料</div>
        </div>
        <div class="space-y-4">
          <div>
            <label class="app-label">头像颜色</label>
            <div class="flex items-center gap-3 mt-1.5">
              <span
                class="grid place-items-center w-11 h-11 rounded-full text-base font-bold shrink-0 shadow-[var(--shadow-sm)]"
                :style="{ backgroundColor: meAvatarBg, color: avatarText(meAvatarBg) }"
              >{{ initial(me?.displayName || team.user?.displayName || '?') }}</span>
              <div class="flex flex-wrap items-center gap-1.5">
                <button
                  v-for="c in PALETTE"
                  :key="c"
                  :title="c"
                  :disabled="savingAvatar"
                  class="w-6 h-6 rounded-full transition-transform hover:scale-110 disabled:opacity-60"
                  :style="avatarSwatchStyle(c)"
                  @click="setAvatarColor(c)"
                />
                <label
                  class="w-6 h-6 rounded-full border border-dashed border-[var(--color-border-strong)] grid place-items-center cursor-pointer text-[var(--color-text-secondary)] hover:text-[var(--color-text)]"
                  title="自定义颜色"
                >
                  <Palette :size="12" />
                  <input type="color" class="sr-only" @change="setAvatarColor(($event.target as HTMLInputElement).value)" />
                </label>
                <button
                  :disabled="savingAvatar"
                  class="h-6 px-2 rounded-full text-[0.68rem] border border-[var(--color-border)] text-[var(--color-text-secondary)] hover:border-[var(--color-border-strong)]"
                  @click="setAvatarColor('')"
                >自动</button>
              </div>
            </div>
          </div>
          <div class="app-divider" />
          <div>
            <label class="app-label">显示名（审核列表里别人看到的名字）</label>
            <div class="flex gap-2 mt-1.5">
              <input v-model="displayName" class="app-input flex-1" />
              <button @click="saveName" :disabled="savingName" class="btn btn-sm btn-ghost border border-[var(--color-border)] whitespace-nowrap">{{ savingName ? '保存中…' : '保存' }}</button>
            </div>
          </div>
          <div class="app-divider" />
          <div>
            <label class="app-label">修改密码</label>
            <div class="grid grid-cols-1 sm:grid-cols-3 gap-2 mt-1.5">
              <input v-model="oldPw" type="password" placeholder="当前密码" class="app-input" />
              <input v-model="newPw" type="password" placeholder="新密码（≥6位）" class="app-input" />
              <button @click="savePw" :disabled="savingPw" class="btn btn-sm btn-brand w-full">{{ savingPw ? '修改中…' : '修改密码' }}</button>
            </div>
          </div>
        </div>
      </section>

      <!-- 创建团队账号（仅管理员） -->
      <section v-if="iAmAdmin" class="app-card p-5" data-tour="acct-admin">
        <div class="flex items-center gap-2 mb-4">
          <span class="grid place-items-center w-7 h-7 rounded-lg bg-success/12 text-success"><UserPlus :size="15" /></span>
          <div class="section-title">创建团队账号</div>
        </div>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <div>
            <label class="app-label">用户名（登录用）</label>
            <input v-model="newUser.username" class="app-input mt-1.5" />
          </div>
          <div>
            <label class="app-label">初始密码（≥6位）</label>
            <input v-model="newUser.password" type="text" class="app-input mt-1.5" />
          </div>
          <div>
            <label class="app-label">显示名（留空=用户名）</label>
            <input v-model="newUser.displayName" class="app-input mt-1.5" />
          </div>
          <div>
            <label class="app-label">角色</label>
            <SkSelect
              class="mt-1.5"
              :model-value="newUser.role"
              @update:model-value="newUser.role = $event as string"
              :options="[
                { value: 'member', label: '翻译' },
                { value: 'reviewer', label: '校对' },
                ...(iAmSuperadmin ? [{ value: 'admin', label: '管理员' }] : []),
              ]"
            />
          </div>
        </div>
        <button @click="createUser" :disabled="creating" class="btn btn-sm btn-brand mt-4">
          <UserPlus :size="15" /> {{ creating ? '创建中…' : '创建账号' }}
        </button>
      </section>

      <!-- 用户列表 -->
      <section v-if="team.loggedIn" class="app-card p-5">
        <div class="flex items-center justify-between mb-4">
          <div class="flex items-center gap-2">
            <span class="grid place-items-center w-7 h-7 rounded-lg bg-accent/12 text-accent"><Users :size="15" /></span>
            <div class="section-title">团队用户 <span class="text-[var(--color-text-tertiary)] font-normal">· {{ users.length }}</span></div>
          </div>
          <button @click="refreshUsers" class="icon-btn" :class="{ 'animate-spin': refreshing }" title="刷新"><RefreshCw :size="15" /></button>
        </div>

        <div v-if="loadingUsers && !users.length" class="flex items-center justify-center gap-2 py-8 text-sm text-[var(--color-text-secondary)]">
          <span class="loading loading-spinner loading-sm" /> 加载中…
        </div>

        <ul v-else class="space-y-2">
          <li
            v-for="u in users"
            :key="u.id"
            class="flex items-center gap-3 rounded-[var(--radius-control)] border border-[var(--color-border)] bg-[var(--color-bg)] px-3 py-2.5 transition-colors hover:border-[var(--color-border-strong)]"
            :class="{ 'opacity-60': u.status === 'disabled' }"
          >
            <span
              class="grid place-items-center w-9 h-9 rounded-full text-sm font-bold shrink-0 shadow-[var(--shadow-sm)]"
              :style="{ backgroundColor: avatarBg(u), color: avatarText(avatarBg(u)) }"
            >{{ initial(u.displayName) }}</span>

            <div class="min-w-0 flex-1">
              <div class="text-sm font-semibold flex items-center gap-2 truncate">
                {{ u.displayName }}
                <span v-if="u.id === myId" class="app-chip bg-primary/12 text-primary">你</span>
                <span v-if="u.status === 'disabled'" class="app-chip bg-error/15 text-error">已禁用</span>
              </div>
              <div class="text-[11px] text-[var(--color-text-tertiary)]">@{{ u.username }}</div>
            </div>

            <!-- 可管理该用户时显示操作（超管可管理任何人；管理员仅能管理翻译/校对） -->
            <template v-if="canManage(u)">
              <!-- Controlled by :model-value="u.role" — on guard/failure u.role is
                   left unchanged so the control reverts on its own (no DOM hack). -->
              <SkSelect
                size="sm"
                :model-value="u.role"
                @update:model-value="changeRole(u, $event as string)"
                :options="[
                  { value: 'member', label: '翻译' },
                  { value: 'reviewer', label: '校对' },
                  ...(iAmSuperadmin ? [{ value: 'admin', label: '管理员' }] : []),
                  ...(u.role === 'superadmin' ? [{ value: 'superadmin', label: '超级管理员', disabled: true }] : []),
                ]"
              />
              <button @click="toggleStatus(u)" class="btn btn-ghost btn-xs gap-1" :title="u.status === 'active' ? '禁用' : '启用'">
                <Ban v-if="u.status === 'active'" :size="13" /><CircleCheck v-else :size="13" />
                <span class="hidden sm:inline">{{ u.status === 'active' ? '禁用' : '启用' }}</span>
              </button>
              <button @click="resetPw(u)" class="btn btn-ghost btn-xs gap-1" title="重置密码">
                <KeyRound :size="13" /><span class="hidden sm:inline">重置</span>
              </button>
              <button v-if="iAmSuperadmin" @click="deleteUser(u)" class="btn btn-ghost btn-xs gap-1 text-error hover:bg-error/10" title="删除">
                <Trash2 :size="13" />
              </button>
            </template>
            <span v-else class="app-chip min-w-[5.5rem] justify-center" :class="roleBadgeClass(u.role)">
              <Crown v-if="u.role === 'superadmin'" :size="11" />
              <ShieldCheck v-else-if="u.role === 'admin'" :size="11" />
              {{ roleLabel(u.role) }}
            </span>
          </li>
        </ul>
      </section>
    </main>
  </div>
</template>
