<script setup lang="ts">
import { computed, ref, watch, onActivated, onDeactivated, onMounted, nextTick } from 'vue'
import { useAppStore } from '../../stores/app'
import { useStoryStore } from '../../stores/story'
import { useEditorStore } from '../../stores/editor'
import { useSettingsStore } from '../../stores/settings'
import { api } from '../../api/client'
import { useToast } from '../../composables/useToast'
import { useUndo } from '../../composables/useUndo'
import VoicePlayButton from './VoicePlayButton.vue'
import Live2DJumpButton from './Live2DJumpButton.vue'
import { useFlashbackTooltip } from '../../composables/useFlashbackTooltip'
import { useGlossaryMatcher } from '../../composables/useGlossaryMatcher'
import { useGlossaryTooltip } from '../../composables/useGlossaryTooltip'
import { annotateFlashbacks } from '../../utils/flashback'
import { FileText } from 'lucide-vue-next'
import type { DstTalk } from '../../types/translation'

const iconErrors = ref<Set<number>>(new Set())
const workspaceRef = ref<HTMLElement | null>(null)

// Preserve the editor scroll position when navigating away (settings, Live2D,
// etc.) and back. The scroll offset is captured LIVE on every scroll — by the
// time onDeactivated fires the route transition has already reset scrollTop to
// 0, so reading it there is too late. Restore on activate (after the DOM is
// re-attached and rows re-rendered).
let savedScrollTop = 0
function onWorkspaceScroll() {
  const top = workspaceRef.value?.scrollTop ?? 0
  if (top > 0) savedScrollTop = top
}
onActivated(() => {
  nextTick(() => {
    if (workspaceRef.value && savedScrollTop > 0) workspaceRef.value.scrollTop = savedScrollTop
  })
})

// Context menu state
const ctxMenu = ref<{ show: boolean; x: number; y: number; row: number }>({ show: false, x: 0, y: 0, row: -1 })
function hideCtxMenu() { ctxMenu.value.show = false }
function ctxReplace(row: number, b: string) { handleBracketsReplace(row, b); hideCtxMenu() }
const bracketOptions = [
  { key: '「」', label: '「」 直角引号' },
  { key: '『』', label: '『』 双层直角' },
  { key: '（）', label: '（） 全角括号' },
  { key: '""', label: '"" 双引号' },
  { key: "''", label: "'' 单引号" },
]

const app = useAppStore()
const story = useStoryStore()
const editor = useEditorStore()
const settings = useSettingsStore()
const toast = useToast()
const undo = useUndo()
const { visible: fbVisible, tooltipStyle: fbStyle, clueGroups: fbClueGroups, show: fbShow, hide: fbHide } = useFlashbackTooltip()

// ---- Glossary term matching + tooltip (opt-in via app.showGlossary) ----
const matcher = useGlossaryMatcher()
const { visible: glVisible, tooltipStyle: glStyle, tip: glTip, show: glShow, hide: glHide } = useGlossaryTooltip()
onMounted(() => { if (app.showGlossary) matcher.ensureLoaded() })
watch(() => app.showGlossary, (on) => { if (on) matcher.ensureLoaded() })

// ---- Flashback (from SourcePanel) ----
const talksWithFlashback = computed(() => {
  if (!app.showFlashback) {
    return story.sourceTalks.map(t => ({ ...t, isFlashback: false }))
  }
  return annotateFlashbacks(story.sourceTalks)
})

// ---- Helpers ----
function srcIdx(talk: DstTalk): number {
  return talk.idx - 1
}

function srcTalk(talk: DstTalk) {
  return story.sourceTalks[srcIdx(talk)]
}

function flashbackItem(talk: DstTalk) {
  return talksWithFlashback.value[srcIdx(talk)]
}

function srcTalkCharIndex(talk: DstTalk) {
  return srcTalk(talk)?.charIndex ?? -1
}

// ---- Live2D jump anchor ----
// Speakers whose rows are NEVER spoken/旁白 lines and so must NOT receive a
// talkIndex ordinal: scene location banners, top-left scene labels, and
// choice/option rows. NOTE: '' (empty speaker) is intentionally NOT in this set —
// an empty-speaker row is narration (旁白), a REAL SourceTalk line (non-empty text)
// that the Live2D plugin's dialogLineForTalkIndex counts. The only empty-speaker
// rows to skip are synthetic separators (empty speaker AND empty text); those are
// excluded explicitly in talkOrdinalBySrcIdx below. Previously '' lived here, which
// dropped every narration line from the count and shifted the ordinal by the number
// of preceding 旁白 rows (breaking the voiceless-line fallback).
const NON_TALK_SPEAKERS = ['场景', '左上场景', '选项']

