import { api } from '../api/client'
import { capabilities } from '../platform/capabilities'
import { mobileCore } from '../platform/mobileCore'
import type { DstTalk } from '../types/translation'
import type { SaveMetadata } from '../types/api'

const isTauri = capabilities.isTauri

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
      if (!path || typeof path !== 'string') return null
      if (capabilities.isAndroid) {
        const { readTextFile } = await import('@tauri-apps/plugin-fs')
        // Android's picker returns an opaque content:// URI. The fs plugin
        // accepts that URI as a string; wrapping it in URL rejects non-file
        // schemes before the native Android resolver can handle it.
        const content = await readTextFile(path)
        const result = await mobileCore.loadTranslation(content)
        // The stock dialog opens with a temporary read grant and does not take
        // a persistable grant. Treat this as an import, not a writable binding;
        // the first Save creates a fresh document URI through the save picker.
        // Keep the provider's display-name-like tail for title/mode inference.
        const fileName = decodeURIComponent(path.split('/').pop() || '').replace(/^document:/, '')
        return { talks: result.talks, meta: result.meta, fileName }
      }
      const result = await api.translationLoad(path)
      return { talks: result.talks, meta: result.meta, filePath: path }
    } else {
      return new Promise((resolve, reject) => {
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
          } catch (error) {
            // A selected file that cannot be read or parsed is a real open
            // failure, not picker cancellation. Reject so EditorPage can keep
            // the current document and show its normal “打开失败” feedback.
            reject(error)
          }
        }
        // The file picker fires no `change` event when the user cancels, so the
        // Promise would hang forever. Modern browsers emit `cancel` on the input
        // in that case; listen for it to resolve(null) and release the closure.
        input.addEventListener('cancel', () => resolve(null))
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
      // exist ("The string did not match the expected pattern"). Android's SAF
      // owns its destination and only accepts a display filename/content URI.
      let defaultPath = capabilities.isAndroid
        ? (defaultName.split(/[/\\]/).pop() || defaultName)
        : defaultName
      const isLayered = /[/\\]/.test(defaultName)
      if (!capabilities.isAndroid && isLayered) {
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
      if (capabilities.isAndroid) {
        const [{ writeTextFile }, serialized] = await Promise.all([
          import('@tauri-apps/plugin-fs'),
          mobileCore.serializeTranslation({ talks, saveN, meta }),
        ])
        await writeTextFile(path, serialized.content)
      } else {
        await api.translationSave(path, talks, saveN, meta)
      }
      return path
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

  // Native directory picker (Finder / Explorer). Tauri-only — callers should
  // hide the entry point in the browser (isTauri) instead of handling null.
  async function pickDirectory(title: string): Promise<string | null> {
    if (!isTauri) return null
    const { open } = await import('@tauri-apps/plugin-dialog')
    const dir = await open({ title, directory: true })
    return typeof dir === 'string' && dir ? dir : null
  }

  return { openTranslation, saveTranslation, pickDirectory, isTauri }
}
