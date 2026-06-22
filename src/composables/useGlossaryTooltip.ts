import { ref } from 'vue'
import { api } from '../api/client'
import type { GlossaryEntry } from '../types/glossary'

// Cache appellation lookups by "speaker\x00target" so hovering the same name
// repeatedly doesn't re-hit the backend.
const appellCache = new Map<string, { jp?: string; cn?: string } | null>()

export interface GlossaryTip {
  source: string
  translation: string
  aliases?: string[]
  note?: string
  category?: string
  // appellation suggestion (filled when the term is a character the speaker addresses)
  appellSpeaker?: string
  appellCn?: string
  appellJp?: string
}

export function useGlossaryTooltip() {
  const visible = ref(false)
  const tooltipStyle = ref<Record<string, string>>({})
  const tip = ref<GlossaryTip | null>(null)

  let showTimer: ReturnType<typeof setTimeout> | null = null

  // Show after the mouse rests on a term span ~0.4s. `el` is the hovered span,
  // `entry` the matched glossary entry, `speaker` the current line's speaker
  // (for an appellation suggestion). Rect captured up front (async lookup below).
  function show(el: HTMLElement, entry: GlossaryEntry, speaker?: string) {
    if (showTimer || visible.value) return
    const rect = el.getBoundingClientRect()
    showTimer = setTimeout(async () => {
      const t: GlossaryTip = {
        source: entry.source,
        translation: entry.translation,
        aliases: entry.aliases,
        note: entry.note,
        category: entry.category,
      }
      // If the hovered term looks like a character name and we know who's
      // speaking, suggest how the speaker addresses them.
      if (speaker && speaker.trim() && speaker !== entry.source) {
        const key = `${speaker}\x00${entry.source}`
        let res = appellCache.get(key)
        if (res === undefined) {
          try {
            const r = await api.glossaryAppellationLookup(speaker, entry.source)
            res = r.found ? { jp: r.jp, cn: r.cn } : null
          } catch {
            res = null
          }
          appellCache.set(key, res)
        }
        if (res) {
          t.appellSpeaker = speaker
          t.appellJp = res.jp
          t.appellCn = res.cn
        }
      }
      tip.value = t
      tooltipStyle.value = {
        position: 'fixed',
        left: rect.left + 'px',
        top: rect.bottom + 4 + 'px',
        maxWidth: '320px',
        zIndex: '9999',
      }
      visible.value = true
    }, 400)
  }

  function hide() {
    if (showTimer) {
      clearTimeout(showTimer)
      showTimer = null
    }
    visible.value = false
  }

  return { visible, tooltipStyle, tip, show, hide }
}