// Maps a source-talk array index -> the 0-based ordinal of that row among the
// story's spoken/Talk lines (display order), counting narration (旁白) the same way
// the plugin does. Scene/option rows and synthetic separators (empty speaker AND
// empty text) are skipped and not assigned an ordinal. Precomputed once per story
// so the per-row lookup below is O(1) instead of O(n) (avoids O(n^2) over groups).
const talkOrdinalBySrcIdx = computed(() => {
  const map = new Map<number, number>()
  let ord = 0
  story.sourceTalks.forEach((t, i) => {
    // Synthetic separator: empty speaker with no body — not a spoken line.
    const isSeparator = t.speaker === '' && (t.text ?? '').trim() === ''
    if (!NON_TALK_SPEAKERS.includes(t.speaker) && !isSeparator) {
      map.set(i, ord)
      ord++
    }
  })
  return map
})

// The integer passed to the Live2D plugin as `talkIndex`: the 0-based index of
// THIS dialogue among the story's spoken/Talk lines in display order. Returns -1
// for non-Talk rows (scene/option/empty), which the template uses to hide the
// jump button there. NOTE: this is only the FALLBACK anchor — the plugin prefers
// matching by voiceId (exact); talkIndex disambiguates voiceless Talk lines.
function talkIndexFor(talk: DstTalk): number {
  return talkOrdinalBySrcIdx.value.get(srcIdx(talk)) ?? -1
}

// Group consecutive dest lines sharing the same source idx
const talkGroups = computed(() => {
  const groups: { srcIdx: number; items: { talk: DstTalk; globalIdx: number }[] }[] = []
  for (let i = 0; i < editor.talks.length; i++) {
    const talk = editor.talks[i]
    const last = groups[groups.length - 1]
    if (last && last.srcIdx === talk.idx) {
      last.items.push({ talk, globalIdx: i })
    } else {
      groups.push({ srcIdx: talk.idx, items: [{ talk, globalIdx: i }] })
    }
  }
  return groups
})

// ---- Editing (from DestPanel) ----
let editTimeout: ReturnType<typeof setTimeout> | null = null

// Per-row v-for keys use the talk's globalIdx (its index in editor.talks), which
// is unique across the whole list. The old idx+dstidx key could collide between a
// scene line and a sub-line, making Vue reuse the wrong DOM node — editing one
// row then overwrote another (e.g. the first scene line was clobbered by a later
// line's text). globalIdx keys eliminate that aliasing.

const MAX_LINES_PER_SRC = 10

// A row is "changed" iff it carries a real diff (computed by the backend against
// its baseline). Using the diff as the single source of truth keeps the baseline
// row, the green background and the green inline text perfectly in sync — they can
// no longer disagree (the cause of "green text but no baseline row").
function isChanged(talk: DstTalk): boolean {
  return app.editorMode >= 1 && !!talk.diff && talk.diff.length > 0
}

// Whether to render the read-only baseline row above the edit row.
// Driven solely by isChanged (diff presence) so it always matches the green
// highlight; renderBaseline falls back to '' if baseline is somehow missing.
function showBaselineRow(talk: DstTalk): boolean {
  return app.showCompare && isChanged(talk)
}

// Edit row (the one the user types into).
function getEditBg(talk: DstTalk): string {
  if (app.showCompare && isChanged(talk)) return 'bg-green-400/8'
  if (!talk.checked && talk.save) return 'bg-red-400/8'
  return ''
}
function getEditBorder(talk: DstTalk): string {
  if (app.showCompare && isChanged(talk)) return 'border-l-green-400'
  if (!talk.checked && talk.save) return 'border-l-red-400'
  return ''
}

// The pending debounced edit, kept alongside the timer so it can be FLUSHED
// (executed immediately) before a save, not just cancelled.
let pendingEdit: { row: number; text: string } | null = null

async function commitTextChange(row: number, newText: string) {
  // Stale-document guard: onBlur committed newText into talks[row] synchronously.
  // If it no longer matches, the arrays were swapped out from under this call
  // (mode switch, open/载入, replace-all, undo) — applying the edit now would
  // write the old document's text into an unrelated row of the NEW document.
  if (editor.talks[row]?.text !== newText) return
  try {
    const result = await api.changeText({
      row,
      text: newText,
      editorMode: app.editorMode,
      talks: editor.talks,
      dstTalks: editor.dstTalks,
      referTalks: editor.referTalks,
    })
    editor.setTalks(result.talks, result.dstTalks, editor.referTalks)
    editor.markUnsaved()
  } catch (e: any) {
    console.error('[Editor] text change API failed', { row, error: e?.message || e })
    toast.show('Text save failed: ' + (e?.message || 'unknown error'), 'error')
  }
}

function handleTextChange(row: number, newText: string) {
  if (editTimeout) clearTimeout(editTimeout)
  pendingEdit = { row, text: newText }
  editTimeout = setTimeout(() => {
    editTimeout = null
    pendingEdit = null
    commitTextChange(row, newText)
  }, 300)
}

// blur 包装：清编辑态（恢复外层选中框）再走原提交逻辑。
function onEditBlur(e: Event, idx: number) {
  if (editingRow.value === idx) editingRow.value = -1
  onBlur(e, idx)
}

