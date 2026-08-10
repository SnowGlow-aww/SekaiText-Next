import { describe, expect, it } from 'vitest'
import source from './EditorPage.vue?raw'

describe('EditorPage explicit save flow', () => {
  it('keeps real document writes behind the save picker and uses recovery for edits', () => {
    expect(source).toContain('watch(() => editor.mutationSeq')
    expect(source).toContain('autoSave.schedule()')
    expect(source).toContain('fileDialog.saveTranslation(')
    expect(source).not.toContain('api.translationSave(')
    expect(source).not.toContain('writeTxtAutosave')
  })

  it('preserves a custom bound path as the next picker default and warns before replacement', () => {
    expect(source).toContain('editor.currentFilePath || canonicalSavePath() || canonicalFileName()')
    expect(source).toContain('此操作会覆盖原有文件')
    expect(source).toContain("confirmText: '覆盖原有文件'")
  })
})
