import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '../api/client'
import type { TeamUser, Proposal } from '../types/glossary'

// Team mode store: holds login state (mirrored from the local backend, which
// owns the actual tokens) and drives periodic sync of the authoritative
// glossary. The frontend never sees the JWT — it only calls localhost.
export const useTeamStore = defineStore('team', () => {
  const user = ref<TeamUser | null>(null)
  const serverUrl = ref('')
  const connected = ref(false)
  const loading = ref(false)
  const lastSync = ref<{ at: number; changed: boolean; version: number } | null>(null)
  const syncError = ref('')

  const loggedIn = computed(() => user.value !== null)
  // readonly: connected to a server but not logged in (no-login readonly mode)
  const readonly = computed(() => connected.value && user.value === null)
  const isReviewer = computed(
    () => user.value?.role === 'reviewer' || user.value?.role === 'admin' || user.value?.role === 'superadmin',
  )
  // isAdmin: may open the admin panel (create accounts, manage members, bulk-upload).
  // Both the sole superadmin and plain admins qualify; the backend further limits
  // what a plain admin may do to a given target.
  const isAdmin = computed(() => user.value?.role === 'admin' || user.value?.role === 'superadmin')
  // isSuperadmin: the single top-tier account (@admin / Server). Gates the
  // superadmin-only UI (delete account, granting the 管理员 role, acting on admins).
  const isSuperadmin = computed(() => user.value?.role === 'superadmin')

  let timer: ReturnType<typeof setInterval> | null = null

  async function refreshStatus() {
    const s = await api.teamStatus()
    user.value = s.loggedIn ? s.user : null
    serverUrl.value = s.serverUrl
    connected.value = s.connected
    if (connected.value) startPolling()
    else stopPolling()
    return s
  }

  async function login(url: string, username: string, password: string) {
    loading.value = true
    try {
      const r = await api.teamLogin(url, username, password)
      user.value = r.user
      serverUrl.value = url
      connected.value = true
      // Login already succeeded (the backend holds the token); a failed first
      // sync (remote glossary-server briefly unreachable/timing out) must not
      // surface as a failed login. Record the sync error but still start
      // polling so it retries automatically, and return successfully.
      try {
        await sync(true)
      } catch {
        // syncError is already set by sync(); polling will retry.
      }
      startPolling()
      return r.user
    } finally {
      loading.value = false
    }
  }

  // connect: enter no-login readonly mode against a server URL.
  async function connect(url: string) {
    loading.value = true
    try {
      await api.teamConnect(url)
      serverUrl.value = url
      connected.value = true
      user.value = null
      // Connect already succeeded; a failed first sync must not surface as a
      // failed connect. Record the sync error but still start polling so it
      // retries automatically.
      try {
        await sync(true)
      } catch {
        // syncError is already set by sync(); polling will retry.
      }
      startPolling()
    } finally {
      loading.value = false
    }
  }

  // logout: drop to readonly mode (still connected & synced).
  async function logout() {
    // Always drop local login state, even if the backend call rejects — the
    // intent is to leave the logged-in session; a stuck user.value would keep
    // loggedIn true with no way to recover.
    try {
      await api.teamLogout()
    } finally {
      user.value = null
    }
    // stays connected in readonly mode; polling continues
  }

  // disconnect: fully leave team mode (back to pure local editing).
  async function disconnect() {
    stopPolling()
    // Always tear down the local session, even if the backend call rejects;
    // polling is already stopped, so leaving connected true would strand a
    // dead, non-recoverable session in the UI.
    try {
      await api.teamDisconnect()
    } finally {
      user.value = null
      connected.value = false
      serverUrl.value = ''
      lastSync.value = null
    }
  }

  async function sync(force = false) {
    if (!connected.value) return
    try {
      const r = await api.teamSync(force)
      lastSync.value = { at: Date.now(), changed: r.changed, version: r.version }
      syncError.value = ''
      return r
    } catch (e) {
      syncError.value = e instanceof Error ? e.message : String(e)
      throw e
    }
  }

  function startPolling(intervalMs = 60_000) {
    stopPolling()
    timer = setInterval(() => void sync(false).catch(() => {}), intervalMs)
  }

  function stopPolling() {
    if (timer) {
      clearInterval(timer)
      timer = null
    }
  }

  // patchUser reconciles the cached session user in place — e.g. when the
  // authoritative user list reveals the role drifted (a server-side role change,
  // or a backend still holding a pre-change session). No-op when not logged in.
  function patchUser(patch: Partial<TeamUser>) {
    if (user.value) user.value = { ...user.value, ...patch }
  }

  // Proposal helpers
  async function submitProposal(p: {
    kind: 'add' | 'edit' | 'delete'
    targetId?: string
    category: string
    payload: unknown
    baseVersion?: number
  }): Promise<Proposal> {
    return api.teamCreateProposal({ ...p, targetType: 'entry' })
  }

  return {
    user, serverUrl, connected, loading, lastSync, syncError,
    loggedIn, readonly, isReviewer, isAdmin, isSuperadmin,
    refreshStatus, login, connect, logout, disconnect, sync, startPolling, stopPolling, submitProposal, patchUser,
  }
})