function onBlur(e: Event, idx: number) {
  // Use textContent, not innerText: innerText reflects *rendered* layout and can
  // pull in text from adjacent inline elements (e.g. the row-number "0" shown
  // beside the field) or inject newlines from the diff <span>s. textContent
  // returns exactly the concatenated text of this field's nodes — the line text.
  const newText = (e.target as HTMLElement).textContent ?? ''
  // Real-change guard: blurring without an actual edit must not mark the
  // document dirty or trigger a diff recompute.
  if (editor.talks[idx]?.text === newText) return
  // Snapshot the PRE-edit state here — before the in-place commit below — so the
  // first undo actually reverts this edit. pushSnapshot deep-clones on capture,
  // and the debounced handleTextChange runs AFTER the commit; snapshotting there
  // recorded the already-applied text (off-by-one: the first undo appeared to do
  // nothing, every undo lagged one edit). It also survives debounce cancellation
  // when a second row is edited within 300ms, which previously dropped the snapshot.
  undo.pushSnapshot(editor.talks, editor.dstTalks)
  // Commit the edit to the talks array SYNCHRONOUSLY before the debounced API
  // call. Previously the text only reached editor.talks when changeText
  // returned; but handleTextChange debounces on a single shared timer, so
  // editing a second row within 300ms cleared the first row's pending save and
  // its edit (which lived only in the DOM) was lost — and a later setTalks
  // re-render wiped it from the DOM too. Committing here guarantees every
  // blurred edit is in the array, so saving and subsequent recomputes always
  // carry it, regardless of debounce cancellation.
  if (editor.talks[idx]) editor.talks[idx].text = newText
  // Commit to dstTalks as well: SAVE serializes editor.dstTalks, not talks, and
  // dstTalks otherwise only picks the edit up when the debounced changeText
  // round-trip lands. Saving inside that 300ms window — or after any
  // cancelPendingEdit (undo / add-remove line / replace-all) — wrote the file
  // with the row's OLD text while the screen showed the new one. The backend
  // round-trip still refines this raw commit (punctuation normalization, diff).
  const di = editor.talks[idx]?.dstidx ?? -1
  if (di >= 0 && di < editor.dstTalks.length) editor.dstTalks[di].text = newText
  handleTextChange(idx, newText)
}

async function handleAddLine(row: number) {
  // FLUSH (not cancel) any pending debounced edit before mutating rows: the
  // changeText round-trip does not reorder talks, so the captured indices stay
  // valid, and cancelling would leave dstTalks without the edit's processed
  // form (for a compare deletion row the slot insert happens server-side, so a
  // cancel would drop that edit from the saved file entirely).
  await flushPendingEdit().catch(() => {})
  const currentIdx = editor.talks[row]?.idx
  if (currentIdx && editor.talks.filter(t => t.idx === currentIdx).length >= MAX_LINES_PER_SRC) {
    toast.show(`每个原文行最多添加 ${MAX_LINES_PER_SRC} 行`, 'warn')
    return
  }
  undo.pushSnapshot(editor.talks, editor.dstTalks)
  try {
    const result = await api.addLine({
      row,
      talks: editor.talks,
      dstTalks: editor.dstTalks,
      isProofreading: app.editorMode !== 0,
    })
    editor.setTalks(result.talks, result.dstTalks, editor.referTalks)
    editor.markUnsaved()
  } catch (e: any) {
    console.error('[Editor] add line failed', { row, error: e?.message || e })
    toast.show('Add line failed: ' + (e?.message || 'unknown error'), 'error')
  }
}

async function handleRemoveLine(row: number) {
  // See handleAddLine: flush the pending debounced edit before mutating rows.
  await flushPendingEdit().catch(() => {})
  undo.pushSnapshot(editor.talks, editor.dstTalks)
  try {
    const result = await api.removeLine({
      row,
      talks: editor.talks,
      dstTalks: editor.dstTalks,
    })
    editor.setTalks(result.talks, result.dstTalks, editor.referTalks)
    editor.markUnsaved()
  } catch (e: any) {
    console.error('[Editor] remove line failed', { row, error: e?.message || e })
    toast.show('Remove line failed: ' + (e?.message || 'unknown error'), 'error')
  }
}

// Render the read-only baseline row: shows baseline text with removed chars in red.
function renderBaseline(talk: DstTalk): string {
  if (!talk.diff || talk.diff.length === 0) return escapeHtml(talk.baseline ?? '')
  return talk.diff
    .filter(p => p.type === 'same' || p.type === 'remove')
    .map(p => {
      const esc = escapeHtml(p.text)
      return p.type === 'remove' ? `<span class="bg-red-400/30">${esc}</span>` : esc
    })
    .join('')
}

