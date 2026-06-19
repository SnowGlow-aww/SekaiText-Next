// Flashback detection shared by SourcePanel and EditorWorkspace so the two views
// never diverge. A line is a flashback only when it references an EARLIER story
// chapter — not a forward teaser of a later episode, and not a non-story voice
// (login bonus, area talk, etc.) that merely shares the same event prefix.
import type { SourceTalk } from '../types/translation'

// A flashback points BACKWARD (an earlier scene). Rules, in order:
//   1. No major clue yet -> treat as flashback (degrade safely).
//   2. Clue whose last segment isn't a numeric chapter (e.g. "ev_wl_shuffle_login")
//      is a bonus/non-story voice, never a flashback.
//   3. Same series (same prefix, equal segment count): flashback only if its
//      chapter number is EARLIER than the current episode's. A later number is a
//      forward reference / teaser, not a flashback.
//   4. Different series (other event / mainstory / card): always a flashback.
export function isBackReference(clue: string, major: string | null): boolean {
  if (!major) return true
  const cp = clue.split('_')
  const mp = major.split('_')
  const cEp = parseInt(cp[cp.length - 1], 10)
  if (Number.isNaN(cEp)) return false
  const sameSeries =
    cp.length === mp.length &&
    cp.slice(0, -1).join('_') === mp.slice(0, -1).join('_')
  if (!sameSeries) return true
  const mEp = parseInt(mp[mp.length - 1], 10)
  if (Number.isNaN(mEp)) return true
  return cEp < mEp
}

// Annotate each source talk with isFlashback. The "major clue" is the most
// frequent clue across the scenario (the current episode's own voices); any
// other clue that is a genuine back reference flags the line.
export function annotateFlashbacks<T extends SourceTalk>(
  talks: T[],
): (T & { isFlashback: boolean })[] {
  const counts = new Map<string, number>()
  for (const t of talks) {
    for (const c of t.clues ?? []) counts.set(c, (counts.get(c) ?? 0) + 1)
  }
  let major: string | null = null
  let max = 0
  for (const [c, n] of counts) {
    if (n > max) { max = n; major = c }
  }
  return talks.map(t => {
    if (!t.clues || t.clues.length === 0) return { ...t, isFlashback: false }
    const isFlashback = t.clues.some(c => c !== major && isBackReference(c, major))
    return { ...t, isFlashback }
  })
}
