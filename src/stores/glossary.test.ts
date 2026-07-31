import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useGlossaryStore } from './glossary'

const apiMock = vi.hoisted(() => ({
  glossaryAppellationTargets: vi.fn(),
  glossaryEntries: vi.fn(),
}))

vi.mock('../api/client', () => ({ api: apiMock }))

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => { resolve = done })
  return { promise, resolve }
}

describe('glossary appellation navigation', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('keeps targets from the latest speaker when requests resolve out of order', async () => {
    const oldSpeaker = deferred<string[]>()
    const newSpeaker = deferred<string[]>()
    apiMock.glossaryAppellationTargets
      .mockReturnValueOnce(oldSpeaker.promise)
      .mockReturnValueOnce(newSpeaker.promise)
    const glossary = useGlossaryStore()

    const oldRequest = glossary.loadTargets('旧说话人')
    const newRequest = glossary.loadTargets('新说话人')
    newSpeaker.resolve(['新对象'])
    await newRequest
    oldSpeaker.resolve(['旧对象'])
    await oldRequest

    expect(glossary.targets).toEqual(['新对象'])
  })

  it('clears stale targets as soon as a new speaker request starts', async () => {
    const response = deferred<string[]>()
    apiMock.glossaryAppellationTargets.mockReturnValueOnce(response.promise)
    const glossary = useGlossaryStore()
    glossary.targets = ['旧对象']

    const pending = glossary.loadTargets('新说话人')

    expect(glossary.targets).toEqual([])
    response.resolve(['新对象'])
    await pending
    expect(glossary.targets).toEqual(['新对象'])
  })

  it('keeps the newest full-entry cache when forced loads resolve out of order', async () => {
    const oldResponse = deferred<{ items: Array<{ id: string; source: string; translation: string; category: string; origin: 'remote' }>; total: number }>()
    const newResponse = deferred<{ items: Array<{ id: string; source: string; translation: string; category: string; origin: 'remote' }>; total: number }>()
    apiMock.glossaryEntries
      .mockReturnValueOnce(oldResponse.promise)
      .mockReturnValueOnce(newResponse.promise)
    const glossary = useGlossaryStore()
    const oldEntry = { id: 'old', source: '旧词', translation: '旧译', category: '测试', origin: 'remote' as const }
    const newEntry = { id: 'new', source: '新词', translation: '新译', category: '测试', origin: 'remote' as const }

    const oldRequest = glossary.loadAllEntries(true)
    const newRequest = glossary.loadAllEntries(true)
    newResponse.resolve({ items: [newEntry], total: 1 })
    await newRequest
    oldResponse.resolve({ items: [oldEntry], total: 1 })
    await oldRequest

    expect(apiMock.glossaryEntries).toHaveBeenCalledTimes(2)
    expect(glossary.allEntries).toEqual([newEntry])
    expect(glossary.allEntriesLoaded).toBe(true)
  })
})
