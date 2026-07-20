import { api } from '../api/client'
import type { RecoverySaveRequestV2 } from './recovery'
import {
  commitRecoveryRaw,
  discardStagedRecoveryRaw,
  forgetRecoveryRaw,
  stageRecoveryRaw,
} from './recovery'
import { SaveCoordinator } from './saveCoordinator'

const coordinator = new SaveCoordinator()
let recoveryClearPending = false

export function saveRecovery(request: RecoverySaveRequestV2): Promise<void> {
  return coordinator.run(async () => {
    const associationId = stageRecoveryRaw(request)
    try {
      await api.recoverySave(request)
      if (associationId) commitRecoveryRaw(associationId)
      else forgetRecoveryRaw()
      recoveryClearPending = false
    } catch (error) {
      if (associationId) discardStagedRecoveryRaw(associationId)
      throw error
    }
  })
}

export function clearRecovery(): Promise<void> {
  return coordinator.run(async () => {
    try {
      await api.recoveryClear()
      forgetRecoveryRaw()
      recoveryClearPending = false
    } catch (error) {
      recoveryClearPending = true
      throw error
    }
  })
}

export function hasPendingRecoveryClear(): boolean {
  return recoveryClearPending
}

export function waitForRecoveryWrites(): Promise<void> {
  return coordinator.wait()
}
