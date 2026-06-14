import { ref } from 'vue'
import { api } from '../api/client'

const hintCache = new Map<string, string[]>()

export function useFlashbackTooltip() {
  const visible = ref(false)
  const tooltipStyle = ref<Record<string, string>>({})
  const clueGroups = ref<{ clue: string; hints: string[] }[]>([])

  let showTimer: ReturnType<typeof setTimeout> | null = null

  async function show(e: MouseEvent, clues: string[], lines?: number[]) {
    if (!clues || clues.length === 0) return

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

    const target = e.currentTarget as HTMLElement
    const rect = target.getBoundingClientRect()
    tooltipStyle.value = {
      position: 'fixed',
      left: rect.left + 'px',
      top: (rect.bottom + 4) + 'px',
      minWidth: Math.min(rect.width, 320) + 'px',
      maxWidth: '360px',
      zIndex: '9999',
    }

    if (showTimer) clearTimeout(showTimer)
    showTimer = setTimeout(() => {
      visible.value = true
    }, 300)
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
