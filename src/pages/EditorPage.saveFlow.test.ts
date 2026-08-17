import { describe, expect, it } from 'vitest'
import source from './EditorPage.vue?raw'

describe('EditorPage explicit save flow', () => {
  it('supports configurable silent save and prompt save modes while protecting recovery', () => {
    expect(source).toContain('watch(() => editor.mutationSeq')
    expect(source).toContain('autoSave.schedule()')
    expect(source).toContain('fileDialog.saveTranslation(')
    expect(source).toContain('settings.settings.confirmSavePath')
    expect(source).not.toContain('writeTxtAutosave')
  })

  it('preserves a custom bound path as the next picker default and warns before replacement', () => {
    expect(source).toContain('editor.currentFilePath || canonicalSavePath()')
    expect(source).toContain('此操作会覆盖原有文件')
    expect(source).toContain("confirmText: '覆盖原有文件'")
  })
})
