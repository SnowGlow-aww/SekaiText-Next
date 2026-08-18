import { describe, expect, it } from 'vitest'
import {
  canonicalStoryIdentity,
  formatManagedDocumentFileName,
  parseDocumentFileName,
  titlePartForParsedFile,
} from './documentFileName'

describe('managed translation filenames', () => {
  it('uses translated title directly in filename and recovers it on parse', () => {
    const fileName = formatManagedDocumentFileName({
      modeLabel: '翻译',
      saveTitle: '3rd-group5-06',
      chapterTitle: 'あなたの戦い方',
      titleOverride: '属于你的斗争方式',
    })

    expect(fileName).toBe('【翻译】3rd-group5-06 属于你的斗争方式.txt')
    const parsed = parseDocumentFileName(fileName)
    expect(parsed).toEqual({
      rawName: fileName,
      body: '3rd-group5-06 属于你的斗争方式',
      canonical: '3rd-group5-06 属于你的斗争方式',
      titlePart: '',
      hasExplicitTitle: false,
    })
    expect(titlePartForParsedFile(parsed, {
      saveTitle: '3rd-group5-06',
      chapterTitle: 'あなたの戦い方',
    })).toBe('属于你的斗争方式')
  })

  it('keeps backwards compatibility for legacy files containing explicit title marker', () => {
    const legacyFile = '【翻译】event211-airi 前篇【标题】爱莉前篇译文.txt'
    const parsed = parseDocumentFileName(legacyFile)
    expect(parsed).toEqual({
      rawName: legacyFile,
      body: 'event211-airi 前篇【标题】爱莉前篇译文',
      canonical: 'event211-airi 前篇',
      titlePart: '爱莉前篇译文',
      hasExplicitTitle: true,
    })
    expect(titlePartForParsedFile(parsed, {
      saveTitle: 'event211-airi',
      chapterTitle: '前篇',
    })).toBe('爱莉前篇译文')
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
