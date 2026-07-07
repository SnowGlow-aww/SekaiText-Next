package service

import "sync"

var (
	char2dMu  sync.RWMutex
	char2dMap map[int]cdnCharacter2D // nil until a fetch succeeds; immutable once published
)

// Character2dByID returns the character2ds master record for the given chara2d
// id, lazily fetching the master table on first use. The result is cached only
// on a successful fetch: a transient failure (offline, DNS blip, 502) leaves the
// cache empty so a later call retries, instead of disabling partvoice resolution
// for the whole session. While the map is still nil, ok is false and callers
// fall back gracefully (e.g. an empty partvoice URL).
func Character2dByID(id int) (cdnCharacter2D, bool) {
	// Fast path: once a fetch has succeeded, char2dMap is never mutated again
	// (only ever replaced wholesale under the write lock), so a snapshot taken
	// here can be read without further locking.
	char2dMu.RLock()
	m := char2dMap
	char2dMu.RUnlock()

	if m == nil {
		char2dMu.Lock()
		// Re-check under the write lock: another goroutine may have populated
		// the map while we waited. Fetch single-flighted so a burst of lookups
		// can't stampede the CDN.
		if char2dMap == nil {
			var cs []cdnCharacter2D
			if err := fetchCDNJSON("character2ds", &cs); err == nil {
				built := make(map[int]cdnCharacter2D, len(cs))
				for _, c := range cs {
					built[c.ID] = c
				}
				char2dMap = built
			}
		}
		m = char2dMap
		char2dMu.Unlock()
	}

	if m == nil {
		return cdnCharacter2D{}, false
	}
	c, ok := m[id]
	return c, ok
}
