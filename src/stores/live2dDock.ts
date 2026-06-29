import { defineStore } from 'pinia'
import { ref } from 'vue'
import { useSettingsStore } from './settings'
import { useStoryStore } from './story'

// Where the Live2D player sits relative to the editor. 'left' is deliberately
// absent — the left edge is the story navigator / plugin sidebar.
export type Live2dPlacement = 'top' | 'right' | 'bottom' | 'window'

// A jump request published by the editor's "在 Live2D 播放" button.
//  - talkIndex: 0-based index of the clicked dialogue among the story's spoken
//    (Talk) lines, in display order — the plugin maps it to its own dialog-line
//    numbering and seeks there.
//  - voiceId: the clicked line's voice clip id when it has one. PREFERRED anchor:
//    the plugin should match the snippet by voice id first (exact, no index math)
//    and only fall back to talkIndex for voiceless lines.
//  - nonce makes repeated jumps to the SAME line still fire the panel's watcher.
//  - sel carries the host story-store selection so a cold 独立窗口 (a fresh JS
//    context with an EMPTY story store) can reconstruct stage.play() args / source
//    that the plugin otherwise derives from the store. Docked mode carries it too
//    (harmless — the store is already populated there).
export interface Live2dSel {
  type: string
  sort: string
  index: string
  chapter: number
  source: string
}

export interface Live2dJump {
  scenarioId: string
  talkIndex: number
  voiceId?: string
  nonce: number
  sel?: Live2dSel
}

// Host-owned coordination point between the core editor and the (separately
// loaded) Live2D plugin. The editor publishes jumps here; the plugin's docked
// panel / detached player watches `pendingJump` and acts. Keeping the state in a
// host store means host and plugin share one reactive singleton without sharing
// any imports (the plugin reaches it via host.stores.live2dDock()).
export const useLive2dDockStore = defineStore('live2dDock', () => {
  // Whether the docked panel is shown (top/right/bottom placements only).
  const visible = ref(false)
  // Docked-strip extent along its axis, in px (width for left/right, height for
  // top/bottom). Persisted for the session; the dock's drag handle writes it.
  const size = ref(380)
  // The latest jump the mounted panel/player should apply (cleared once consumed).
  const pendingJump = ref<Live2dJump | null>(null)
  // When the user picked 独立窗口 but the window couldn't open (web dev or a
  // Tauri create failure), we degrade to a docked panel. EditorPage.dockSide
  // returns null for placement()==='window', so without this the fallback panel
  // would never mount and the jump would be silently dropped. forcedDock overrides
  // that for the failure case; it is reset whenever the dock hides or a window
  // does open, so it never sticks.
  const forcedDock = ref<'top' | 'right' | 'bottom' | null>(null)
  let seq = 0

  function placement(): Live2dPlacement {
    const s = useSettingsStore()
    const p = s.settings.live2dPosition as Live2dPlacement | undefined
    return p === 'top' || p === 'bottom' || p === 'window' ? p : 'right'
  }

  // Entry point for the editor's Live2D button. Routes by the user's placement:
  // a docked edge → reveal the panel + publish the jump; 独立窗口 → open/focus a
  // separate Tauri window at #/live2d carrying the jump (URL for a cold window,
  // a live event for an already-open one). Falls back to docking off-Tauri.
  async function requestJump(scenarioId: string, talkIndex: number, voiceId?: string) {
    // Snapshot the host story selection so a cold 独立窗口 can rebuild play()/source.
    const st = useStoryStore()
    const sel: Live2dSel = {
      type: st.selectedType,
      sort: st.selectedSort,
      index: st.selectedIndex,
      chapter: st.selectedChapter,
      source: st.selectedSource,
    }
    const jump: Live2dJump = { scenarioId, talkIndex, voiceId, nonce: ++seq, sel }
    if (placement() === 'window') {
      await openWindow(jump)
      return
    }
    forcedDock.value = null
    visible.value = true
    pendingJump.value = jump
  }

  // The panel calls this after applying a jump so toggling visibility later
  // doesn't replay a stale request.
  function consumeJump(): Live2dJump | null {
    const j = pendingJump.value
    pendingJump.value = null
    return j
  }

  function show() { visible.value = true }
  function hide() { visible.value = false; forcedDock.value = null }
  function toggle() { visible.value = !visible.value; if (!visible.value) forcedDock.value = null }

  async function openWindow(jump: Live2dJump) {
    try {
      const { WebviewWindow } = await import('@tauri-apps/api/webviewWindow')
      const { emit } = await import('@tauri-apps/api/event')
      const label = 'live2d'
      // getByLabel is sync in some versions, async in others — await tolerates both.
      const existing = await WebviewWindow.getByLabel(label)
      if (existing) {
        forcedDock.value = null
        await existing.setFocus().catch(() => {})
        await emit('live2d:jump', jump)
        return
      }
      // Hash route + query so a cold window self-seeks on mount without an event.
      // The query carries the FULL selection (type/sort/index/chapter/source) so
      // the plugin can rebuild the host story store before seeking — a fresh window
      // has an empty store and can't derive play()/source otherwise. This URL must
      // stay byte-for-byte in sync with the plugin's route.query parser.
      const enc = encodeURIComponent
      const sel = jump.sel
      let url = `index.html#/live2d?jump=${jump.talkIndex}`
      if (jump.voiceId) url += `&voice=${enc(jump.voiceId)}`
      if (sel) {
        url += `&type=${enc(sel.type)}&sort=${enc(sel.sort)}&index=${enc(sel.index)}`
          + `&chapter=${sel.chapter}&source=${enc(sel.source)}`
      }
      const w = new WebviewWindow(label, {
        url,
        title: 'Live2D',
        width: 1024,
        height: 720,
        resizable: true,
      })
      // Belt-and-braces: also push the jump once the window reports created, in
      // case the page's listener attaches before it parses the URL query.
      w.once('tauri://created', () => { forcedDock.value = null; void emit('live2d:jump', jump) })
      w.once('tauri://error', () => {
        // Window couldn't open (perm/path) → degrade to a docked panel. forcedDock
        // makes EditorPage.dockSide mount the dock even though placement is 'window'.
        forcedDock.value = 'right'
        visible.value = true
        pendingJump.value = jump
      })
    } catch {
      // Non-Tauri (web dev) or API unavailable → dock instead (see above).
      forcedDock.value = 'right'
      visible.value = true
      pendingJump.value = jump
    }
  }

  return { visible, size, pendingJump, forcedDock, placement, requestJump, consumeJump, show, hide, toggle }
})
