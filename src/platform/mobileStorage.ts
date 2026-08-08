import type { Settings } from '../types/api'
import type { RecoveryLoadResult, RecoverySaveRequestV2, RecoveryStoredMode } from '../editor/recovery'
import { serializeRecoveryTalks } from '../editor/recovery'

const SETTINGS_KEY = 'sekaitext:android-settings-v1'
const RECOVERY_KEY = 'sekaitext:android-recovery-v2'
const RECOVERY_DB_NAME = 'sekaitext-mobile-recovery'
const RECOVERY_STORE_NAME = 'snapshots'
const RECOVERY_DB_KEY = 'recovery-v2'

const defaultSettings: Settings = {
  fontSize: 18,
  uiFontSize: 16,
  saveN: true,
  debugEnabled: false,
  indexOrder: 'asc',
  jsonDownloadDir: '',
  saveBaseDir: '',
  undoDepth: 20,
  keepHighlightWhenCompareOff: true,
  shortcuts: {},
  hideAgreementImportHint: false,
  live2dPosition: 'bottom',
}

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

export function loadMobileSettings(): Settings {
  try {
    const stored = JSON.parse(localStorage.getItem(SETTINGS_KEY) || 'null') as Partial<Settings> | null
    return { ...clone(defaultSettings), ...(stored ?? {}) }
  } catch {
    return clone(defaultSettings)
  }
}

export function saveMobileSettings(settings: Settings): Settings {
  const normalized = {
    ...clone(settings),
    jsonDownloadDir: '',
    saveBaseDir: '',
    live2dPosition: settings.live2dPosition === 'window' ? 'bottom' : settings.live2dPosition,
  }
  localStorage.setItem(SETTINGS_KEY, JSON.stringify(normalized))
  return clone(normalized)
}

export async function saveMobileRecovery(request: RecoverySaveRequestV2): Promise<{ status: string }> {
  const modes: RecoveryStoredMode[] = request.modes.map(mode => ({
    content: serializeRecoveryTalks(mode.talks, request.saveN),
    talks: clone(mode.editorTalks),
    dstTalks: clone(mode.talks),
    referTalks: clone(mode.referTalks),
    // Android content:// grants from the stock picker are process-scoped. Do
    // not persist a stale URI into crash recovery; restored documents must use
    // Save/Save As to acquire a fresh writable destination.
    filePath: mode.filePath.startsWith('content://') ? '' : mode.filePath,
    editorMode: mode.editorMode,
    titleOverride: mode.titleOverride,
    hasUnsavedChanges: mode.hasUnsavedChanges,
    sourceTalks: clone(mode.sourceTalks),
    docMeta: mode.docMeta ? { ...mode.docMeta } : null,
    storyType: mode.docMeta?.type,
    storySort: mode.docMeta?.sort,
    storyIndex: mode.docMeta?.index,
    storyChapter: mode.docMeta?.chapter,
    storySource: mode.docMeta?.source,
  }))
  const serialized = JSON.stringify({
    exists: modes.length > 0,
    version: 2,
    activeMode: request.activeMode,
    modes,
    savedAt: new Date().toISOString(),
  } satisfies RecoveryLoadResult)

  let indexedDbError: unknown
  try {
    if (await writeIndexedRecovery(serialized)) {
      // The IndexedDB transaction is already committed. Failure (or process
      // death) while removing an older localStorage fallback must not turn this
      // successful write into a rollback; loading compares timestamps across
      // both stores and selects the newer snapshot.
      try { localStorage.removeItem(RECOVERY_KEY) } catch { /* stale fallback is harmless */ }
      return { status: 'ok' }
    }
  } catch (error) {
    indexedDbError = error
  }

  try {
    // Retrying after removing the previous value both frees its quota and
    // guarantees a failed larger write cannot leave an older snapshot looking current.
    try {
      localStorage.setItem(RECOVERY_KEY, serialized)
    } catch {
      localStorage.removeItem(RECOVERY_KEY)
      localStorage.setItem(RECOVERY_KEY, serialized)
    }
    await deleteIndexedRecovery().catch(() => {})
    return { status: 'ok' }
  } catch (localError) {
    try { localStorage.removeItem(RECOVERY_KEY) } catch { /* preserve the original write error */ }
    await deleteIndexedRecovery().catch(() => {})
    throw indexedDbError ?? localError
  }
}

