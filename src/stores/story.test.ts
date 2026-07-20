import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useStoryStore } from './story'

const apiMock = vi.hoisted(() => ({
  storySorts: vi.fn(),
  storyIndex: vi.fn(),
  storyChapter: vi.fn(),
}))

vi.mock('../api/client', () => ({ api: apiMock }))

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => { resolve = done })
  return { promise, resolve }
}

describe('story navigation requests', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('does not restore old child lists after their parent selections are cleared', async () => {
    const sorts = deferred<{ label: string; value: string }[]>()
    const indices = deferred<{ label: string; value: string }[]>()
    const chapters = deferred<{ number: number; label: string }[]>()
    apiMock.storySorts.mockReturnValueOnce(sorts.promise)
    apiMock.storyIndex.mockReturnValueOnce(indices.promise)
    apiMock.storyChapter.mockReturnValueOnce(chapters.promise)
    const story = useStoryStore()

    story.selectedType = 'event'
    const sortsRequest = story.fetchSorts('event')
    story.selectedType = ''

    story.selectedType = 'event'
    story.selectedSort = 'unit'
    const indexRequest = story.fetchIndex('event', 'unit')
    story.selectedSort = ''

    story.selectedSort = 'unit'
    story.selectedIndex = '001'
    const chapterRequest = story.fetchChapters('event', 'unit', '001')
    story.selectedIndex = ''

    sorts.resolve([{ label: '旧排序', value: 'old-sort' }])
    indices.resolve([{ label: '旧索引', value: 'old-index' }])
    chapters.resolve([{ number: 1, label: '旧章节' }])
    await Promise.all([sortsRequest, indexRequest, chapterRequest])

    expect(story.sorts).toEqual([])
    expect(story.indices).toEqual([])
    expect(story.chapters).toEqual([])
  })
})
