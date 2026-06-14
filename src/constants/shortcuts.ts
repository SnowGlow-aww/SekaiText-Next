// Central keyboard-shortcut registry. Shortcuts are stored as combo strings and
// resolved against the user's settings (custom overrides) with a built-in
// default fallback, so the editor key handler, the settings UI, and persistence
// all share one source of truth.
//
// Combo format: modifier tokens joined by "+", then the main key.
//   - "mod"  -> Cmd on macOS, Ctrl elsewhere
//   - "alt", "shift"
//   - main key: a letter (case-insensitive) or an e.key value like "ArrowUp"
// Examples: "mod+o", "alt+r", "ArrowUp", "mod+shift+z".

export interface ShortcutAction {
  id: string
  label: string
  default: string
  /** Optional note shown in settings (e.g. context where it applies). */
  note?: string
}

export const SHORTCUT_ACTIONS: ShortcutAction[] = [
  { id: 'open', label: '打开文件', default: 'mod+o' },
  { id: 'save', label: '保存文件', default: 'mod+s' },
  { id: 'search', label: '打开搜索', default: 'mod+f' },
  { id: 'replaceAll', label: '全部替换', default: 'alt+r', note: '搜索栏打开时' },
  { id: 'prevMatch', label: '上一个匹配', default: 'ArrowUp', note: '搜索栏打开、焦点不在输入框时' },
  { id: 'nextMatch', label: '下一个匹配', default: 'ArrowDown', note: '搜索栏打开、焦点不在输入框时' },
  { id: 'importBaseline', label: '导入校对稿', default: 'mod+p', note: '合意模式' },
  { id: 'undo', label: '撤销', default: 'mod+z' },
  { id: 'redo', label: '重做', default: 'mod+y' },
]

export const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)

/** Resolve the active combo for an action: user override or registry default. */
export function resolveCombo(shortcuts: Record<string, string> | undefined, id: string): string {
  const fromUser = shortcuts?.[id]
  if (fromUser) return fromUser
  const a = SHORTCUT_ACTIONS.find(x => x.id === id)
  return a ? a.default : ''
}

/** True if a KeyboardEvent matches a combo string. */
export function matchEvent(e: KeyboardEvent, combo: string): boolean {
  if (!combo) return false
  const parts = combo.split('+')
  const main = parts[parts.length - 1]
  const mods = parts.slice(0, -1)

  const wantMod = mods.includes('mod')
  const wantAlt = mods.includes('alt')
  const wantShift = mods.includes('shift')

  const hasMod = e.ctrlKey || e.metaKey
  if (wantMod !== hasMod) return false
  if (wantAlt !== e.altKey) return false
  if (wantShift !== e.shiftKey) return false

  // Main key compare: letters case-insensitive; named keys (ArrowUp…) exact.
  const k = e.key
  if (main.length === 1) return k.toLowerCase() === main.toLowerCase()
  return k === main
}

/** Build a combo string from a KeyboardEvent (for the settings recorder). */
export function comboFromEvent(e: KeyboardEvent): string | null {
  const k = e.key
  // Ignore pure modifier presses.
  if (['Control', 'Meta', 'Alt', 'Shift'].includes(k)) return null
  const mods: string[] = []
  if (e.ctrlKey || e.metaKey) mods.push('mod')
  if (e.altKey) mods.push('alt')
  if (e.shiftKey) mods.push('shift')
  const main = k.length === 1 ? k.toLowerCase() : k
  return [...mods, main].join('+')
}

/** Human-readable combo for display (platform-aware modifier symbols). */
export function formatCombo(combo: string): string {
  if (!combo) return ''
  const parts = combo.split('+')
  const main = parts[parts.length - 1]
  const mods = parts.slice(0, -1)
  const out: string[] = []
  for (const m of mods) {
    if (m === 'mod') out.push(isMac ? '⌘' : 'Ctrl')
    else if (m === 'alt') out.push(isMac ? '⌥' : 'Alt')
    else if (m === 'shift') out.push(isMac ? '⇧' : 'Shift')
  }
  const keyLabel: Record<string, string> = {
    ArrowUp: '↑', ArrowDown: '↓', ArrowLeft: '←', ArrowRight: '→',
  }
  out.push(keyLabel[main] || (main.length === 1 ? main.toUpperCase() : main))
  return out.join(isMac ? '' : '+')
}
