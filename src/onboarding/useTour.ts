import { ref } from 'vue'
import { useSettingsStore } from '../stores/settings'

export interface TourStep {
  /** 高亮目标的 CSS 选择器；缺省 = 居中卡片（欢迎/收尾/纯说明步）。 */
  selector?: string
  /** 展示本步前需要跳转到的路由。 */
  route?: string
  title: string
  body: string
  /** 可选外链按钮（官网指南等），用 openExternal 打开。 */
  link?: { label: string; url: string }
}

export interface TourDef {
  /** 记入 settings.seenTours 的唯一 id，如 "app-welcome"、"plugin:live2d"。 */
  id: string
  /** 结束时一并记为已看的附加 id。用于把多段导览拼成一次连续播放（如术语库
   *  按角色分层的 base+翻译+校对+管理员），完成/跳过后各层都不再重复弹出。 */
  alsoMarks?: string[]
  steps: TourStep[]
}

// 模块级单例：任何页面 useTour() 拿到的都是同一份状态。
const active = ref<TourDef | null>(null)

export function useTour() {
  const settings = useSettingsStore()

  function seen(id: string): boolean {
    return (settings.settings.seenTours ?? []).includes(id)
  }

  function markSeen(id: string) {
    const list = settings.settings.seenTours ?? []
    if (!list.includes(id)) {
      settings.settings.seenTours = [...list, id]
      settings.saveSettings().catch(() => {})
    }
  }

  /** 无条件开始（设置页「重新查看导览」用）。 */
  function start(def: TourDef) {
    active.value = def
  }

  /** 只在没看过且当前无其他导览时开始（首启/插件首次进入/版本更新用）。 */
  function startOnce(def: TourDef) {
    if (active.value || seen(def.id)) return
    active.value = def
  }

  /** 结束当前导览；完成与跳过都记为已看（含 alsoMarks 的各层 id）。 */
  function finish() {
    if (active.value) {
      markSeen(active.value.id)
      for (const id of active.value.alsoMarks ?? []) markSeen(id)
    }
    active.value = null
  }

  return { active, seen, markSeen, start, startOnce, finish }
}
