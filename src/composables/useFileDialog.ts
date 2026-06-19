import { api } from '../api/client'
import type { DstTalk } from '../types/translation'
import type { SaveMetadata } from '../types/api'

const isTauri = typeof window !== 'undefined' && !!(window as any).__TAURI_INTERNALS__

export function useFileDialog() {
  async function openTranslation(): Promise<{
    talks: DstTalk[]
    meta: SaveMetadata | null
    filePath?: string
    fileName?: string
  } | null> {
    if (isTauri) {
      const { open } = await import('@tauri-apps/plugin-dialog')
      const path = await open({
        title: '打开翻译文件',
        filters: [{ name: '翻译文件', extensions: ['txt'] }],
      })
      if (!path) return null
      const result = await api.translationLoad(path as string)
      return { talks: result.talks, meta: result.meta, filePath: path as string }
    } else {
      return new Promise((resolve) => {
        const input = document.createElement('input')
        input.type = 'file'
        input.accept = '.txt'
        input.onchange = async () => {
          const file = input.files?.[0]
          if (!file) { resolve(null); return }
          try {
            const content = await file.text()
            const result = await api.translationLoadContent(content)
            resolve({ talks: result.talks, meta: result.meta, fileName: file.name })
          } catch {
            resolve(null)
          }
        }
        input.click()
      })
    }
  }

  async function saveTranslation(
    defaultName: string,
    talks: DstTalk[],
    saveN: boolean,
    meta?: SaveMetadata,
  ): Promise<string | null> {
    if (isTauri) {
      const { save } = await import('@tauri-apps/plugin-dialog')
      // macOS NSSavePanel rejects a defaultPath whose parent directory does not
      // exist ("The string did not match the expected pattern"). When the name
      // is a layered path (<base>/<type>/<index>/<file>.txt), create the parent
      // dirs first so the dialog can default into them. Best-effort: if it fails
      // we fall back to passing just the bare filename.
      let defaultPath = defaultName
      const isLayered = /[/\\]/.test(defaultName)
      if (isLayered) {
        try {
          await api.ensureDir(defaultName)
        } catch (e) {
          console.warn('[Save] ensureDir failed, falling back to bare filename', e)
          defaultPath = defaultName.split(/[/\\]/).pop() || defaultName
        }
      }
      const path = await save({
        title: '保存翻译文件',
        defaultPath,
        filters: [{ name: '翻译文件', extensions: ['txt'] }],
      })
      if (!path) return null
      console.log('[Save] writing file', { path, talkCount: talks.length, saveN, hasMeta: !!meta })
      await api.translationSave(path as string, talks, saveN, meta)
      return path as string
    } else {
      console.log('[Save] serializing for download', { defaultName, talkCount: talks.length });
      const { content } = await api.translationSerialize({ talks, saveN, meta })
      const blob = new Blob([content], { type: 'text/plain;charset=utf-8' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = defaultName
      a.click()
      URL.revokeObjectURL(url)
      return defaultName
    }
  }

  return { openTranslation, saveTranslation, isTauri }
}
