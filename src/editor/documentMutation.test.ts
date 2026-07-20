import { describe, expect, it } from 'vitest'
import { commitDocumentMutation, commitRebasedDocumentMutation, DocumentMutationQueue } from './documentMutation'

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => { resolve = done })
  return { promise, resolve }
}

describe('commitDocumentMutation', () => {
  it.each(['undo', 'replace all', 'speaker replacement'])(
    'does not apply an in-flight changeText response after %s',
    async () => {
      let version = { documentRevision: 3, mutationSeq: 8 }
      let applied = ''
      const response = deferred<string>()

      const pending = commitDocumentMutation(
        () => version,
        () => response.promise,
        result => { applied = result },
      )
      version = { ...version, mutationSeq: 9 }
      response.resolve('stale server snapshot')

      await expect(pending).resolves.toBe(false)
      expect(applied).toBe('')
    },
  )

  it('applies the response while the same document version is current', async () => {
    const version = { documentRevision: 3, mutationSeq: 8 }
    let applied = ''

    await expect(commitDocumentMutation(
      () => version,
      async () => 'current server snapshot',
      result => { applied = result },
    )).resolves.toBe(true)
    expect(applied).toBe('current server snapshot')
  })
})

describe('commitRebasedDocumentMutation', () => {
  it('re-runs against the latest snapshot when an unrelated edit races the response', async () => {
    let version = { documentRevision: 3, mutationSeq: 8 }
    const first = deferred<string>()
    let calls = 0
    let applied = ''

    const pending = commitRebasedDocumentMutation(
      () => version,
      () => true,
      () => ++calls === 1 ? first.promise : Promise.resolve('rebased snapshot'),
      result => { applied = result },
    )
    version = { ...version, mutationSeq: 9 }
    first.resolve('stale snapshot')

    await expect(pending).resolves.toBe(true)
    expect(calls).toBe(2)
    expect(applied).toBe('rebased snapshot')
  })

  it('stops without applying when the original row edit is no longer relevant', async () => {
    const response = deferred<string>()
    let relevant = true
    let applied = ''
    const pending = commitRebasedDocumentMutation(
      () => ({ documentRevision: 3, mutationSeq: 8 }),
      () => relevant,
      () => response.promise,
      result => { applied = result },
    )
    relevant = false
    response.resolve('stale snapshot')
    await expect(pending).resolves.toBe(false)
    expect(applied).toBe('')
  })

  it('re-runs when focused input is materialized before response validation', async () => {
    let version = { documentRevision: 3, mutationSeq: 8 }
    let calls = 0
    let applied = ''

    await expect(commitRebasedDocumentMutation(
      () => version,
      () => true,
      async () => ++calls === 1 ? 'snapshot before focused edit' : 'snapshot with focused edit',
      result => { applied = result },
      () => {
        if (calls === 1) version = { ...version, mutationSeq: 9 }
      },
    )).resolves.toBe(true)

    expect(calls).toBe(2)
    expect(applied).toBe('snapshot with focused edit')
  })
})

describe('DocumentMutationQueue', () => {
  it('runs a rapid second structural operation against the first result', async () => {
    const queue = new DocumentMutationQueue()
    const first = deferred<void>()
    const rows = ['a']

    const addFirst = queue.run(async () => {
      await first.promise
      rows.push('b')
    })
    const addSecond = queue.run(async () => {
      expect(rows).toEqual(['a', 'b'])
      rows.push('c')
    })

    first.resolve()
    await Promise.all([addFirst, addSecond])
    expect(rows).toEqual(['a', 'b', 'c'])
  })
})
