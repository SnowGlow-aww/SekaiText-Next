import { describe, expect, it } from 'vitest'
import {
  canonicalStoryIdentity,
  formatManagedDocumentFileName,
  parseDocumentFileName,
  titlePartForParsedFile,
} from './documentFileName'

describe('managed translation filenames', () => {
  it('keeps SaveTitle and ChapterTitle canonical while storing a translated title separately', () => {
    const fileName = formatManagedDocumentFileName({
      modeLabel: '翻译',
      saveTitle: 'event211-airi',
      chapterTitle: '前篇',
      titleOverride: '爱莉前篇译文',
    })

    expect(fileName).toBe('【翻译】event211-airi 前篇【标题】爱莉前篇译文.txt')
    expect(parseDocumentFileName(fileName)).toEqual({
      rawName: fileName,
      body: 'event211-airi 前篇【标题】爱莉前篇译文',
      canonical: 'event211-airi 前篇',
      titlePart: '爱莉前篇译文',
      hasExplicitTitle: true,
    })
  })

  it('does not append a redundant title suffix for the canonical chapter title', () => {
    expect(formatManagedDocumentFileName({
      modeLabel: '翻译',
      saveTitle: 'Special Story SaveTitle With Spaces',
      chapterTitle: '',
      titleOverride: '',
    })).toBe('【翻译】Special Story SaveTitle With Spaces.txt')
  })

  it('recovers a legacy title only against the complete loaded identity', () => {
    const parsed = parseDocumentFileName('【翻译】Special Story SaveTitle With Spaces 自定义标题.txt')
    expect(parsed.canonical).toBe('Special Story SaveTitle With Spaces 自定义标题')
    expect(titlePartForParsedFile(parsed, {
      saveTitle: 'Special Story SaveTitle With Spaces',
      chapterTitle: '',
    })).toBe('自定义标题')
  })

  it('preserves the canonical alias for a special chapter', () => {
    expect(canonicalStoryIdentity('fes202204-shiho', '特殊篇')).toBe('fes202204-shiho 特殊篇')
    expect(parseDocumentFileName('【翻译】fes202204-shiho 其他.txt').canonical).toBe('fes202204-shiho 其他')
  })
})
