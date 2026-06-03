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
      const path = await save({
        title: '保存翻译文件',
        defaultPath: defaultName,
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
