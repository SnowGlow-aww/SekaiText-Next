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

export type UpdatePhase = 'idle' | 'available' | 'downloading' | 'ready' | 'error'

export const useAppUpdateStore = defineStore('appUpdate', () => {
  const info = ref<AppUpdateInfo | null>(null)
  const phase = ref<UpdatePhase>('idle')
  const dismissed = ref(false)
  const read = ref(0)
  const total = ref(0)
  const downloadedPath = ref('')
  const errorMsg = ref('')

  const percent = computed(() =>
    total.value > 0 ? Math.min(100, Math.round((read.value / total.value) * 100)) : 0,
  )
  // Banner shows once an update is available and the user hasn't dismissed it.
  const show = computed(() => !dismissed.value && phase.value !== 'idle' && !!info.value?.updateAvailable)

  // Silent on-boot check. Network/mirror failure is swallowed (offline is normal).
  async function check(): Promise<void> {
    try {
      const got = await api.appUpdateCheck(currentVersion())
      if (got.updateAvailable) {
        info.value = got
        phase.value = 'available'
      }
    } catch {
      /* offline / mirror down — stay silent */
    }
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
      for (;;) {
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
    } catch (e: any) {
      errorMsg.value = e?.message || '下载失败'
      phase.value = 'error'
    }
  }

  // Open the downloaded installer (mounts the .dmg / launches the installer).
  async function install(): Promise<void> {
    if (!downloadedPath.value) return
    try {
      await api.appUpdateOpen(downloadedPath.value)
    } catch (e: any) {
      errorMsg.value = e?.message || '打开失败'
      phase.value = 'error'
    }
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
    download,
    install,
    dismiss,
    autoUpdatePlugins,
  }
})
