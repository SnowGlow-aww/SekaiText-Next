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
  // Generation token for the pending/in-flight show. Bumped on every new show
  // intent and on hide(), so the async timer callback below can tell whether it
  // was superseded (e.g. mouseleave -> hide) while awaiting the appellation
  // lookup and must NOT flip visible on. clearTimeout can't cancel a fired timer.
  let showToken = 0

  // Show after the mouse rests on a term span ~0.4s. `el` is the hovered span,
  // `entry` the matched glossary entry, `speaker` the current line's speaker
  // (for an appellation suggestion). Rect is re-read just before positioning
  // (below) so the fixed coords stay attached to the term across the timer +
  // async lookup even if the editor scrolled in the meantime.
  function show(el: HTMLElement, entry: GlossaryEntry, speaker?: string) {
    if (showTimer || visible.value) return
    const token = ++showToken
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

      // Bail if this show was superseded (hidden / re-triggered) while awaiting
      // the appellation lookup above; otherwise the tooltip would pop up after
      // the mouse has already left and stay stuck there.
      if (token !== showToken) return

      tip.value = t
      // Re-read the rect now (after the 400ms delay + async lookup) so the
      // fixed position reflects the term's current viewport location, not
      // where it was when the mouse first stopped.
      const rect = el.getBoundingClientRect()
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
    // Invalidate any in-flight timer callback that is currently awaiting the
    // lookup, so it won't flip visible back on after we hide.
    showToken++
    if (showTimer) {
      clearTimeout(showTimer)
      showTimer = null
    }
    visible.value = false
  }

  return { visible, tooltipStyle, tip, show, hide }
}