// Render the edit row: shows current text. Added chars are highlighted green when
// comparing, or when compare is off but the user opted to keep highlights.
function renderHighlight(talk: DstTalk): string {
  const highlight = app.editorMode >= 1 &&
    (app.showCompare || settings.settings.keepHighlightWhenCompareOff)
  let html: string
  if (!talk.diff || talk.diff.length === 0 || !highlight) {
    html = escapeHtml(talk.text)
  } else {
    html = talk.diff
      .filter(p => p.type === 'same' || p.type === 'add')
      .map(p => {
        const esc = escapeHtml(p.text)
        return p.type === 'add' ? `<span class="bg-green-400/30">${esc}</span>` : esc
      })
      .join('')
  }
  return markQuery(html)
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

// ---- Search highlight (query state lives in app store; navigation/replace in EditorPage) ----
function markQuery(html: string): string {
  const q = app.searchQuery.trim()
  if (!q) return html
  // Highlight occurrences in already-escaped html, skipping inside tags.
  const escQ = escapeHtml(q).replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return html.replace(new RegExp(`(${escQ})(?![^<]*>)`, 'gi'), '<mark class="bg-yellow-300 text-black rounded-sm">$1</mark>')
}

function highlightSpeaker(speaker: string): string {
  return markQuery(escapeHtml(speaker))
}

// Wrap matched glossary terms in the ALREADY-escaped source html with a
// highlight span carrying data-term (the raw source key) for hover lookup.
// Mirrors markQuery's (?![^<]*>) guard so we never inject inside an existing
// tag (e.g. a <mark> from search). Only applied to read-only source text.
function markGlossary(html: string, rawText: string): string {
  if (!app.showGlossary || !rawText) return html
  const hits = matcher.matchTerms(rawText)
  if (hits.length === 0) return html
  // Unique terms, longest first, so a longer term isn't broken by a shorter one.
  const terms = Array.from(new Set(hits.map(h => h.term))).sort((a, b) => b.length - a.length)
  let out = html
  for (const term of terms) {
    const escTerm = escapeHtml(term)
    const pat = escTerm.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    out = out.replace(
      new RegExp(`(${pat})(?![^<]*>)`, 'g'),
      `<span class="glossary-hit" data-term="${escTerm.replace(/"/g, '&quot;')}">$1</span>`,
    )
  }
  return out
}

// Render source text with search + glossary highlighting layered on.
function renderSource(text: string): string {
  return markGlossary(markQuery(escapeHtml(text)), text)
}

// Hover handler (event delegation on the source container): show the glossary
// tooltip for the term under the cursor, with an appellation suggestion based
// on the line's speaker.
function onGlossaryHover(e: MouseEvent, speaker: string) {
  if (!app.showGlossary) return
  const el = (e.target as HTMLElement)?.closest('[data-term]') as HTMLElement | null
  if (!el) { glHide(); return }
  const term = el.getAttribute('data-term') || ''
  const entry = matcher.lookup(term)
  if (entry) glShow(el, entry, speaker)
}

// Match list across source text, dest text and speaker, in group order. Owns
// app.searchTotal; the toolbar (EditorPage) steps app.searchActiveIndex.
const searchMatches = computed(() => {
  const q = app.searchQuery.trim().toLowerCase()
  const out: number[] = []
  if (!q) return out
  talkGroups.value.forEach((group, gi) => {
    const first = group.items[0].talk
    const src = srcTalk(first)
    const inSrc = !!src?.text && src.text.toLowerCase().includes(q)
    const inSpeaker = !!first.speaker && first.speaker.toLowerCase().includes(q)
    const inDst = group.items.some(it => it.talk.text && it.talk.text.toLowerCase().includes(q))
    if (inSrc || inSpeaker || inDst) out.push(gi)
  })
  return out
})

watch(searchMatches, (m) => {
  app.searchTotal = m.length
  if (app.searchActiveIndex >= m.length) app.searchActiveIndex = 0
}, { immediate: true })

watch(() => app.searchActiveIndex, () => {
  const gi = searchMatches.value[app.searchActiveIndex]
  if (gi === undefined) return
  const el = workspaceRef.value?.querySelector(`[data-group="${gi}"]`) as HTMLElement | null
  el?.scrollIntoView({ block: 'center', behavior: 'smooth' })
})

async function handleBracketsReplace(row: number, brackets: string) {
  // See handleAddLine: flush, don't drop, the pending edit.
  await flushPendingEdit().catch(() => {})
  undo.pushSnapshot(editor.talks, editor.dstTalks)
  api.replaceBrackets({ row, brackets, talks: editor.talks, dstTalks: editor.dstTalks }).then(({ talks, dstTalks }) => {
    editor.talks = talks
    editor.dstTalks = dstTalks
    editor.markUnsaved()
  }).catch((e: any) => {
    console.error('[Editor] bracket replace failed', { row, brackets, error: e?.message || e })
    toast.show('Bracket replace failed: ' + (e?.message || 'unknown error'), 'error')
  })
}

function handleContextMenu(e: MouseEvent, row: number) {
  e.preventDefault()
  ctxMenu.value = { show: true, x: e.clientX, y: e.clientY, row }
}

function focusNext(e: KeyboardEvent) {
  // IME guard: during composition the Enter key confirms the candidate word, so
  // bail WITHOUT preventDefault to let the IME commit normally. Otherwise the
  // keystroke is swallowed, focus jumps to the next row and the in-flight
  // composition is dropped — fatal for a JP→CN tool where every input is IME.
  // The template deliberately drops `.enter.prevent` for `.enter` alone: the
  // `.prevent` modifier fires preventDefault unconditionally BEFORE this handler,
  // which would cancel the candidate commit regardless of this guard. We now
  // call preventDefault below only for real navigation. keyCode 229 covers
  // engines that omit isComposing on the composing keydown.
  if (e.isComposing || (e as any).keyCode === 229) return
  e.preventDefault()
  const container = workspaceRef.value
  if (!container) return
  const editables = container.querySelectorAll<HTMLElement>('[contenteditable="true"]')
  const idx = Array.from(editables).indexOf(e.target as HTMLElement)
  const next = editables[idx + 1]
  if (next) {
    next.focus()
    next.scrollIntoView({ block: 'center' }) // Enter 逐行推进时保持当前编辑行居中
    const range = document.createRange()
    range.selectNodeContents(next)
    range.collapse(false)
    const sel = window.getSelection()
    sel?.removeAllRanges()
    sel?.addRange(range)
  }
}

// ── 键盘行导航：Esc 退出编辑 → ↑/↓ 切换选中行 → Enter 进入编辑 ────────────────
// selectedRow 记录当前「选中但未编辑」的行（globalIdx）。编辑框失焦后高亮保留，
// 方向键在可编辑行之间移动高亮，Enter 重新聚焦进入编辑。
// editingRow：正在编辑（contenteditable 聚焦）的行——编辑态只显示编辑框自身的
// 内框，外层行高亮隐藏，避免双框（用户反馈）。
const selectedRow = ref(-1)
const editingRow = ref(-1)

function onEditFocus(gidx: number) {
  selectedRow.value = gidx
  editingRow.value = gidx
}

// 按显示顺序取全部可编辑行（data-gidx 标注 globalIdx）。
function editableEls(): HTMLElement[] {
  const c = workspaceRef.value
  return c ? Array.from(c.querySelectorAll<HTMLElement>('[contenteditable="true"][data-gidx]')) : []
}

function focusEditable(el: HTMLElement) {
  el.focus()
  el.scrollIntoView({ block: 'center' }) // 正在编辑的行始终居中
  const range = document.createRange()
  range.selectNodeContents(el)
  range.collapse(false)
  const sel = window.getSelection()
  sel?.removeAllRanges()
  sel?.addRange(range)
}

// Esc inside a contenteditable: commit (blur) and keep the row selected. During
// IME composition, Escape cancels the candidate — leave it to the input method.
// stopPropagation 必须有：blur 同步生效后同一事件冒泡到 window 的 onNavKey，
// 会命中那里的 Escape=清除选中分支，把刚记下的行号立刻抹掉（下次 ↑/↓ 又从头来）。
function onEditableEsc(e: KeyboardEvent, gidx: number) {
  if (e.isComposing || (e as any).keyCode === 229) return
  e.preventDefault()
  e.stopPropagation()
  selectedRow.value = gidx
  ;(e.target as HTMLElement).blur()
}

// Window-level navigation while NOT editing. Bound on activate/deactivate (the
// page is kept-alive, so mount/unmount never fires on navigation).
function onNavKey(e: KeyboardEvent) {
  const key = e.key
  if (key !== 'ArrowUp' && key !== 'ArrowDown' && key !== 'Enter' && key !== 'Escape') return
  if (e.metaKey || e.ctrlKey || e.altKey || e.shiftKey) return
  // Typing somewhere (title/search input, another editable) — don't hijack.
  const ae = document.activeElement
  if (ae instanceof HTMLElement && (ae.isContentEditable || ae.tagName === 'INPUT' || ae.tagName === 'TEXTAREA' || ae.tagName === 'SELECT')) return
  if (key === 'Escape') { selectedRow.value = -1; return }
  const els = editableEls()
  if (!els.length) return
  const cur = els.findIndex(el => Number(el.dataset.gidx) === selectedRow.value)
  if (key === 'Enter') {
    if (cur < 0) return
    e.preventDefault()
    focusEditable(els[cur])
    return
  }
  e.preventDefault()
  const next = key === 'ArrowDown'
    ? (cur < 0 ? 0 : Math.min(cur + 1, els.length - 1))
    : (cur < 0 ? els.length - 1 : Math.max(cur - 1, 0))
  selectedRow.value = Number(els[next].dataset.gidx)
  els[next].scrollIntoView({ block: 'center' })
}
onActivated(() => window.addEventListener('keydown', onNavKey))
onDeactivated(() => window.removeEventListener('keydown', onNavKey))

// Allow the parent (EditorPage) to drop the pending debounced edit before it
// performs a structural mutation (replace-all / undo / redo) that reorders
// editor.talks, which would otherwise make the stale timer write to a shifted
// row. onBlur has already committed the visible text, so cancelling loses nothing.
function cancelPendingEdit() {
  if (editTimeout) { clearTimeout(editTimeout); editTimeout = null }
  pendingEdit = null
}

// Run the pending debounced edit NOW (awaited) instead of dropping it. Used by
// EditorPage before saving so the file gets the fully processed (normalized +
// diffed) text rather than relying on the raw onBlur commit alone.
async function flushPendingEdit() {
  if (editTimeout) { clearTimeout(editTimeout); editTimeout = null }
  const p = pendingEdit
  pendingEdit = null
  if (p) await commitTextChange(p.row, p.text)
}
defineExpose({ cancelPendingEdit, flushPendingEdit })

function onSourceEnter(e: MouseEvent, talk: DstTalk) {
  const fb = flashbackItem(talk)
  if (fb?.isFlashback && fb?.clues) {
    // Keep clues and their source-line numbers aligned while dropping empties.
    const clues: string[] = []
    const lines: number[] = []
    fb.clues.forEach((c: string, i: number) => {
      if (!c) return
      clues.push(c)
      lines.push(fb.flashbackLines?.[i] ?? 0)
    })
    fbShow(e, clues, lines)
  }
}
</script>

<template>
  <div class="flex h-full">
    <div
      ref="workspaceRef"
      @scroll="onWorkspaceScroll"
      class="flex-1 overflow-y-auto border border-[var(--color-border)] rounded-[var(--radius-card)] bg-[var(--color-surface)]"
    >
      <!-- Column headers. rounded-t matches the panel: a position:sticky child
           gets its own compositing layer in WKWebView and escapes the parent's
           border-radius clip, so without its own rounding its square top corners
           poke past the rounded panel. -->
      <div class="grid grid-cols-2 border-b border-[var(--color-border)] bg-[var(--color-surface)] rounded-t-[var(--radius-card)] sticky top-0 z-10">
        <div class="flex items-center justify-between px-3 py-2">
          <span class="font-semibold text-sm text-[var(--color-text-secondary)]">原文</span>
          <span v-if="story.scenarioId" class="text-xs text-[var(--color-text-secondary)]">{{ story.scenarioId }}</span>
        </div>
        <div class="flex items-center px-3 py-2 border-l border-[var(--color-border)]">
          <span class="font-semibold text-sm text-[var(--color-text-secondary)]">译文</span>
          <input
            v-model="editor.titleOverride"
            type="text"
            :placeholder="story.chapterTitle || story.saveTitle || '标题...'"
            title="仅替换文件名中的标题部分（【模式】前缀与路径自动保留）"
            class="app-input ml-2 flex-1 py-1"
          />
        </div>
      </div>

      <template v-if="story.sourceTalks.length === 0">
        <div class="flex flex-col items-center justify-center gap-3 py-16 text-center text-[var(--color-text-tertiary)]">
          <FileText :size="32" class="opacity-60" />
          <span class="text-sm">选择故事并载入以查看原文</span>
        </div>
      </template>

      <template v-else>
        <div class="flex flex-col gap-1.5 px-2 py-1">
          <template v-for="(group, gi) in talkGroups" :key="gi">
            <div class="grid grid-cols-2 gap-2" :data-group="gi">
              <!-- ===== Source Side (merged for group) ===== -->
              <div
                class="flex flex-col justify-center p-3 rounded-lg border border-[var(--color-border)] transition-colors"
                :class="{ 'bg-[var(--color-flashback)]': flashbackItem(group.items[0].talk)?.isFlashback }"
                @mouseenter="onSourceEnter($event, group.items[0].talk)"
                @mousemove="onSourceEnter($event, group.items[0].talk)"
                @mouseover="onGlossaryHover($event, srcTalk(group.items[0].talk)?.speaker ?? group.items[0].talk.speaker)"
                @mouseleave="fbHide(); glHide()"
              >
                <div class="flex items-center gap-3">
                  <div
                    class="w-8 h-8 rounded-full flex-shrink-0 overflow-hidden bg-[var(--color-surface)] border border-[var(--color-border)]"
                  >
                    <img
                      v-if="srcTalkCharIndex(group.items[0].talk) >= 0 && !iconErrors.has(srcTalkCharIndex(group.items[0].talk)) && !['场景', '左上场景', '选项', ''].includes(srcTalk(group.items[0].talk)?.speaker)"
                      :src="api.characterIconUrl(srcTalkCharIndex(group.items[0].talk) + 1)"
                      :alt="srcTalk(group.items[0].talk)?.speaker"
                      class="w-full h-full object-cover"
                      @error="iconErrors.add(srcTalkCharIndex(group.items[0].talk))"
                    />
                    <div
                      v-else
                      class="w-full h-full flex items-center justify-center bg-neutral text-neutral-content text-xs font-medium select-none"
                    >
                      {{ srcTalk(group.items[0].talk)?.speaker?.charAt(0) || '' }}
                    </div>
                  </div>

                  <div class="flex-1 min-w-0">
                    <div class="text-xs font-medium text-[var(--color-text-secondary)] mb-0.5" v-html="highlightSpeaker(srcTalk(group.items[0].talk)?.speaker ?? '')">
                    </div>
                    <div v-if="srcTalk(group.items[0].talk)?.text" class="leading-relaxed whitespace-pre-wrap break-words" style="font-size: var(--editor-font-size)" v-html="renderSource(srcTalk(group.items[0].talk)?.text ?? '')">
                    </div>
                    <div v-else class="flex items-center gap-3" style="font-size: var(--editor-font-size)">
                      <span class="flex-1 border-t border-[var(--color-border)] opacity-40" />
                      <span class="text-[var(--color-text-secondary)] text-xs opacity-50 select-none">空</span>
                      <span class="flex-1 border-t border-[var(--color-border)] opacity-40" />
                    </div>
                  </div>

                  <!-- Per-line button stack: flat 扁长方形 controls, voice on top
                       and the Live2D jump below. items-stretch keeps both the same
                       width so it reads as a tidy 2-row control. -->
                  <div class="flex flex-col gap-1 items-stretch flex-shrink-0">
                    <VoicePlayButton
                      v-if="(srcTalk(group.items[0].talk)?.voices?.length ?? 0) > 0"
                      :scenario-id="story.scenarioId"
                      :voice-ids="(srcTalk(group.items[0].talk)?.voices ?? []) as string[]"
                      :volume="srcTalk(group.items[0].talk).volume"
                      :source="story.selectedSource"
                      :chara2d="srcTalk(group.items[0].talk)?.chara2d"
                    />
                    <!-- talk-index = 0-based ordinal of this dialogue among the
                         story's spoken/Talk lines (talkIndexFor). Only rendered for
                         real Talk rows (>= 0); the plugin prefers voice-id. -->
                    <Live2DJumpButton
                      v-if="talkIndexFor(group.items[0].talk) >= 0"
                      :scenario-id="story.scenarioId"
                      :talk-index="talkIndexFor(group.items[0].talk)"
                      :voice-id="srcTalk(group.items[0].talk)?.voices?.[0]"
                    />
                  </div>
                </div>
              </div>

              <!-- ===== Dest Side (stacked per sub-line) ===== -->
              <div class="flex flex-col gap-1 h-full">
                <template v-for="item in group.items" :key="item.globalIdx">
                  <!-- Baseline row (read-only): shown under compare when baseline differs -->
                  <div
                    v-if="showBaselineRow(item.talk)"
                    class="p-2 rounded-lg border border-[var(--color-border)] border-l-4 border-l-yellow-400 bg-yellow-400/8 select-none"
                  >
                    <div class="flex items-start gap-2">
                      <div class="w-8 flex-shrink-0" />
                      <div style="min-width:3rem;max-width:8rem" class="flex-shrink-0 text-xs text-[var(--color-text-secondary)] pt-1">原</div>
                      <div
                        class="flex-1 min-w-0 leading-relaxed px-1 -mx-1 text-[var(--color-text-secondary)]"
                        style="font-size: var(--editor-font-size)"
                        v-html="renderBaseline(item.talk)"
                      ></div>
                    </div>
                  </div>

                  <!-- Edit row -->
                  <div
                    :class="['p-2 rounded-lg border border-[var(--color-border)] transition-colors hover:bg-[var(--color-primary)]/[0.04]', group.items.length === 1 && !showBaselineRow(item.talk) ? 'flex-1 flex flex-col justify-center' : '', getEditBorder(item.talk) ? `border-l-4 ${getEditBorder(item.talk)}` : '', getEditBg(item.talk), selectedRow === item.globalIdx && editingRow !== item.globalIdx ? 'row-selected' : '']"
                  >
                    <div class="flex items-start gap-2">
                      <div class="w-8 flex-shrink-0 text-xs text-[var(--color-text-secondary)] pt-1">
                        <span v-if="item.talk.start" class="font-mono">{{ item.talk.idx }}</span>
                      </div>

                      <div v-if="item.talk.start" class="flex-shrink-0 text-xs text-[var(--color-text-secondary)] pt-1 truncate" style="min-width:3rem;max-width:8rem" :title="item.talk.speaker">
                        {{ item.talk.speaker }}
                      </div>
                      <div v-else style="min-width:3rem;max-width:8rem" class="flex-shrink-0" />

                      <div
                        class="flex-1 min-w-0"
                        @contextmenu="handleContextMenu($event, item.globalIdx)"
                      >
                        <div
                          :contenteditable="item.talk.save && ![''].includes(item.talk.speaker)"
                          :data-gidx="item.globalIdx"
                          class="leading-relaxed outline-none rounded px-1 -mx-1"
                          style="font-size: var(--editor-font-size)"
                          :class="{ 'cursor-text': item.talk.save && ![''].includes(item.talk.speaker) }"
                          @focus="onEditFocus(item.globalIdx)"
                          @blur="onEditBlur($event, item.globalIdx)"
                          @keydown.enter="focusNext"
                          @keydown.esc="onEditableEsc($event, item.globalIdx)"
                          v-html="renderHighlight(item.talk)"
                        ></div>
                        <div v-if="item.talk.message" class="text-xs text-error mt-0.5">
                          {{ item.talk.message }}
                        </div>
                      </div>

                      <div class="flex items-center gap-1 flex-shrink-0">
                        <span v-if="!item.talk.end && item.talk.save" class="text-xs text-[var(--color-text-secondary)] font-mono">\N</span>
                        <button
                          v-if="item.talk.end && ![''].includes(item.talk.speaker) && item.talk.save"
                          class="w-6 h-6 rounded border border-[var(--color-border)] text-xs hover:text-[var(--color-primary)]"
                          title="添加行"
                          @click="handleAddLine(item.globalIdx)"
                        >+</button>
                        <button
                          v-if="!item.talk.start"
                          class="w-6 h-6 rounded border border-[var(--color-border)] text-xs hover:bg-error/10 hover:text-error"
                          title="删除行"
                          @click="handleRemoveLine(item.globalIdx)"
                        >−</button>
                      </div>
                    </div>
                  </div>
                </template>
              </div>
            </div>
          </template>
        </div>
      </template>
    </div>
  </div>

  <Teleport to="body">
    <div
      v-if="fbVisible && fbClueGroups.length > 0"
      :style="fbStyle"
      class="flashback-tooltip rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] shadow-lg p-3 text-xs pointer-events-none"
    >
      <div class="font-semibold text-[var(--color-primary)] mb-1.5">闪回来源</div>
      <template v-for="(group, gi) in fbClueGroups" :key="gi">
        <div
          v-if="gi > 0"
          class="border-t border-[var(--color-border)] my-1.5"
        />
        <div
          v-for="(hint, hi) in group.hints"
          :key="hi"
          class="text-[var(--color-text-secondary)] leading-relaxed"
          :class="hi === 0 ? 'font-medium' : 'text-xs opacity-80'"
        >
          {{ hint }}
        </div>
      </template>
    </div>
  </Teleport>

  <!-- Glossary term tooltip -->
  <Teleport to="body">
    <div
      v-if="glVisible && glTip"
      :style="glStyle"
      class="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] shadow-lg p-3 text-xs pointer-events-none"
    >
      <div class="flex items-baseline gap-2">
        <span class="font-semibold">{{ glTip.source }}</span>
        <span class="text-[var(--color-text-secondary)]">→</span>
        <span class="text-[var(--color-primary)] font-medium">{{ glTip.translation }}</span>
      </div>
      <div v-if="glTip.aliases && glTip.aliases.length" class="text-[var(--color-text-secondary)] mt-1">别称：{{ glTip.aliases.join('、') }}</div>
      <div v-if="glTip.note" class="text-[var(--color-text-secondary)] mt-1 leading-relaxed">{{ glTip.note }}</div>
      <div v-if="glTip.appellCn || glTip.appellJp" class="border-t border-[var(--color-border)] mt-1.5 pt-1.5">
        <span class="text-[var(--color-text-secondary)]">{{ glTip.appellSpeaker }} 称呼：</span>
        <span class="font-medium">{{ glTip.appellJp }}</span>
        <span v-if="glTip.appellCn" class="text-[var(--color-primary)]"> / {{ glTip.appellCn }}</span>
      </div>
      <div v-if="glTip.category" class="text-[10px] text-[var(--color-text-secondary)] mt-1.5 opacity-70">{{ glTip.category }}</div>
    </div>
  </Teleport>

  <!-- Context Menu -->
  <Teleport to="body">
    <div
      v-if="ctxMenu.show"
      class="fixed inset-0 z-[100]"
      @click="hideCtxMenu"
    >
      <div
        class="absolute bg-[var(--color-surface)] border border-[var(--color-border)] rounded-lg shadow-lg py-1 text-sm min-w-[160px]"
        :style="{ left: ctxMenu.x + 'px', top: ctxMenu.y + 'px' }"
        @click.stop
      >
        <div class="px-3 py-0.5 text-xs text-[var(--color-text-secondary)]">替换括号</div>
        <div class="border-t border-[var(--color-border)] my-1" />
        <button
          v-for="opt in bracketOptions" :key="opt.key"
          class="w-full text-left px-3 py-1.5 hover:bg-[var(--color-primary)]/10 transition-colors"
          @click="ctxReplace(ctxMenu.row, opt.key)"
        >{{ opt.label }}</button>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
/* 键盘导航选中行：内描边高亮，不与左侧 border-l 编辑指示条冲突。 */
.row-selected {
  box-shadow: inset 0 0 0 1.5px var(--color-primary);
}

/* 编辑态内框：全局 :focus-visible 只在键盘聚焦时画框，鼠标点进编辑没有指示——
   这里统一成"只要在编辑就有内框"（外层 row-selected 编辑态已隐藏，恰好互补）。 */
[contenteditable='true']:focus {
  outline: 2px solid color-mix(in oklch, var(--accent, var(--color-primary)) 70%, transparent);
  outline-offset: 2px;
}

/* Glossary term highlight in source text (injected via v-html, so :deep). */
:deep(.glossary-hit) {
  border-bottom: 1.5px dotted var(--color-primary);
  cursor: help;
}
:deep(.glossary-hit:hover) {
  background-color: color-mix(in srgb, var(--color-primary) 15%, transparent);
  border-radius: 2px;
}
</style>
