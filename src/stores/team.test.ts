import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useGlossaryStore } from './glossary'
import { useTeamStore } from './team'

const apiMock = vi.hoisted(() => ({
  teamSync: vi.fn(),
  glossaryEntries: vi.fn(),
}))

vi.mock('../api/client', () => ({ api: apiMock }))

describe('team glossary synchronization', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('refreshes the glossary matcher cache when remote entries changed', async () => {
    const entry = {
      id: 'synced',
      source: '同步词条',
      translation: '同步译文',
      category: '测试',
      origin: 'remote' as const,
    }
    apiMock.teamSync.mockResolvedValueOnce({ status: 'ok', version: 2, changed: true })
    apiMock.glossaryEntries.mockResolvedValueOnce({ items: [entry], total: 1 })
    const team = useTeamStore()
    const glossary = useGlossaryStore()
    team.connected = true

    await team.sync()

    expect(apiMock.glossaryEntries).toHaveBeenCalledWith('', 0, 100000)
    expect(glossary.allEntries).toEqual([entry])
    expect(glossary.allEntriesLoaded).toBe(true)
  })

  it('does not report a successful remote sync as failed when cache refresh rejects', async () => {
    const response = { status: 'ok', version: 3, changed: true }
    const refreshError = new Error('cache refresh failed')
    apiMock.teamSync.mockResolvedValueOnce(response)
    apiMock.glossaryEntries.mockRejectedValueOnce(refreshError)
    const warning = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const team = useTeamStore()
    team.connected = true
    team.syncError = 'previous error'

    await expect(team.sync(true)).resolves.toEqual(response)

    expect(team.syncError).toBe('')
    expect(team.lastSync).toEqual(expect.objectContaining({ changed: true, version: 3 }))
    expect(warning).toHaveBeenCalledWith(
      'Failed to refresh glossary matcher cache after team sync',
      refreshError,
    )
  })
})
