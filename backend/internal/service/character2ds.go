package service

import "sync"

var (
	char2dOnce sync.Once
	char2dMap  map[int]cdnCharacter2D
)

// Character2dByID returns the character2ds master record for the given chara2d
// id, lazily fetching the master table once. On fetch failure the map stays nil
// and ok is false, so callers fall back gracefully (e.g. an empty partvoice URL).
func Character2dByID(id int) (cdnCharacter2D, bool) {
	char2dOnce.Do(func() {
		var cs []cdnCharacter2D
		if err := fetchCDNJSON("character2ds", &cs); err == nil {
			char2dMap = make(map[int]cdnCharacter2D, len(cs))
			for _, c := range cs {
				char2dMap[c.ID] = c
			}
		}
	})
	c, ok := char2dMap[id]
	return c, ok
}
