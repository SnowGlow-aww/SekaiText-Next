import { describe, expect, it } from 'vitest'
import { completeManualSave, manualSaveFeedback, SaveCoordinator } from './saveCoordinator'

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

describe('manualSaveFeedback', () => {
  it('reports success only when the current editor version and recovery state are both saved', () => {
    expect(manualSaveFeedback('saved')).toEqual({ message: '已保存', tone: 'success' })
    expect(manualSaveFeedback('stale')).toEqual({
      message: '已保存提交时的内容；期间发生了新修改，当前版本仍未保存',
      tone: 'warn',
    })
    expect(manualSaveFeedback('recovery-failed')).toEqual({
      message: '文件已写入，但自动恢复快照清理失败；当前文档仍标记为未保存，请重试',
      tone: 'warn',
    })
  })
})

describe('completeManualSave', () => {
  it('does not touch recovery when the captured editor version is stale', async () => {
    const syncRecovery = async () => { throw new Error('must not run') }
    let restored = false

    await expect(completeManualSave({
      markSavedIfUnchanged: () => false,
      syncRecovery,
      restoreUnsavedIfUnchanged: () => { restored = true },
    })).resolves.toBe('stale')

    expect(restored).toBe(false)
  })

  it('reports full success only after recovery cleanup succeeds', async () => {
    await expect(completeManualSave({
      markSavedIfUnchanged: () => true,
      syncRecovery: async () => {},
      restoreUnsavedIfUnchanged: () => { throw new Error('must not run') },
    })).resolves.toBe('saved')
  })

  it('restores the dirty state and reports a warning outcome when recovery cleanup fails', async () => {
    const error = new Error('recovery offline')
    let restored = false
    let reported: unknown

    await expect(completeManualSave({
      markSavedIfUnchanged: () => true,
      syncRecovery: async () => { throw error },
      restoreUnsavedIfUnchanged: () => { restored = true },
      onRecoveryError: value => { reported = value },
    })).resolves.toBe('recovery-failed')

    expect(restored).toBe(true)
    expect(reported).toBe(error)
  })
})

describe('SaveCoordinator', () => {
  it('serializes automatic and manual saves through one queue', async () => {
    const coordinator = new SaveCoordinator()
    const releaseAuto = deferred()
    const order: string[] = []

    const auto = coordinator.run(async () => {
      order.push('auto:start')
      await releaseAuto.promise
      order.push('auto:end')
    })
    const manual = coordinator.run(async () => {
      order.push('manual:start')
      order.push('manual:end')
    })

    await Promise.resolve()
    expect(order).toEqual(['auto:start'])
    releaseAuto.resolve()
    await Promise.all([auto, manual])
    expect(order).toEqual(['auto:start', 'auto:end', 'manual:start', 'manual:end'])
  })

  it('continues after a failed save', async () => {
    const coordinator = new SaveCoordinator()
    await expect(coordinator.run(async () => { throw new Error('disk full') })).rejects.toThrow('disk full')
    await expect(coordinator.run(async () => 'saved')).resolves.toBe('saved')
  })
})
