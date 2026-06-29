<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RefreshCw, LogIn, LogOut, Plug, Unplug, UploadCloud, Server, Eye, ShieldCheck, Crown } from 'lucide-vue-next'
import { api } from '../../api/client'
import { useTeamStore } from '../../stores/team'
import { useGlossaryStore } from '../../stores/glossary'
import { useToast } from '../../composables/useToast'
import { useConfirm } from '../../composables/useConfirm'

const team = useTeamStore()
const glossary = useGlossaryStore()
const { show } = useToast()
const { confirm } = useConfirm()
const ok = (m: string) => show(m, 'success')
const err = (m: string) => show(m, 'error')

const serverUrl = ref('')
const username = ref('')
const password = ref('')
const busy = ref(false)

onMounted(async () => {
  try {
    const s = await team.refreshStatus()
    if (s.serverUrl) serverUrl.value = s.serverUrl
  } catch { /* backend may be starting */ }
})

async function doLogin() {
  if (!serverUrl.value.trim() || !username.value.trim() || !password.value) {
    err('请填写服务器地址、用户名和密码')
    return
  }
  busy.value = true
  try {
    const u = await team.login(serverUrl.value.trim(), username.value.trim(), password.value)
    password.value = ''
    ok(`已登录：${u.displayName}（${roleLabel(u.role)}）`)
    await refreshLocal()
  } catch (e) {
    err('登录失败：' + (e instanceof Error ? e.message : String(e)))
  } finally {
    busy.value = false
  }
}

async function doConnect() {
  if (!serverUrl.value.trim()) { err('请填写服务器地址'); return }
  busy.value = true
  try {
    await team.connect(serverUrl.value.trim())
    ok('已连接（只读模式）')
    await refreshLocal()
  } catch (e) {
    err('连接失败：' + (e instanceof Error ? e.message : String(e)))
  } finally {
    busy.value = false
  }
}

async function doLogout() {
  busy.value = true
  try {
    await team.logout()
    ok('已登出（切换为只读模式）')
  } finally {
    busy.value = false
  }
}

async function doDisconnect() {
  busy.value = true
  try {
    await team.disconnect()
    ok('已断开，恢复本地编辑')
  } finally {
    busy.value = false
  }
}

async function doSync() {
  busy.value = true
  try {
    const r = await team.sync(true)
    ok(r?.changed ? `已同步，术语库已更新（v${r.version}）` : '已是最新')
    await refreshLocal()
  } catch (e) {
    err('同步失败：' + (e instanceof Error ? e.message : String(e)))
  } finally {
    busy.value = false
  }
}

// Superadmin: push the entire LOCAL glossary up to the team server in one shot.
// Sync only pulls DOWN, so this is the only way to seed the shared server
// glossary from an admin's local copy. Idempotent (server upserts by entry ID).
async function doBulkUpload() {
  if (!(await confirm({
    title: '上传本地术语库',
    message: '将把本地术语库全部上传到团队服务器。',
    detail: '按条目 ID 覆盖更新，可重复执行。',
    tone: 'primary',
    confirmText: '上传',
  }))) return
  busy.value = true
  try {
    const data = await api.glossaryExport()
    const total = data.entries?.length ?? 0
    if (total === 0) { err('本地术语库为空，没有可上传的条目'); return }
    // Send the FULL local glossary (entries + appellations + grammar); the server
    // upserts each. data is GlossaryData straight from glossaryExport().
    const r = await api.teamBulkImport(data)
    ok(`已上传 ${r.upserted} / ${total} 条到服务器（v${r.version}）`)
    await team.sync(true)
    await refreshLocal()
  } catch (e) {
    err('上传失败：' + (e instanceof Error ? e.message : String(e)))
  } finally {
    busy.value = false
  }
}

async function refreshLocal() {
  await glossary.fetchCategories()
  await glossary.loadAllEntries(true)
  await glossary.loadSpeakers()
}

function roleLabel(role: string) {
  return role === 'superadmin' ? '超级管理员'
    : role === 'admin' ? '管理员'
    : role === 'reviewer' ? '校对'
    : '成员'
}
</script>

