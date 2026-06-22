import { ref, computed } from 'vue'
import { useGlossaryStore } from '../stores/glossary'
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

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

// Module-level singleton state so every editor row shares one prebuilt regex.
const entriesLoaded = ref(false)
let combinedRe: RegExp | null = null
let termToEntry = new Map<string, GlossaryEntry>()
let builtFromCount = -1

export function useGlossaryMatcher() {
  const glossary = useGlossaryStore()

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
      if (!termToEntry.has(src)) {
        termToEntry.set(src, e)
        sources.push(src)
      }
    }
    sources.sort((a, b) => b.length - a.length)
    if (sources.length === 0) {
      combinedRe = null
    } else {
      combinedRe = new RegExp(sources.map(escapeRegExp).join('|'), 'g')
    }
    builtFromCount = entries.length
  }

  // matchTerms returns non-overlapping term hits in `text`, in position order.
  function matchTerms(text: string): TermMatch[] {
    if (!text) return []
    if (builtFromCount !== glossary.allEntries.length) rebuild()
    if (!combinedRe) return []
    const out: TermMatch[] = []
    combinedRe.lastIndex = 0
    let m: RegExpExecArray | null
    while ((m = combinedRe.exec(text)) !== null) {
      const term = m[0]
      const entry = termToEntry.get(term)
      if (entry) out.push({ term, entry, start: m.index, end: m.index + term.length })
      if (m.index === combinedRe.lastIndex) combinedRe.lastIndex++ // guard zero-width
    }
    return out
  }

  const ready = computed(() => entriesLoaded.value && combinedRe !== null)

  return { ensureLoaded, matchTerms, ready, lookup: (t: string) => termToEntry.get(t) }
}