function recoverySavedAt(result: RecoveryLoadResult): number {
  if (!result.exists || !result.savedAt) return Number.NEGATIVE_INFINITY
  const timestamp = Date.parse(result.savedAt)
  return Number.isFinite(timestamp) ? timestamp : Number.NEGATIVE_INFINITY
}

// Cross-store cleanup cannot be atomic: Android may stop the process after the
// new snapshot commits but before the older fallback is deleted. Always select
// the freshest complete record instead of trusting either storage by priority.
export function selectLatestMobileRecovery(
  local: RecoveryLoadResult,
  indexed: RecoveryLoadResult,
): RecoveryLoadResult {
  if (!local.exists) return indexed.exists ? indexed : { exists: false }
  if (!indexed.exists) return local
  return recoverySavedAt(indexed) > recoverySavedAt(local) ? indexed : local
}

export async function loadMobileRecovery(): Promise<RecoveryLoadResult> {
  let local: RecoveryLoadResult = { exists: false }
  try {
    local = parseStoredRecovery(localStorage.getItem(RECOVERY_KEY))
  } catch { /* localStorage may be unavailable; IndexedDB can still recover */ }

  try {
    const indexed = parseStoredRecovery(await readIndexedRecovery())
    return selectLatestMobileRecovery(local, indexed)
  } catch {
    return local
  }
}

export async function clearMobileRecovery(): Promise<{ status: string }> {
  localStorage.removeItem(RECOVERY_KEY)
  // IndexedDB is a second authoritative recovery store. If deletion fails, the
  // old snapshot can win on the next launch; propagate the failure so the
  // recovery/open transaction does not commit and recoveryCoordinator keeps a
  // pending clear for retry.
  await deleteIndexedRecovery()
  return { status: 'ok' }
}

function parseStoredRecovery(raw: string | null): RecoveryLoadResult {
  if (!raw) return { exists: false }
  try {
    const result = JSON.parse(raw) as RecoveryLoadResult | null
    return result?.exists ? result : { exists: false }
  } catch {
    return { exists: false }
  }
}

function openRecoveryDatabase(): Promise<IDBDatabase | null> {
  if (typeof indexedDB === 'undefined') return Promise.resolve(null)
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(RECOVERY_DB_NAME, 1)
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains(RECOVERY_STORE_NAME))
        request.result.createObjectStore(RECOVERY_STORE_NAME)
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error ?? new Error('IndexedDB open failed'))
  })
}

async function writeIndexedRecovery(serialized: string): Promise<boolean> {
  const database = await openRecoveryDatabase()
  if (!database) return false
  try {
    await runRecoveryTransaction(database, 'readwrite', store => store.put(serialized, RECOVERY_DB_KEY))
    return true
  } finally {
    database.close()
  }
}

async function readIndexedRecovery(): Promise<string | null> {
  const database = await openRecoveryDatabase()
  if (!database) return null
  try {
    return await new Promise<string | null>((resolve, reject) => {
      const transaction = database.transaction(RECOVERY_STORE_NAME, 'readonly')
      const request = transaction.objectStore(RECOVERY_STORE_NAME).get(RECOVERY_DB_KEY)
      request.onsuccess = () => resolve(typeof request.result === 'string' ? request.result : null)
      request.onerror = () => reject(request.error ?? new Error('IndexedDB recovery read failed'))
      transaction.onabort = () => reject(transaction.error ?? new Error('IndexedDB recovery read aborted'))
    })
  } finally {
    database.close()
  }
}

async function deleteIndexedRecovery(): Promise<void> {
  const database = await openRecoveryDatabase()
  if (!database) return
  try {
    await runRecoveryTransaction(database, 'readwrite', store => store.delete(RECOVERY_DB_KEY))
  } finally {
    database.close()
  }
}

function runRecoveryTransaction(
  database: IDBDatabase,
  mode: IDBTransactionMode,
  operation: (store: IDBObjectStore) => IDBRequest,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const transaction = database.transaction(RECOVERY_STORE_NAME, mode)
    const request = operation(transaction.objectStore(RECOVERY_STORE_NAME))
    request.onerror = () => reject(request.error ?? new Error('IndexedDB recovery operation failed'))
    transaction.oncomplete = () => resolve()
    transaction.onabort = () => reject(transaction.error ?? new Error('IndexedDB recovery transaction aborted'))
    transaction.onerror = () => reject(transaction.error ?? new Error('IndexedDB recovery transaction failed'))
  })
}
