import { ref } from 'vue'
import { api } from '../api/client'

const hintCache = new Map<string, string[]>()

export function useFlashbackTooltip() {
  const visible = ref(false)
  const tooltipStyle = ref<Record<string, string>>({})
  const clueGroups = ref<{ clue: string; hints: string[] }[]>([])

  let showTimer: ReturnType<typeof setTimeout> | null = null

  // Show the flashback tooltip after the mouse rests in the box ~0.5s. Callable
  // from both mouseenter and mousemove: it's idempotent while a timer is pending
  // or the tooltip is already shown, so moving the mouse inside the box does NOT
  // restart the countdown (which felt sluggish), yet a move re-arms it if the
  // box was entered without a mouseenter firing. The rect is captured up front
  // because e.currentTarget is null once the async fetch resolves.
  function show(e: MouseEvent, clues: string[], lines?: number[]) {
    if (!clues || clues.length === 0) return
    if (showTimer || visible.value) return
    const target = e.currentTarget as HTMLElement | null
    if (!target) return
    const rect = target.getBoundingClientRect()

    showTimer = setTimeout(async () => {
      const newHints: Record<string, string[]> = {}
      for (let i = 0; i < clues.length; i++) {
        const clue = clues[i]
        if (!clue) continue
        const line = lines?.[i] ?? 0
        // Cache key includes the line so the same clue at different source lines
        // doesn't collide.
        const cacheKey = line > 0 ? `${clue}#${line}` : clue
        if (hintCache.has(cacheKey)) {
          newHints[clue] = hintCache.get(cacheKey)!
          continue
        }
        try {
          const res = await api.clueHints(clue)
          let hints = res.hints?.length ? res.hints : [clue]
          if (line > 0) hints = [...hints, `源剧情第 ${line} 行`]
          hintCache.set(cacheKey, hints)
          newHints[clue] = hints
        } catch {
          newHints[clue] = line > 0 ? [clue, `源剧情第 ${line} 行`] : [clue]
        }
      }

      clueGroups.value = Object.entries(newHints).map(([clue, hints]) => ({ clue, hints }))
      tooltipStyle.value = {
        position: 'fixed',
        left: rect.left + 'px',
        top: (rect.bottom + 4) + 'px',
        minWidth: Math.min(rect.width, 320) + 'px',
        maxWidth: '360px',
        zIndex: '9999',
      }
      visible.value = true
    }, 500)
  }

  function hide() {
    if (showTimer) {
      clearTimeout(showTimer)
      showTimer = null
    }
    visible.value = false
  }

  return { visible, tooltipStyle, clueGroups, show, hide }
}
