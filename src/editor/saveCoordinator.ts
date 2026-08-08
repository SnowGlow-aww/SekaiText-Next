export interface ManualSaveFeedback {
  message: string
  tone: 'success' | 'warn'
}

export type ManualSaveOutcome = 'saved' | 'stale' | 'recovery-failed'

export function manualSaveFeedback(outcome: ManualSaveOutcome): ManualSaveFeedback {
  switch (outcome) {
    case 'saved':
      return { message: '已保存', tone: 'success' }
    case 'recovery-failed':
      return {
        message: '文件已写入，但自动恢复快照清理失败；当前文档仍标记为未保存，请重试',
        tone: 'warn',
      }
    case 'stale':
      return { message: '已保存提交时的内容；期间发生了新修改，当前版本仍未保存', tone: 'warn' }
  }
}

export async function completeManualSave(options: {
  markSavedIfUnchanged: () => boolean
  syncRecovery: () => Promise<void>
  restoreUnsavedIfUnchanged: () => void
  onRecoveryError?: (error: unknown) => void
}): Promise<ManualSaveOutcome> {
  if (!options.markSavedIfUnchanged()) return 'stale'
  try {
    await options.syncRecovery()
    return 'saved'
  } catch (error) {
    options.restoreUnsavedIfUnchanged()
    options.onRecoveryError?.(error)
    return 'recovery-failed'
  }
}

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
