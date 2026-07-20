export class SaveCoordinator {
  private tail: Promise<void> = Promise.resolve()

  run<T>(save: () => Promise<T>): Promise<T> {
    const result = this.tail.then(save, save)
    this.tail = result.then(() => undefined, () => undefined)
    return result
  }

  wait(): Promise<void> {
    return this.tail
  }
}
