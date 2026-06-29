import { defineStore } from 'pinia'
import { ref } from 'vue'
import { useSettingsStore } from './settings'

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
export interface Live2dJump {
  scenarioId: string
  talkIndex: number
  voiceId?: string
  nonce: number
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
    const jump: Live2dJump = { scenarioId, talkIndex, voiceId, nonce: ++seq }
    if (placement() === 'window') {
      await openWindow(jump)
      return
    }
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
  function hide() { visible.value = false }
  function toggle() { visible.value = !visible.value }

  async function openWindow(jump: Live2dJump) {
    try {
      const { WebviewWindow } = await import('@tauri-apps/api/webviewWindow')
      const { emit } = await import('@tauri-apps/api/event')
      const label = 'live2d'
      // getByLabel is sync in some versions, async in others — await tolerates both.
      const existing = await WebviewWindow.getByLabel(label)
      if (existing) {
        await existing.setFocus().catch(() => {})
        await emit('live2d:jump', jump)
        return
      }
      // Hash route + query so a cold window self-seeks on mount without an event.
      const url = `index.html#/live2d?jump=${jump.talkIndex}&scenario=${encodeURIComponent(jump.scenarioId)}`
        + (jump.voiceId ? `&voice=${encodeURIComponent(jump.voiceId)}` : '')
      const w = new WebviewWindow(label, {
        url,
        title: 'Live2D',
        width: 1024,
        height: 720,
        resizable: true,
      })
      // Belt-and-braces: also push the jump once the window reports created, in
      // case the page's listener attaches before it parses the URL query.
      w.once('tauri://created', () => { void emit('live2d:jump', jump) })
      w.once('tauri://error', () => {
        // Window couldn't open (perm/path) → degrade to a docked panel.
        visible.value = true
        pendingJump.value = jump
      })
    } catch {
      // Non-Tauri (web dev) or API unavailable → dock instead.
      visible.value = true
      pendingJump.value = jump
    }
  }

  return { visible, size, pendingJump, placement, requestJump, consumeJump, show, hide, toggle }
})
