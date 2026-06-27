// Minimal promise-based IndexedDB blob store for personalisation assets
// (user-imported fonts + background image). localStorage can't hold multi-MB
// binaries; IndexedDB stores Blobs directly with no practical size cap and
// survives restarts. One object store, keyed by string.
const DB_NAME = 'sekaitext-personalization'
const STORE = 'assets'
const VERSION = 1

let dbPromise: Promise<IDBDatabase> | null = null

function openDb(): Promise<IDBDatabase> {
  if (dbPromise) return dbPromise
  dbPromise = new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, VERSION)
    req.onupgradeneeded = () => {
      const db = req.result
      if (!db.objectStoreNames.contains(STORE)) db.createObjectStore(STORE)
    }
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => reject(req.error)
  })
  return dbPromise
}

function run<T>(mode: IDBTransactionMode, fn: (s: IDBObjectStore) => IDBRequest): Promise<T> {
  return openDb().then(
    (db) =>
      new Promise<T>((resolve, reject) => {
        const t = db.transaction(STORE, mode)
        const req = fn(t.objectStore(STORE))
        req.onsuccess = () => resolve(req.result as T)
        req.onerror = () => reject(req.error)
      })
  )
}

export function idbPut(key: string, blob: Blob): Promise<void> {
  return run<void>('readwrite', (s) => s.put(blob, key))
}

export function idbGet(key: string): Promise<Blob | undefined> {
  return run<Blob | undefined>('readonly', (s) => s.get(key))
}

export function idbDel(key: string): Promise<void> {
  return run<void>('readwrite', (s) => s.delete(key))
}
