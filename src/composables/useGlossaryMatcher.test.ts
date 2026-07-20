import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { GlossaryEntry } from '../types/glossary'
import { useGlossaryMatcher } from './useGlossaryMatcher'

const glossaryMock = vi.hoisted(() => ({
  allEntries: [] as GlossaryEntry[],
  loadAllEntries: vi.fn(),
}))

vi.mock('../stores/glossary', () => ({
  useGlossaryStore: () => glossaryMock,
}))

function entry(id: string, source: string): GlossaryEntry {
  return { id, source, translation: `译-${id}`, category: '测试', origin: 'user' }
}

describe('glossary matcher index', () => {
  beforeEach(() => {
    glossaryMock.allEntries = []
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('matches the longest term without overlaps and rebuilds after cache replacement', () => {
    glossaryMock.allEntries = [entry('short', '世界'), entry('long', '世界计划')]
    const matcher = useGlossaryMatcher()

    expect(matcher.matchTerms('世界计划与世界')).toEqual([
      expect.objectContaining({ term: '世界计划', start: 0, end: 4, entry: expect.objectContaining({ id: 'long' }) }),
      expect.objectContaining({ term: '世界', start: 5, end: 7, entry: expect.objectContaining({ id: 'short' }) }),
    ])

    glossaryMock.allEntries = [entry('new', '新词')]
    expect(matcher.matchTerms('世界与新词')).toEqual([
      expect.objectContaining({ term: '新词', start: 3, end: 5, entry: expect.objectContaining({ id: 'new' }) }),
    ])
  })

  it('indexes 100k terms without compiling them into one oversized regular expression', () => {
    glossaryMock.allEntries = Array.from({ length: 100_000 }, (_, i) => entry(String(i), `术语${i}`))
    const NativeRegExp = RegExp
    class GuardedRegExp extends NativeRegExp {
      constructor(pattern: string | RegExp, flags?: string) {
        if (String(pattern).length > 10_000) throw new Error('oversized regular expression')
        super(pattern, flags)
      }
    }
    vi.stubGlobal('RegExp', GuardedRegExp)

    const matcher = useGlossaryMatcher()
    expect(() => matcher.matchTerms('这里有术语99999')).not.toThrow()
    expect(matcher.matchTerms('这里有术语99999')).toEqual([
      expect.objectContaining({ term: '术语99999', entry: expect.objectContaining({ id: '99999' }) }),
    ])
  })
})
