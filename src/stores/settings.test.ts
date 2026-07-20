import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { rebaseSettings, useSettingsStore } from './settings'
import { saveDirectoryCoordinator } from '../editor/saveDirectoryCoordinator'

const apiMock = vi.hoisted(() => ({
  getSettings: vi.fn(),
  putSettings: vi.fn(),
  migrateSaveDir: vi.fn(),
}))

vi.mock('../api/client', () => ({ api: apiMock }))

describe('settings drafts', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('keeps persistent state unchanged when a draft is cancelled or fails to save', async () => {
    const settings = useSettingsStore()
    settings.settings.shortcuts = { save: 'mod+s' }
    const draft = settings.createDraft()
    draft.fontSize = 32
    draft.shortcuts!.save = 'mod+shift+s'

    expect(settings.settings.fontSize).toBe(18)
    expect(settings.settings.shortcuts).toEqual({ save: 'mod+s' })

    apiMock.putSettings.mockRejectedValueOnce(new Error('disk full'))
    await expect(settings.saveSettings(draft)).rejects.toThrow('disk full')
    expect(settings.settings.fontSize).toBe(18)
    expect(settings.settings.shortcuts).toEqual({ save: 'mod+s' })
  })

  it('commits a detached copy only after the backend accepts the draft', async () => {
    const settings = useSettingsStore()
    const draft = settings.createDraft()
    draft.fontSize = 24
    apiMock.putSettings.mockImplementationOnce(async (next) => next)

    await settings.saveSettings(draft)
    draft.fontSize = 30

    expect(settings.settings.fontSize).toBe(24)
  })

  it('rebases edited fields onto runtime-maintained settings', async () => {
    const settings = useSettingsStore()
    const base = settings.createDraft()
    const draft = settings.createDraft()
    draft.fontSize = 24
    settings.settings.seenTours = ['app-welcome']
    settings.settings.lastSeenVersion = '5.9.0'
    apiMock.putSettings.mockImplementationOnce(async (next) => next)

    await settings.saveSettings(draft, base)

    expect(apiMock.putSettings).toHaveBeenCalledWith(expect.objectContaining({
      fontSize: 24,
      seenTours: ['app-welcome'],
      lastSeenVersion: '5.9.0',
    }))
  })

  it('preserves a user draft when cold-loaded settings arrive', () => {
    const settings = useSettingsStore()
    const base = settings.createDraft()
    const draft = settings.createDraft()
    draft.fontSize = 31
    const loaded = { ...settings.createDraft(), uiFontSize: 20, seenTours: ['runtime'] }

    expect(rebaseSettings(base, draft, loaded)).toEqual(expect.objectContaining({
      fontSize: 31,
      uiFontSize: 20,
      seenTours: ['runtime'],
    }))
  })

  it('persists a save directory migration that completes during an older settings save', async () => {
    const settings = useSettingsStore()
    settings.settings.saveBaseDir = '/old'
    const base = settings.createDraft()
    const draft = settings.createDraft()
    draft.fontSize = 24
    let finishFirst!: (value: typeof draft) => void
    const firstResponse = new Promise<typeof draft>(resolve => { finishFirst = resolve })
    apiMock.putSettings
      .mockImplementationOnce(() => firstResponse)
      .mockImplementationOnce(async next => next)

    const saving = settings.saveSettings(draft, base)
    await vi.waitFor(() => expect(apiMock.putSettings).toHaveBeenCalledTimes(1))
    settings.settings.saveBaseDir = '/migrated'
    finishFirst({ ...draft, saveBaseDir: '/old' })
    await saving

    expect(apiMock.putSettings).toHaveBeenCalledTimes(2)
    expect(apiMock.putSettings.mock.calls[1][0]).toEqual(expect.objectContaining({
      fontSize: 24,
      saveBaseDir: '/migrated',
    }))
    expect(settings.settings.saveBaseDir).toBe('/migrated')
  })

  it('serializes save directory migration after an older settings write', async () => {
    const settings = useSettingsStore()
    settings.settings.saveBaseDir = '/old'
    const base = settings.createDraft()
    const draft = settings.createDraft()
    draft.fontSize = 24
    let finishSave!: (value: typeof draft) => void
    const firstResponse = new Promise<typeof draft>(resolve => { finishSave = resolve })
    apiMock.putSettings.mockImplementationOnce(() => firstResponse)
    apiMock.migrateSaveDir.mockResolvedValueOnce({
      oldDir: '/old', newDir: '/migrated', moved: 0, skipped: 0, skippedPaths: [],
    })

    const saving = settings.saveSettings(draft, base)
    await vi.waitFor(() => expect(apiMock.putSettings).toHaveBeenCalledOnce())
    const migrating = settings.migrateSaveDir('/migrated')
    expect(apiMock.migrateSaveDir).not.toHaveBeenCalled()

    finishSave({ ...draft, saveBaseDir: '/old' })
    await saving
    await migrating

    expect(apiMock.migrateSaveDir).toHaveBeenCalledWith('/migrated')
    expect(settings.settings.saveBaseDir).toBe('/migrated')
  })

  it('rebases an older queued settings draft after save directory migration', async () => {
    const settings = useSettingsStore()
    settings.settings.saveBaseDir = '/old'
    const base = settings.createDraft()
    const draft = settings.createDraft()
    draft.fontSize = 24
    let finishMigration!: (value: {
      oldDir: string
      newDir: string
      moved: number
      skipped: number
      skippedPaths: string[]
    }) => void
    apiMock.migrateSaveDir.mockImplementationOnce(() => new Promise(resolve => { finishMigration = resolve }))
    apiMock.putSettings.mockImplementationOnce(async next => next)

    const migrating = settings.migrateSaveDir('/migrated')
    await vi.waitFor(() => expect(apiMock.migrateSaveDir).toHaveBeenCalledOnce())
    const saving = settings.saveSettings(draft, base)
    expect(apiMock.putSettings).not.toHaveBeenCalled()

    finishMigration({ oldDir: '/old', newDir: '/migrated', moved: 0, skipped: 0, skippedPaths: [] })
    await migrating
    await saving

    expect(apiMock.putSettings).toHaveBeenCalledWith(expect.objectContaining({
      fontSize: 24,
      saveBaseDir: '/migrated',
    }))
    expect(settings.settings.saveBaseDir).toBe('/migrated')
  })

  it('does not start migration until an in-flight document transaction finishes', async () => {
    const settings = useSettingsStore()
    let releaseSave!: () => void
    const saveBlocked = new Promise<void>(resolve => { releaseSave = resolve })
    const save = saveDirectoryCoordinator.run(() => saveBlocked)
    apiMock.migrateSaveDir.mockResolvedValueOnce({
      oldDir: '/old', newDir: '/new', moved: 1, skipped: 0, skippedPaths: [],
    })

    const migration = settings.migrateSaveDir('/new')
    await Promise.resolve()
    expect(apiMock.migrateSaveDir).not.toHaveBeenCalled()

    releaseSave()
    await save
    await migration
    expect(apiMock.migrateSaveDir).toHaveBeenCalledWith('/new')
  })

  it('publishes path rebinding before a queued document transaction resumes', async () => {
    const settings = useSettingsStore()
    let finishMigration!: (value: {
      oldDir: string
      newDir: string
      moved: number
      skipped: number
      skippedPaths: string[]
    }) => void
    apiMock.migrateSaveDir.mockImplementationOnce(() => new Promise(resolve => { finishMigration = resolve }))
    let rebound = false

    const migration = settings.migrateSaveDir('/new', () => { rebound = true })
    await vi.waitFor(() => expect(apiMock.migrateSaveDir).toHaveBeenCalledOnce())
    const save = saveDirectoryCoordinator.run(async () => {
      expect(rebound).toBe(true)
    })

    finishMigration({ oldDir: '/old', newDir: '/new', moved: 1, skipped: 0, skippedPaths: [] })
    await Promise.all([migration, save])
  })
})
