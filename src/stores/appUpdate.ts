import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { api } from '../api/client'

// Mirrors the Go service.PluginUpdateResult / AutoUpdateSummary.
export interface PluginUpdateResult {
  id: string
  name: string
  fromVersion?: string
  toVersion?: string
  error?: string
}
export interface AutoUpdateSummary {
  updated: PluginUpdateResult[]
  failed: PluginUpdateResult[]
}

// Mirrors the Go service.AppUpdateInfo.
export interface AppUpdateInfo {
  current: string
  latest: string
  updateAvailable: boolean
  notes?: string
  pubDate?: string
  downloadUrl?: string
  platform: string
}

function currentVersion(): string {
  return typeof __APP_VERSION__ !== 'undefined' ? __APP_VERSION__ : ''
}

const delay = (ms: number) => new Promise((r) => setTimeout(r, ms))

const isTauri = typeof window !== 'undefined' && !!(window as any).__TAURI_INTERNALS__

export type UpdatePhase = 'idle' | 'available' | 'downloading' | 'ready' | 'error'

export const useAppUpdateStore = defineStore('appUpdate', () => {
  const info = ref<AppUpdateInfo | null>(null)
  const phase = ref<UpdatePhase>('idle')
  const dismissed = ref(false)
  const read = ref(0)
  const total = ref(0)
  const downloadedPath = ref('')
  const errorMsg = ref('')
  const lastCheckAt = ref(0)
  const RECHECK_THROTTLE_MS = 30 * 60 * 1000 // refocus re-check at most every 30 min

  const percent = computed(() =>
    total.value > 0 ? Math.min(100, Math.round((read.value / total.value) * 100)) : 0,
  )
  // Banner shows once an update is available and the user hasn't dismissed it.
  const show = computed(() => !dismissed.value && phase.value !== 'idle' && !!info.value?.updateAvailable)

  // Check the manifest. Returns the outcome so a manual trigger can toast; the
  // silent boot/refocus callers ignore it. manual=true re-shows a dismissed banner
  // and never clobbers an in-flight download/ready phase.
  async function check(opts?: { manual?: boolean }): Promise<'available' | 'latest' | 'error'> {
    try {
      const got = await api.appUpdateCheck(currentVersion())
      lastCheckAt.value = Date.now()
      if (got.updateAvailable) {
        info.value = got
        if (opts?.manual) dismissed.value = false
        if (phase.value === 'idle' || phase.value === 'available') phase.value = 'available'
        return 'available'
      }
      return 'latest'
    } catch {
      return 'error'
    }
  }

  // Re-check on window refocus, throttled, so a long-running app still notices a
  // release without a restart. Skips while a download is in flight or ready.
  async function maybeRecheck(): Promise<void> {
    if (phase.value === 'downloading' || phase.value === 'ready') return
    if (lastCheckAt.value && Date.now() - lastCheckAt.value < RECHECK_THROTTLE_MS) return
    await check()
  }

  // Download the installer (mirror-accelerated) with progress, then mark ready.
  async function download(): Promise<void> {
    if (!info.value?.updateAvailable) return
    errorMsg.value = ''
    read.value = 0
    total.value = 0
    phase.value = 'downloading'
    try {
      const { taskId } = await api.appUpdateDownload(currentVersion())
      // Bounded poll: the backend caps the download at 30 min, so ~42 min of polls
      // is a safe ceiling that prevents an unbounded loop if a task never terminates.
      const maxPolls = 5000
      for (let i = 0; i < maxPolls; i++) {
        await delay(500)
        const p = await api.appUpdateDownloadProgress(taskId)
        read.value = p.read
        total.value = p.total
        if (p.status === 'done') {
          downloadedPath.value = p.filePath || ''
          phase.value = 'ready'
          return
        }
        if (p.status === 'error') {
          errorMsg.value = p.error || '下载失败'
          phase.value = 'error'
          return
        }
      }
      errorMsg.value = '下载超时'
      phase.value = 'error'
    } catch (e: any) {
      errorMsg.value = e?.message || '下载失败'
      phase.value = 'error'
    }
  }

  // Open the downloaded installer (mounts the .dmg / launches the installer), then
  // quit this instance so the new version can replace the running app without the
  // user manually quitting first. Returns true once the installer launched; Tauri
  // only — in a browser dev context there is nothing to quit.
  async function install(): Promise<boolean> {
    if (!downloadedPath.value) return false
    try {
      await api.appUpdateOpen(downloadedPath.value)
    } catch (e: any) {
      errorMsg.value = e?.message || '打开失败'
      phase.value = 'error'
      return false
    }
    if (isTauri) {
      // Small delay so the installer window (mounted .dmg / setup.exe) surfaces and
      // the "quitting" toast is visible before we exit.
      try {
        const { invoke } = await import('@tauri-apps/api/core')
        setTimeout(() => {
          invoke('quit_app').catch(() => {})
        }, 1500)
      } catch {
        /* not in Tauri / core unavailable → stay open, user quits manually */
      }
    }
    return true
  }

  function dismiss(): void {
    dismissed.value = true
  }

  // Silent plugin auto-update; returns the summary (caller toasts on updates).
  async function autoUpdatePlugins(): Promise<AutoUpdateSummary | null> {
    try {
      return await api.marketAutoUpdate(currentVersion())
    } catch {
      return null
    }
  }

  return {
    info,
    phase,
    dismissed,
    read,
    total,
    downloadedPath,
    errorMsg,
    percent,
    show,
    check,
    maybeRecheck,
    download,
    install,
    dismiss,
    autoUpdatePlugins,
  }
})
