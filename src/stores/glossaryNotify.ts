import { defineStore } from 'pinia'
import { ref, computed, watch } from 'vue'
import { api } from '../api/client'
import { useTeamStore } from './team'

// 术语库侧栏「呼吸灯」通知的数据源：
//  - 审核员/管理员：有提案待审核（pendingCount > 0，实时工作量，审完自然熄灭）
//  - 所有成员：自己的提案在「上次打开术语库之后」有新通过的（approvedUnseen，
//    打开术语库页即视为已读）
// 轮询术语库协作服务器（60s 一轮 + 登录状态变化立即刷）；未登录时熄灭。
export const useGlossaryNotifyStore = defineStore('glossaryNotify', () => {
  const team = useTeamStore()
  const pendingCount = ref(0)
  const approvedUnseen = ref(false)
  // 最近一轮里「已通过」提案的最大审核时间，markApprovedSeen 以它为已读水位。
  // 用服务器时间戳而非本机 Date.now()，时钟偏差不会造成漏报/误报。
  let newestApprovedAt = 0

  const active = computed(() => pendingCount.value > 0 || approvedUnseen.value)
  const tooltip = computed(() => {
    const parts: string[] = []
    if (pendingCount.value > 0) parts.push(`${pendingCount.value} 条术语提案待审核`)
    if (approvedUnseen.value) parts.push('你的术语提案已通过')
    return parts.join('；')
  })

  // 已读水位按 服务器+账号 存，换号/换服务器互不串。
  function seenKey(): string {
    return `glossaryNotify:seenApprovedAt:${team.serverUrl}|${team.user?.id || ''}`
  }

  async function poll() {
    if (!team.loggedIn) {
      pendingCount.value = 0
      approvedUnseen.value = false
      return
    }
    // 两个请求各自兜底：服务器暂不可达时保持上次状态，别把灯闪没了。
    try {
      pendingCount.value = team.isReviewer ? (await api.teamPendingProposals()).length : 0
    } catch { /* keep last value */ }
    try {
      const mine = await api.teamMyProposals()
      newestApprovedAt = mine.reduce(
        (m, p) => (p.status === 'approved' ? Math.max(m, p.reviewedAt || p.createdAt || 0) : m),
        0,
      )
      const seen = Number(localStorage.getItem(seenKey()) || 0)
      approvedUnseen.value = newestApprovedAt > seen
    } catch { /* keep last value */ }
  }

  /** 打开术语库页即已读「提案通过」；待审核数是真实工作量，不在这里清零。 */
  function markApprovedSeen() {
    if (newestApprovedAt > 0) {
      try { localStorage.setItem(seenKey(), String(newestApprovedAt)) } catch { /* ignore */ }
    }
    approvedUnseen.value = false
  }

  let timer: ReturnType<typeof setInterval> | null = null
  function start() {
    if (timer) return
    timer = setInterval(() => { void poll() }, 60000)
    void poll()
    watch(() => team.loggedIn, () => { void poll() })
  }

  return { pendingCount, approvedUnseen, active, tooltip, poll, markApprovedSeen, start }
})
