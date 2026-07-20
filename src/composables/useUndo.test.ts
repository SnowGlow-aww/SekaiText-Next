import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import { clearUndoHistory, useUndo } from './useUndo'
import type { DstTalk } from '../types/translation'

vi.mock('../stores/settings', () => ({
  useSettingsStore: () => ({ settings: ref({ undoDepth: 20 }) }),
}))

function talk(text: string): DstTalk {
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

describe('undo document history', () => {
  beforeEach(() => {
    clearUndoHistory()
  })

  it('cannot undo into the previous story after a new document starts', () => {
    const undo = useUndo()
    undo.pushSnapshot([talk('旧剧情')], [talk('旧剧情')])

    clearUndoHistory()

    expect(undo.canUndo.value).toBe(false)
    expect(undo.undo([talk('新剧情')], [talk('新剧情')])).toBeNull()
  })
})
