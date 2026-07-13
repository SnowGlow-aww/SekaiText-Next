import { ref, computed } from 'vue'
import { useGlossaryStore } from '../stores/glossary'
import { useDictStore } from '../stores/dict'
import type { GlossaryEntry } from '../types/glossary'

// Minimum source length (in code points) to include in matching. 1-char terms
// match all over the text and just create noise, so they're dropped.
const MIN_TERM_LEN = 2

// Bare honorific / filler suffixes that exist as standalone glossary rows (in
// the 人称 subcategory) but would match after every name and pollute the
// highlight. Full names and name+honorific combos (e.g. 司さん) are NOT in this
// set, so they still match and can drive appellation suggestions.
const STOPWORDS = new Set([
  'さん', 'ちゃん', 'くん', '君', '様', 'さま', 'っち', 'たん', 'ねぇ', 'ふふ',
])

export interface TermMatch {
  term: string
  entry: GlossaryEntry
  start: number
  end: number
}

// 字典取词命中：[start,end) 是原始行文本里的偏移（foldForMatch 保长，可直接
// 切片）；surface 是字典里的原样表面形（用作 data-dict-surface / 查词键）。
export interface DictHit {
  start: number
  end: number
  surface: string
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

// Fold a string for matching: compatibility-normalize (NFKC) + lowercase each
// code point so matching is case- and full/half-width-insensitive (e.g. ＶＩＶＩＤ
// and vivid both match a "Vivid" term, ！ matches !). A code point is only
// folded when it maps to a single code point of the SAME UTF-16 width; anything
// that would change length (ligatures like ﬀ→ff, ㍿→株式会社, …) is left as-is.
// This guarantees foldForMatch(s).length === s.length so regex match offsets map
// straight back onto the original text and highlight positions stay correct.
export function foldForMatch(s: string): string {
  let out = ''
  for (const ch of s) {
    const n = ch.normalize('NFKC').toLowerCase()
    out += n.length === ch.length && [...n].length === 1 ? n : ch
  }
  return out
}

// Module-level singleton state so every editor row shares one prebuilt regex.
const entriesLoaded = ref(false)
let combinedRe: RegExp | null = null
let termToEntry = new Map<string, GlossaryEntry>()
// The exact allEntries array the current regex/map were built from. Compared by
// identity (not length): the store swaps in a fresh array on every (re)load /
// sync / import, so this invalidates the cache on ANY content change — including
// edits that keep the entry count the same (changed source/translation, or a
// +1/-1 that nets out). A length check would miss those and keep stale data.
let builtFromEntries: GlossaryEntry[] | null = null

export function useGlossaryMatcher() {
  const glossary = useGlossaryStore()
  const dict = useDictStore()

  async function ensureLoaded() {
    if (entriesLoaded.value) return
    await glossary.loadAllEntries()
    entriesLoaded.value = true
    rebuild()
  }

  // Rebuild the combined regex from the current entry cache. Sources are sorted
  // longest-first so alternation prefers the longest match at a given position.
  function rebuild() {
    const entries = glossary.allEntries
    termToEntry = new Map()
    const sources: string[] = []
    for (const e of entries) {
      const src = (e.source || '').trim()
      if ([...src].length < MIN_TERM_LEN) continue
      if (STOPWORDS.has(src)) continue // bare honorifics/fillers: too noisy
      // Key by the folded source so the regex/map are case- and full/half-width-
      // insensitive; foldForMatch is length-preserving so offsets stay valid.
      const key = foldForMatch(src)
      if (!termToEntry.has(key)) {
        termToEntry.set(key, e)
        sources.push(key)
      }
    }
    sources.sort((a, b) => b.length - a.length)
    if (sources.length === 0) {
      combinedRe = null
    } else {
      combinedRe = new RegExp(sources.map(escapeRegExp).join('|'), 'g')
    }
    builtFromEntries = entries
  }

  // matchTerms returns non-overlapping term hits in `text`, in position order.
  function matchTerms(text: string): TermMatch[] {
    if (!text) return []
    if (builtFromEntries !== glossary.allEntries) rebuild()
    if (!combinedRe) return []
    const out: TermMatch[] = []
    // Match against the folded text; foldForMatch preserves length, so m.index
    // and the match length map straight onto the original `text`.
    const folded = foldForMatch(text)
    combinedRe.lastIndex = 0
    let m: RegExpExecArray | null
    while ((m = combinedRe.exec(folded)) !== null) {
      const key = m[0]
      const entry = termToEntry.get(key)
      // Report the ORIGINAL substring (not the folded key) so downstream
      // highlight re-search and hover lookup key off real on-screen text.
      if (entry) {
        const term = text.slice(m.index, m.index + key.length)
        out.push({ term, entry, start: m.index, end: m.index + key.length })
      }
      if (m.index === combinedRe.lastIndex) combinedRe.lastIndex++ // guard zero-width
    }
    return out
  }

  // matchDict：字典层取词（术语正则层完全不动）。对折叠后的行文本做最长匹配
  // 扫描：每个位置从 maxLen 往短探测 foldedMap，命中即记录并跳过该区间。
  // occupied 传入术语命中的区间，与之重叠的候选直接跳过（术语优先）；重叠时
  // 继续尝试更短的候选，所以术语右侧紧邻的短词仍能命中。
  function matchDict(text: string, occupied: { start: number; end: number }[] = []): DictHit[] {
    if (!text || !dict.surfacesLoaded || dict.foldedMap.size === 0) return []
    // foldForMatch 保长（同术语层），命中偏移可直接映射回原始文本。
    const folded = foldForMatch(text)
    const n = folded.length
    const out: DictHit[] = []
    let pos = 0
    while (pos < n) {
      // 首码元不在任何 surface 的首码元集合里 → 该位置不可能命中，直接跳过，
      // 省掉 maxLen 次子串分配+探测（日文行里绝大多数位置走这条快路）。
      if (!dict.firstChars.has(folded.charCodeAt(pos))) {
        pos += 1
        continue
      }
      let advanced = 1
      const limit = Math.min(dict.maxLen, n - pos)
      for (let L = limit; L >= 1; L--) {
        const end = pos + L
        if (occupied.some((r) => pos < r.end && end > r.start)) continue
        const surface = dict.foldedMap.get(folded.slice(pos, end))
        if (surface !== undefined) {
          out.push({ start: pos, end, surface })
          advanced = L
          break
        }
      }
      pos += advanced
    }
    return out
  }

  const ready = computed(() => entriesLoaded.value && combinedRe !== null)

  // lookup is called with the original on-screen term (data-term), so fold it to
  // match the folded keys stored in termToEntry.
  return { ensureLoaded, matchTerms, matchDict, ready, lookup: (t: string) => termToEntry.get(foldForMatch(t)) }
}