<template>
  <div class="text-left">
    <!-- 已登录 -->
    <div v-if="team.loggedIn" class="space-y-4">
      <div class="flex items-start justify-between gap-3">
        <div class="min-w-0">
          <div class="flex items-center gap-2">
            <span class="text-sm font-semibold truncate">{{ team.user?.displayName }}</span>
            <span class="app-chip bg-primary/12 text-primary">
              <Crown v-if="team.user?.role === 'superadmin'" :size="11" />
              <ShieldCheck v-else-if="team.user?.role === 'admin'" :size="11" />
              {{ roleLabel(team.user?.role || '') }}
            </span>
          </div>
          <div class="mt-0.5 flex items-center gap-1.5 text-xs text-[var(--color-text-tertiary)] font-mono truncate">
            <Server :size="12" class="shrink-0" />{{ team.serverUrl }}
          </div>
        </div>
        <div class="flex items-center gap-2 shrink-0">
          <button @click="doLogout" :disabled="busy" class="btn btn-sm btn-ghost border border-[var(--color-border)]">
            <LogOut :size="14" /> 登出
          </button>
          <button @click="doDisconnect" :disabled="busy" class="btn btn-sm btn-ghost text-error">
            <Unplug :size="14" /> 断开
          </button>
        </div>
      </div>

      <div class="app-divider" />

      <div class="flex items-center justify-between gap-3 flex-wrap">
        <div class="text-xs text-[var(--color-text-secondary)]">
          <template v-if="team.lastSync">
            上次同步：v{{ team.lastSync.version }}
            <span v-if="team.lastSync.changed" class="text-primary">（有更新）</span>
          </template>
          <template v-else>每 60 秒自动检查更新</template>
          <span v-if="team.syncError" class="text-error ml-2">{{ team.syncError }}</span>
        </div>
        <div class="flex items-center gap-2">
          <button v-if="team.isAdmin" @click="doBulkUpload" :disabled="busy"
            class="btn btn-sm btn-ghost border border-[var(--color-border)]" title="把本地术语库整体上传到团队服务器（仅管理员）">
            <UploadCloud :size="14" /> 上传本地术语库
          </button>
          <button @click="doSync" :disabled="busy" class="btn btn-sm btn-brand">
            <RefreshCw :size="14" /> 立即同步
          </button>
        </div>
      </div>
    </div>

    <!-- 只读模式:已连接未登录 -->
    <div v-else-if="team.readonly" class="space-y-4">
      <div class="flex items-start justify-between gap-3">
        <div class="min-w-0">
          <div class="flex items-center gap-2">
            <span class="text-sm font-semibold flex items-center gap-1.5"><Eye :size="14" /> 只读模式</span>
            <span class="app-chip bg-warning/15 text-warning">未登录</span>
          </div>
          <div class="mt-0.5 flex items-center gap-1.5 text-xs text-[var(--color-text-tertiary)] font-mono truncate">
            <Server :size="12" class="shrink-0" />{{ team.serverUrl }}
          </div>
        </div>
        <div class="flex items-center gap-2 shrink-0">
          <button @click="doSync" :disabled="busy" class="btn btn-sm btn-brand">
            <RefreshCw :size="14" /> 立即同步
          </button>
          <button @click="doDisconnect" :disabled="busy" class="btn btn-sm btn-ghost text-error">
            <Unplug :size="14" /> 断开
          </button>
        </div>
      </div>

      <div class="app-divider" />

      <div class="space-y-3">
        <p class="app-help">
          术语库正在自动同步（只读）。登录后即可新增/修改并提交审核。
        </p>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="app-label">用户名</label>
            <input v-model="username" type="text" autocomplete="username" class="app-input mt-1.5" />
          </div>
          <div>
            <label class="app-label">密码</label>
            <input v-model="password" type="password" autocomplete="current-password"
              @keyup.enter="doLogin" class="app-input mt-1.5" />
          </div>
        </div>
        <div class="flex justify-end">
          <button @click="doLogin" :disabled="busy" class="btn btn-sm btn-brand">
            <LogIn :size="14" /> 登录
          </button>
        </div>
      </div>
    </div>

    <!-- 未连接:填地址 → 登录 或 只读连接 -->
    <div v-else class="space-y-3">
      <p class="app-help">
        连接团队术语库服务器：登录后你的新增/修改会作为提案提交审核；也可只读连接，免登录浏览并自动同步。
      </p>
      <div>
        <label class="app-label">服务器地址</label>
        <input v-model="serverUrl" type="text" placeholder="https://your-server:8443"
          class="app-input mt-1.5 font-mono" />
      </div>
      <div class="grid grid-cols-2 gap-3">
        <div>
          <label class="app-label">用户名</label>
          <input v-model="username" type="text" autocomplete="username" class="app-input mt-1.5" />
        </div>
        <div>
          <label class="app-label">密码</label>
          <input v-model="password" type="password" autocomplete="current-password"
            @keyup.enter="doLogin" class="app-input mt-1.5" />
        </div>
      </div>
      <div class="flex justify-end gap-2">
        <button @click="doConnect" :disabled="busy" class="btn btn-sm btn-ghost border border-[var(--color-border)]">
          <Plug :size="14" /> 只读连接
        </button>
        <button @click="doLogin" :disabled="busy" class="btn btn-sm btn-brand">
          <LogIn :size="14" /> 登录
        </button>
      </div>
    </div>
  </div>
</template>
