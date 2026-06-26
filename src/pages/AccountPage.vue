<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowLeft } from 'lucide-vue-next'
import { useTeamStore } from '../stores/team'
import { useToast } from '../composables/useToast'
import { api } from '../api/client'
import TeamModePanel from '../components/settings/TeamModePanel.vue'
import type { TeamUser } from '../types/glossary'

const router = useRouter()
const team = useTeamStore()
const { show } = useToast()
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

async function changeRole(u: TeamUser, role: string, el?: HTMLSelectElement) {
  if (role === u.role) return
  // Plain admins may only promote (never demote); surface it before the round-trip.
  if (!iAmSuperadmin.value && roleRank(role) <= roleRank(u.role)) {
    show('管理员只能提升成员等级，不能降级', 'warn')
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
  if (!confirm(`确定${next === 'disabled' ? '禁用' : '启用'}「${u.displayName}」吗？`)) return
  try { await api.teamSetUserStatus(u.id, next); u.status = next; ok(next === 'disabled' ? '已禁用' : '已启用') }
  catch (e) { err(e) }
}
async function resetPw(u: TeamUser) {
  const np = prompt(`为「${u.displayName}」设置新密码（至少 6 位）：`)
  if (!np) return
  if (np.length < 6) { show('密码至少 6 位', 'warn'); return }
  try { await api.teamResetUserPassword(u.id, np); ok('密码已重置') }
  catch (e) { err(e) }
}
async function deleteUser(u: TeamUser) {
  if (!confirm(`确定永久删除账号「${u.displayName}」（@${u.username}）吗？此操作不可恢复。`)) return
  const typed = prompt(`二次确认：请输入该用户的用户名「${u.username}」以删除：`)
  if (typed !== u.username) { if (typed !== null) show('输入不匹配，已取消', 'warn'); return }
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
  return r === 'superadmin' ? '超级管理员' : r === 'admin' ? '管理员' : r === 'reviewer' ? '校对' : '成员'
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

onMounted(async () => {
  await team.refreshStatus().catch(() => {})
  if (team.loggedIn) await loadUsers()
})

// 登录态变化时(在下方面板登录/登出)刷新用户列表
watch(() => team.loggedIn, (v) => { if (v) { loadUsers() } else { users.value = [] } })
</script>

<template>
  <div class="min-h-screen bg-[var(--color-bg)] text-[var(--color-text)]">
    <header class="sticky top-0 z-10 bg-[var(--color-bg)]/90 backdrop-blur border-b border-[var(--color-border)]">
      <div class="max-w-4xl mx-auto px-6 h-14 flex items-center gap-3">
        <button @click="router.back()" class="p-1.5 rounded-lg hover:bg-[var(--color-surface)] text-[var(--color-text-secondary)]"><ArrowLeft :size="18" /></button>
        <h1 class="text-base font-semibold">账号中心</h1>
        <span v-if="team.user" class="ml-auto text-xs text-[var(--color-text-secondary)]">
          {{ team.user.displayName }} · {{ roleLabel(myDisplayRole) }}
        </span>
      </div>
    </header>

    <main class="max-w-4xl mx-auto px-6 py-6 space-y-6">
      <!-- 团队登录/登出/同步 -->
      <section>
        <h2 class="text-sm font-semibold mb-3">团队术语库</h2>
        <TeamModePanel />
      </section>

      <!-- 我的资料 -->
      <section v-if="team.loggedIn" class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-lg p-4">
        <h2 class="text-sm font-semibold mb-3">我的资料</h2>
        <div class="space-y-3">
          <div>
            <label class="text-xs text-[var(--color-text-secondary)]">显示名（审核列表里别人看到的名字）</label>
            <div class="flex gap-2 mt-1">
              <input v-model="displayName" class="flex-1 px-3 py-2 rounded-lg bg-[var(--color-bg)] border border-[var(--color-border)] text-sm" />
              <button @click="saveName" :disabled="savingName" class="btn btn-outline btn-sm whitespace-nowrap">{{ savingName ? '保存中…' : '保存' }}</button>
            </div>
          </div>
          <div class="border-t border-[var(--color-border)] pt-3">
            <label class="text-xs text-[var(--color-text-secondary)]">修改密码</label>
            <div class="grid grid-cols-1 sm:grid-cols-3 gap-2 mt-1">
              <input v-model="oldPw" type="password" placeholder="当前密码" class="px-3 py-2 rounded-lg bg-[var(--color-bg)] border border-[var(--color-border)] text-sm" />
              <input v-model="newPw" type="password" placeholder="新密码（≥6位）" class="px-3 py-2 rounded-lg bg-[var(--color-bg)] border border-[var(--color-border)] text-sm" />
              <button @click="savePw" :disabled="savingPw" class="btn btn-outline btn-sm">{{ savingPw ? '修改中…' : '修改密码' }}</button>
            </div>
          </div>
        </div>
      </section>

      <!-- 为成员创建账号（仅管理员） -->
      <section v-if="iAmAdmin" class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-lg p-4">
        <h2 class="text-sm font-semibold mb-3">为成员创建账号</h2>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <div>
            <label class="text-xs text-[var(--color-text-secondary)]">用户名（登录用）</label>
            <input v-model="newUser.username" class="block mt-1 w-full px-3 py-2 rounded-lg bg-[var(--color-bg)] border border-[var(--color-border)] text-sm" />
          </div>
          <div>
            <label class="text-xs text-[var(--color-text-secondary)]">初始密码（≥6位）</label>
            <input v-model="newUser.password" type="text" class="block mt-1 w-full px-3 py-2 rounded-lg bg-[var(--color-bg)] border border-[var(--color-border)] text-sm" />
          </div>
          <div>
            <label class="text-xs text-[var(--color-text-secondary)]">显示名（留空=用户名）</label>
            <input v-model="newUser.displayName" class="block mt-1 w-full px-3 py-2 rounded-lg bg-[var(--color-bg)] border border-[var(--color-border)] text-sm" />
          </div>
          <div>
            <label class="text-xs text-[var(--color-text-secondary)]">角色</label>
            <select v-model="newUser.role" class="block mt-1 w-full px-3 py-2 rounded-lg bg-[var(--color-bg)] border border-[var(--color-border)] text-sm">
              <option value="member">成员</option>
              <option value="reviewer">校对</option>
              <option v-if="iAmSuperadmin" value="admin">管理员</option>
            </select>
          </div>
        </div>
        <button @click="createUser" :disabled="creating" class="btn btn-primary btn-sm mt-3">{{ creating ? '创建中…' : '创建账号' }}</button>
      </section>

      <!-- 用户列表 -->
      <section v-if="team.loggedIn" class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-lg p-4">
        <div class="flex items-center justify-between mb-3">
          <h2 class="text-sm font-semibold">成员（{{ users.length }}）</h2>
          <button @click="loadUsers" class="text-xs text-[var(--color-text-secondary)] hover:text-[var(--color-text)]">刷新</button>
        </div>
        <div v-if="loadingUsers" class="text-sm text-[var(--color-text-secondary)] py-4 text-center">加载中…</div>
        <ul v-else class="space-y-1.5">
          <li v-for="u in users" :key="u.id" class="flex items-center gap-3 border border-[var(--color-border)] rounded px-3 py-2">
            <div class="min-w-0 flex-1">
              <div class="text-sm font-medium flex items-center gap-2">
                {{ u.displayName }}
                <span v-if="u.status === 'disabled'" class="text-[10px] px-1.5 py-0.5 rounded bg-error/15 text-error">已禁用</span>
              </div>
              <div class="text-[11px] text-[var(--color-text-secondary)]">@{{ u.username }}</div>
            </div>
            <!-- 可管理该成员时显示操作（超管可管理任何人；管理员仅能管理成员/校对） -->
            <template v-if="canManage(u)">
              <select :value="u.role" @change="changeRole(u, ($event.target as HTMLSelectElement).value, $event.target as HTMLSelectElement)"
                class="text-xs px-2 py-1 rounded bg-[var(--color-bg)] border border-[var(--color-border)]">
                <option value="member">成员</option>
                <option value="reviewer">校对</option>
                <option v-if="iAmSuperadmin" value="admin">管理员</option>
                <!-- never assignable; rendered only so a superadmin row can't show blank -->
                <option v-if="u.role === 'superadmin'" value="superadmin" disabled>超级管理员</option>
              </select>
              <button @click="toggleStatus(u)" class="text-xs text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]">{{ u.status === 'active' ? '禁用' : '启用' }}</button>
              <button @click="resetPw(u)" class="text-xs text-[var(--color-text-secondary)] hover:text-[var(--color-primary)]">重置密码</button>
              <button v-if="iAmSuperadmin" @click="deleteUser(u)" class="text-xs text-error/80 hover:text-error">删除</button>
            </template>
            <span v-else class="text-[11px] px-1.5 py-0.5 rounded bg-[var(--color-bg)] text-[var(--color-text-secondary)]">{{ roleLabel(u.role) }}</span>
          </li>
        </ul>
      </section>
    </main>
  </div>
</template>

