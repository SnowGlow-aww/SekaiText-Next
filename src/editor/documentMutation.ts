export interface DocumentVersion {
  documentRevision: number
  mutationSeq: number
}

export class DocumentMutationQueue {
  private tail: Promise<void> = Promise.resolve()

  run<T>(mutation: () => Promise<T>): Promise<T> {
    const result = this.tail.then(mutation, mutation)
    this.tail = result.then(() => undefined, () => undefined)
    return result
  }
}

function sameVersion(a: DocumentVersion, b: DocumentVersion): boolean {
  return a.documentRevision === b.documentRevision && a.mutationSeq === b.mutationSeq
}

export async function commitDocumentMutation<T>(
  getVersion: () => DocumentVersion,
  request: () => Promise<T>,
  apply: (result: T) => void,
): Promise<boolean> {
  const startedAt = getVersion()
  const result = await request()
  if (!sameVersion(startedAt, getVersion())) return false
  apply(result)
  return true
}

// Some backend editor operations are the only place that can materialize a
// missing dstTalks slot. If an unrelated edit happens while the request is in
// flight, discarding the response would leave the visible text unsavable. Re-run
// against the latest snapshot while the original row edit is still relevant.
export async function commitRebasedDocumentMutation<T>(
  getVersion: () => DocumentVersion,
  isRelevant: () => boolean,
  request: () => Promise<T>,
  apply: (result: T) => void,
  beforeValidate?: () => void | Promise<void>,
): Promise<boolean> {
  while (isRelevant()) {
    const startedAt = getVersion()
    const result = await request()
    await beforeValidate?.()
    if (!isRelevant()) return false
    if (!sameVersion(startedAt, getVersion())) continue
    apply(result)
    return true
  }
  return false
}
