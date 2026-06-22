<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useTeamStore } from '../../stores/team'
import { useGlossaryStore } from '../../stores/glossary'
import { useToast } from '../../composables/useToast'

const team = useTeamStore()
const glossary = useGlossaryStore()
const { show } = useToast()
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

async function refreshLocal() {
  await glossary.fetchCategories()
  await glossary.loadAllEntries(true)
  await glossary.loadSpeakers()
}

function roleLabel(role: string) {
  return role === 'superadmin' ? '超级管理员' : role === 'reviewer' ? '校对/管理员' : '成员'
}
</script>

<template>
  <div class="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-xl p-6 text-left">
    <!-- 已登录 -->
    <div v-if="team.loggedIn">
      <div class="flex items-center justify-between mb-4">
        <div>
          <div class="text-sm font-medium">{{ team.user?.displayName }}
            <span class="ml-2 text-xs px-2 py-0.5 rounded-full bg-[var(--color-primary)]/15 text-[var(--color-primary)]">
              {{ roleLabel(team.user?.role || '') }}
            </span>
          </div>
          <div class="text-xs text-[var(--color-text-secondary)] mt-0.5 font-mono">{{ team.serverUrl }}</div>
        </div>
        <div class="flex items-center gap-2">
          <button @click="doLogout" :disabled="busy" class="btn btn-ghost btn-sm">登出</button>
          <button @click="doDisconnect" :disabled="busy" class="btn btn-ghost btn-sm text-error/80">断开</button>
        </div>
      </div>
      <div class="flex items-center justify-between border-t border-[var(--color-border)] pt-3">
        <div class="text-xs text-[var(--color-text-secondary)]">
          <template v-if="team.lastSync">
            上次同步：v{{ team.lastSync.version }}
            <span v-if="team.lastSync.changed" class="text-[var(--color-primary)]">（有更新）</span>
          </template>
          <template v-else>每 60 秒自动检查更新</template>
          <span v-if="team.syncError" class="text-error ml-2">{{ team.syncError }}</span>
        </div>
        <button @click="doSync" :disabled="busy" class="btn btn-primary btn-sm">立即同步</button>
      </div>
    </div>

    <!-- 只读模式:已连接未登录 -->
    <div v-else-if="team.readonly">
      <div class="flex items-center justify-between mb-4">
        <div>
          <div class="text-sm font-medium flex items-center gap-2">
            只读模式
            <span class="text-xs px-2 py-0.5 rounded-full bg-amber-400/15 text-amber-500">未登录</span>
          </div>
          <div class="text-xs text-[var(--color-text-secondary)] mt-0.5 font-mono">{{ team.serverUrl }}</div>
        </div>
        <div class="flex items-center gap-2">
          <button @click="doSync" :disabled="busy" class="btn btn-primary btn-sm">立即同步</button>
          <button @click="doDisconnect" :disabled="busy" class="btn btn-ghost btn-sm text-error/80">断开</button>
        </div>
      </div>
      <div class="border-t border-[var(--color-border)] pt-3 space-y-3">
        <p class="text-xs text-[var(--color-text-secondary)]">
          术语库正在自动同步（只读）。登录后即可新增/修改并提交审核。
        </p>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="text-xs font-medium block mb-1">用户名</label>
            <input v-model="username" type="text" autocomplete="username"
              class="input input-bordered input-sm w-full" />
          </div>
          <div>
            <label class="text-xs font-medium block mb-1">密码</label>
            <input v-model="password" type="password" autocomplete="current-password"
              @keyup.enter="doLogin" class="input input-bordered input-sm w-full" />
          </div>
        </div>
        <div class="flex justify-end">
          <button @click="doLogin" :disabled="busy" class="btn btn-primary btn-sm">登录</button>
        </div>
      </div>
    </div>

    <!-- 未连接:填地址 → 登录 或 只读连接 -->
    <div v-else class="space-y-3">
      <p class="text-xs text-[var(--color-text-secondary)]">
        连接团队术语库服务器：登录后你的新增/修改会作为提案提交审核；也可只读连接，免登录浏览并自动同步。
      </p>
      <div>
        <label class="text-xs font-medium block mb-1">服务器地址</label>
        <input v-model="serverUrl" type="text" placeholder="https://your-server:8443"
          class="input input-bordered input-sm w-full font-mono" />
      </div>
      <div class="grid grid-cols-2 gap-3">
        <div>
          <label class="text-xs font-medium block mb-1">用户名</label>
          <input v-model="username" type="text" autocomplete="username"
            class="input input-bordered input-sm w-full" />
        </div>
        <div>
          <label class="text-xs font-medium block mb-1">密码</label>
          <input v-model="password" type="password" autocomplete="current-password"
            @keyup.enter="doLogin" class="input input-bordered input-sm w-full" />
        </div>
      </div>
      <div class="flex justify-end gap-2">
        <button @click="doConnect" :disabled="busy" class="btn btn-ghost btn-sm">只读连接</button>
        <button @click="doLogin" :disabled="busy" class="btn btn-primary btn-sm">登录</button>
      </div>
    </div>
  </div>
</template>
