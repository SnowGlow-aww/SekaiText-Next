import { ref } from 'vue'
import { api } from '../api/client'
import { useDictStore } from '../stores/dict'
import type { GlossaryEntry, DictLookupHit } from '../types/glossary'

// Cache appellation lookups by "speaker\x00target" so hovering the same name
// repeatedly doesn't re-hit the backend.
const appellCache = new Map<string, { jp?: string; cn?: string } | null>()

export interface GlossaryTip {
  // 卡片类型：术语卡（默认，undefined 视同 'term'）/ 字典卡。模板按此分支。
  kind?: 'term' | 'dict'
  source: string
  translation: string
  aliases?: string[]
  note?: string
  category?: string
  // appellation suggestion (filled when the term is a character the speaker addresses)
  appellSpeaker?: string
  appellCn?: string
  appellJp?: string
  // 字典卡片（kind === 'dict'）：命中的义项 + 所属字典名（多本时顿号连接）
  dictHits?: DictLookupHit[]
  dictNames?: string
}

export function useGlossaryTooltip() {
  const dict = useDictStore()
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

  // 字典取词卡片：同款 400ms 延迟 + 异步查词（dictStore.lookup 带缓存，同
  // appellation 的异步模式）+ token 防「移开后才弹出」。定位与术语卡片完全一致
  // （同 maxWidth，同类框体宽度一致）。
  function showDict(el: HTMLElement, surface: string) {
    if (showTimer || visible.value) return
    const token = ++showToken
    showTimer = setTimeout(async () => {
      let hits: DictLookupHit[] = []
      try {
        hits = await dict.lookup(surface)
      } catch {
        hits = []
      }
      if (token !== showToken) return
      // 查无义项（如删字典后的陈旧命中）：不弹卡片；释放 timer 允许再次触发。
      if (hits.length === 0) {
        showTimer = null
        return
      }
      tip.value = {
        kind: 'dict',
        source: surface,
        translation: '',
        dictHits: hits,
        dictNames: Array.from(new Set(hits.map((h) => h.dictName))).join('、'),
      }
      // 与 show() 相同：延迟 + 查词后重读 rect，位置跟住滚动后的词。
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
    cancelScheduledHide()
    if (showTimer) {
      clearTimeout(showTimer)
      showTimer = null
    }
    visible.value = false
  }

  // —— 字典卡片可交互（滚动长释义）所需的宽限隐藏 ——
  // 术语卡是 pointer-events-none 即离即隐；字典卡释义可达 4000+ 字要能滚动，
  // 卡片开启指针事件后，鼠标从词移进卡片会先触发源区 mouseleave——立即隐藏
  // 就永远进不了卡片。softHide 对可见的字典卡给 200ms 宽限，进卡片时
  // cancelScheduledHide 保住它；其余情况维持原即离即隐行为。
  let hideTimer: ReturnType<typeof setTimeout> | null = null
  function cancelScheduledHide() {
    if (hideTimer) {
      clearTimeout(hideTimer)
      hideTimer = null
    }
  }
  function softHide() {
    if (visible.value && tip.value?.kind === 'dict') {
      cancelScheduledHide()
      hideTimer = setTimeout(() => {
        hideTimer = null
        hide()
      }, 200)
    } else {
      hide()
    }
  }

  return { visible, tooltipStyle, tip, show, showDict, hide, softHide, cancelScheduledHide }
}
