import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { rebindContainedPath, useEditorStore } from './editor'
import type { DstTalk, SourceTalk } from '../types/translation'

function dst(text: string): DstTalk {
  return {
    idx: 1,
    speaker: '瑞希',
    text,
    start: true,
    end: true,
    checked: true,
    save: true,
    dstidx: 0,
  }
}

function source(text: string): SourceTalk {
  return { speaker: '瑞希', text, charIndex: 0 }
}

describe('editor document sessions', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('marks a user title edit dirty and includes it in the mode snapshot', () => {
    const editor = useEditorStore()

    editor.updateTitle('新标题')

    expect(editor.hasUnsavedChanges).toBe(true)
    expect(editor.captureModeStates()).toContainEqual(expect.objectContaining({
      mode: 0,
      titleOverride: '新标题',
      hasUnsavedChanges: true,
      recoveryPending: false,
    }))
  })

  it('keeps complete source context isolated per mode', () => {
    const editor = useEditorStore()
    editor.setTalks([dst('翻译')], [dst('翻译')], [])
    editor.setSourceTalks([source('原文 A')])

    editor.switchMode(1)
    expect(editor.sourceTalks).toEqual([])
    editor.setTalks([dst('校对')], [dst('校对')], [])
    editor.setSourceTalks([source('原文 B')])

    editor.switchMode(0)
    expect(editor.sourceTalks).toEqual([source('原文 A')])
    expect(editor.captureModeStates()).toEqual(expect.arrayContaining([
      expect.objectContaining({ mode: 0, sourceTalks: [source('原文 A')] }),
      expect.objectContaining({ mode: 1, sourceTalks: [source('原文 B')] }),
    ]))
  })

  it('does not let an old save clear edits made while it was in flight', () => {
    const editor = useEditorStore()
    editor.markUnsaved()
    const oldSave = editor.captureSaveVersion()

    editor.markUnsaved()

    expect(editor.markSavedIfUnchanged(oldSave)).toBe(false)
    expect(editor.hasUnsavedChanges).toBe(true)
    expect(editor.markSavedIfUnchanged(editor.captureSaveVersion())).toBe(true)
    expect(editor.hasUnsavedChanges).toBe(false)
  })

  it('can lock a mode switch without invalidating the save that must finish first', () => {
    const editor = useEditorStore()
    const revision = editor.documentRevision

    const token = editor.beginDocumentOperation(false)

    expect(token).not.toBeNull()
    expect(editor.documentBusy).toBe(true)
    expect(editor.documentRevision).toBe(revision)
    expect(editor.advanceDocumentOperation(token!)).toBe(true)
    expect(editor.documentRevision).toBe(revision + 1)
    editor.finishDocumentOperation(token!)
    expect(editor.documentBusy).toBe(false)
  })

  it('restores all recovered mode slots and activates the saved mode', () => {
    const editor = useEditorStore()
    const seqBeforeRestore = editor.mutationSeq
    const base = {
      referTalks: [],
      currentFilePath: '',
      titleOverride: '',
      hasUnsavedChanges: true,
      recoveryPending: true,
      majorClue: null,
      docMeta: null,
      mutationSeq: 0,
    }

    editor.restoreModeStates([
      { ...base, mode: 0, talks: [dst('翻译')], dstTalks: [dst('翻译')], sourceTalks: [source('原文 A')] },
      { ...base, mode: 1, talks: [dst('校对')], dstTalks: [dst('校对')], sourceTalks: [source('原文 B')], titleOverride: '校对标题' },
    ], 1)

    expect(editor.currentMode).toBe(1)
    expect(editor.talks[0].text).toBe('校对')
    expect(editor.sourceTalks[0].text).toBe('原文 B')
    expect(editor.titleOverride).toBe('校对标题')
    expect(editor.recoveryPending).toBe(true)
    expect(editor.mutationSeq).toBe(seqBeforeRestore)
    editor.switchMode(0)
    expect(editor.talks[0].text).toBe('翻译')
    expect(editor.recoveryPending).toBe(true)

    editor.updateTitle('恢复后真实编辑')
    expect(editor.recoveryPending).toBe(false)
    expect(editor.mutationSeq).toBeGreaterThan(seqBeforeRestore)

    editor.switchMode(1)
    expect(editor.recoveryPending).toBe(true)
  })
})

describe('editor path rebinding', () => {
  it('rewrites true descendants but not paths sharing only a string prefix', () => {
    expect(rebindContainedPath('/work/drafts/story.txt', '/work/drafts', '/archive/drafts')).toBe('/archive/drafts/story.txt')
    expect(rebindContainedPath('/work/drafts-old/story.txt', '/work/drafts', '/archive/drafts')).toBe('/work/drafts-old/story.txt')
  })

  it('matches Windows roots and skipped relative paths case-insensitively', () => {
    expect(rebindContainedPath('C:\\Docs\\Event\\story.txt', 'c:\\docs', 'D:\\Archive')).toBe('D:\\Archive\\Event\\story.txt')
    expect(rebindContainedPath(
      'C:\\Docs\\Event\\story.txt',
      'c:\\docs',
      'D:\\Archive',
      ['event/STORY.txt'],
    )).toBe('C:\\Docs\\Event\\story.txt')
  })

  it('keeps case-sensitive POSIX roots distinct when requested', () => {
    expect(rebindContainedPath('/Work/Drafts/story.txt', '/work/drafts', '/archive', [], false)).toBe('/Work/Drafts/story.txt')
  })
})
