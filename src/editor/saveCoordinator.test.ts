import { describe, expect, it } from 'vitest'
import { SaveCoordinator } from './saveCoordinator'

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

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
