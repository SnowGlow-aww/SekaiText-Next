export interface DebugLogExportEntry {
  ts: string
  msg: string
  type: 'info' | 'warn' | 'error' | 'server'
}

const SECRET_KEYS = '(?:password|passwd|token|access[_-]?token|refresh[_-]?token|authorization|cookie|secret|api[_-]?key)'
const QUOTED_SECRET = new RegExp(`(["']?${SECRET_KEYS}["']?\\s*[:=]\\s*)(["'])(.*?)\\2`, 'gi')
const UNQUOTED_SECRET = new RegExp(`(\\b${SECRET_KEYS}\\b\\s*[:=]\\s*)(?:Bearer\\s+)?([^\\s,;}&\\]]+)`, 'gi')

export function redactDebugText(value: string): string {
  return value
    .replace(QUOTED_SECRET, '$1$2[REDACTED]$2')
    .replace(UNQUOTED_SECRET, '$1[REDACTED]')
    .replace(/\bBearer\s+[A-Za-z0-9._~+/=-]+/gi, 'Bearer [REDACTED]')
    .replace(/\/Users\/[^/\s]+/g, '/Users/[USER]')
    .replace(/\b([A-Za-z]:\\Users\\)[^\\/\s]+/gi, '$1[USER]')
}

export function formatDebugLogLines(entries: DebugLogExportEntry[]): string[] {
  return entries.map((entry) => {
    const tag = entry.type === 'server' ? 'server' : 'front'
    return redactDebugText(`[${entry.ts}] [${tag}] ${entry.msg}`)
  })
}
