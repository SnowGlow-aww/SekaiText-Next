package service

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"sekaitext/backend/internal/model"
)

// GlossaryStore is the in-memory term library backed by a single JSON file on
// disk. It owns search, CRUD, Excel import (see glossary_import.go), and a
// background hot-reload that picks up external edits / server-pushed files.
//
// The store is deliberately backend-owned: the same Go service can later run on
// a server and the frontend (which only talks to the REST API) needs zero
// changes to switch data sources.
type GlossaryStore struct {
	mu           sync.RWMutex
	entries      []model.GlossaryEntry
	appellations []model.Appellation
	grammar      []model.GrammarUsage

	path     string // {dataDir}/resources/glossary/glossary.json
	lastMod  time.Time
	stopPoll chan struct{}
}

// WriteSyncBackup 把一次下行同步拉到的服务器全量 JSON 滚动存档到
// {glossary目录}/backups/glossary-时间戳.json，保留最近 10 份——误覆盖/误合并后
// 可从任一近期服务器状态回滚（也可直接把备份文件重新上传至线上）。
func (s *GlossaryStore) WriteSyncBackup(raw []byte) {
	dir := filepath.Join(filepath.Dir(s.path), "backups")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	name := "glossary-" + time.Now().Format("20060102-150405") + ".json"
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0644); err != nil {
		return
	}
	// 修剪：按文件名排序（时间戳命名即时间序），只留最近 10 份
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "glossary-") && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > 10 {
		_ = os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}

// NewGlossaryStore creates the store, ensures its directory exists, loads any
// existing JSON, and starts the hot-reload poller.
func NewGlossaryStore(dataDir string) *GlossaryStore {
	dir := filepath.Join(dataDir, "resources", "glossary")
	_ = os.MkdirAll(dir, 0755)
	s := &GlossaryStore{
		path:     filepath.Join(dir, "glossary.json"),
		stopPoll: make(chan struct{}),
	}
	if err := s.load(); err != nil {
		log.Printf("[glossary] initial load: %v (starting empty)", err)
	}
	go s.pollReload()
	return s
}

// --- persistence ---

// load reads the JSON file into memory (replacing current contents). A missing
// file is not an error — the store simply starts empty.
func (s *GlossaryStore) load() error {
	info, err := os.Stat(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var gd model.GlossaryData
	if len(data) > 0 {
		if err := json.Unmarshal(data, &gd); err != nil {
			return err
		}
	}
	// Migrate entry ids to the current makeEntryID scheme (older files keyed
	// entries by (source,category) only) and drop any rows that now resolve to the
	// same id, so the in-memory set always has unique ids for the frontend :key.
	out := make([]model.GlossaryEntry, 0, len(gd.Entries))
	pos := make(map[string]int, len(gd.Entries))
	for _, e := range gd.Entries {
		e.ID = makeEntryID(e)
		if idx, ok := pos[e.ID]; ok {
			// Collision on the migrated id: the "user rows always survive" invariant
			// must hold. Only overwrite when the existing row is NOT user-authored, or
			// the incoming row IS — i.e. never let an import/remote entry clobber a
			// user-authored one. (Among same-origin collisions, last occurrence wins.)
			// App paths can't produce two rows with the same (source,category,subCategory),
			// so this only fires on hand-edited JSON or an old-scheme migration; log the
			// dropped row so that data loss is diagnosable rather than silent.
			log.Printf("[glossary] load: dropping duplicate id %s (source=%q category=%q subCategory=%q)", e.ID, e.Source, e.Category, e.SubCategory)
			if out[idx].Origin != model.OriginUser || e.Origin == model.OriginUser {
				out[idx] = e
			}
			continue
		}
		pos[e.ID] = len(out)
		out = append(out, e)
	}
	gd.Entries = out
	s.mu.Lock()
	s.entries = gd.Entries
	s.appellations = gd.Appellations
	s.grammar = gd.Grammar
	s.lastMod = info.ModTime()
	s.mu.Unlock()
	log.Printf("[glossary] loaded %d entries, %d appellations, %d grammar", len(gd.Entries), len(gd.Appellations), len(gd.Grammar))
	return nil
}

// persist writes the current in-memory data to disk and refreshes lastMod so
// the poller doesn't treat our own write as an external change.
func (s *GlossaryStore) persist() error {
	gd := model.GlossaryData{Entries: s.entries, Appellations: s.appellations, Grammar: s.grammar}
	data, err := json.MarshalIndent(gd, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	// Write to a sibling temp file and atomically rename into place: a mid-write
	// failure (disk full / crash) then leaves the previous good file untouched
	// instead of a truncated half-file that the mtime poller would reload as
	// corrupt state. Combined with callers rolling back memory on a persist
	// error, this keeps the memory==disk invariant.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if info, err := os.Stat(s.path); err == nil {
		s.lastMod = info.ModTime()
	}
	return nil
}

// pollReload watches the file mtime every 2s and reloads on external change.
// Polling (not fsnotify) keeps the dependency surface small and behaves
// predictably on macOS where the app data dir lives.
func (s *GlossaryStore) pollReload() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopPoll:
			return
		case <-ticker.C:
			info, err := os.Stat(s.path)
			if err != nil {
				continue
			}
			s.mu.RLock()
			changed := info.ModTime().After(s.lastMod)
			s.mu.RUnlock()
			if changed {
				log.Printf("[glossary] file changed on disk, reloading")
				if err := s.load(); err != nil {
					log.Printf("[glossary] reload failed: %v", err)
				}
			}
		}
	}
}

// Reload forces a re-read from disk (manual trigger behind POST /glossary/reload).
func (s *GlossaryStore) Reload() error { return s.load() }

// --- read ---

// Categories returns each category (sheet) name with its entry count, sorted by
// descending count.
func (s *GlossaryStore) Categories() []map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	counts := map[string]int{}
	order := []string{}
	for _, e := range s.entries {
		if _, ok := counts[e.Category]; !ok {
			order = append(order, e.Category)
		}
		counts[e.Category]++
	}
	out := make([]map[string]interface{}, 0, len(order))
	for _, c := range order {
		out = append(out, map[string]interface{}{"category": c, "count": counts[c]})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i]["count"].(int) > out[j]["count"].(int)
	})
	return out
}

// cloneEntries returns a deep copy of src (a fresh slice header plus a fresh copy
// of each entry's Aliases) so the caller can serialize/hold it outside the lock
// without racing the store's in-place writes (AddEntry/UpdateEntry/DeleteEntry/
// MergeImport all mutate the shared backing array in place).
func cloneEntries(src []model.GlossaryEntry) []model.GlossaryEntry {
	if src == nil {
		return nil
	}
	out := make([]model.GlossaryEntry, len(src))
	copy(out, src)
	for i := range out {
		if len(src[i].Aliases) > 0 {
			out[i].Aliases = append([]string(nil), src[i].Aliases...)
		}
	}
	return out
}

// Entries returns a page of all entries (optionally filtered by category).
func (s *GlossaryStore) Entries(category string, offset, limit int) ([]model.GlossaryEntry, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	filtered := s.entries
	if category != "" {
		filtered = make([]model.GlossaryEntry, 0)
		for _, e := range s.entries {
			if e.Category == category {
				filtered = append(filtered, e)
			}
		}
	}
	total := len(filtered)
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := total
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	// Return a defensive deep copy: the handler serializes this outside the lock,
	// so it must not alias s.entries' backing array (which concurrent writes shift
	// in place). The category-filtered branch above already built a fresh slice,
	// but its elements still share the original Aliases arrays — clone both.
	return cloneEntries(filtered[offset:end]), total
}

// Search matches q against source, translation and aliases (case-insensitive),
// ranked exact > prefix > substring. Optionally scoped to one category.
func (s *GlossaryStore) Search(q, category string, limit int) []model.GlossaryEntry {
	q = strings.TrimSpace(q)
	if q == "" {
		return []model.GlossaryEntry{}
	}
	lq := strings.ToLower(q)
	s.mu.RLock()
	defer s.mu.RUnlock()

	type scored struct {
		e     model.GlossaryEntry
		score int
	}
	var hits []scored
	for _, e := range s.entries {
		if category != "" && e.Category != category {
			continue
		}
		score := matchScore(lq, e)
		if score > 0 {
			hits = append(hits, scored{e, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		return hits[i].score > hits[j].score
	})
	out := make([]model.GlossaryEntry, 0, len(hits))
	for _, h := range hits {
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, h.e)
	}
	return out
}

// matchScore: 3=exact, 2=prefix, 1=substring, 0=no match. Checks source,
// translation and every alias; returns the strongest field match.
func matchScore(lq string, e model.GlossaryEntry) int {
	best := 0
	consider := func(field string) {
		lf := strings.ToLower(field)
		if lf == "" {
			return
		}
		var sc int
		switch {
		case lf == lq:
			sc = 3
		case strings.HasPrefix(lf, lq):
			sc = 2
		case strings.Contains(lf, lq):
			sc = 1
		}
		if sc > best {
			best = sc
		}
	}
	consider(e.Source)
	consider(e.Translation)
	for _, a := range e.Aliases {
		consider(a)
	}
	return best
}

// --- write (CRUD) ---

// makeID derives a stable id from source+category so re-imports of the same row
// keep the same id (and edits/deletes remain stable across reloads).
func makeID(source, category string) string {
	h := sha1.Sum([]byte(source + "\x00" + category))
	return hex.EncodeToString(h[:8])
}

// makeEntryID derives a stable id from the fields that distinguish one entry from
// another: source + category + subCategory. Keying on (source,category) alone
// collapsed genuinely-distinct entries — e.g. the same term under different
// subcategories — onto one id, so they overwrote each other on import and the
// frontend (keyed by id) rendered only one. Re-importing identical content still
// yields the same id, so dedup/update-in-place stays idempotent.
func makeEntryID(e model.GlossaryEntry) string {
	h := sha1.Sum([]byte(e.Source + "\x00" + e.Category + "\x00" + e.SubCategory))
	return hex.EncodeToString(h[:8])
}

// AddEntry inserts a user-authored entry (Origin=user) and persists.
func (s *GlossaryStore) AddEntry(e model.GlossaryEntry) (model.GlossaryEntry, error) {
	e.Origin = model.OriginUser
	if e.Category == "" {
		e.Category = "自定义"
	}
	e.ID = makeEntryID(e)
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := append([]model.GlossaryEntry(nil), s.entries...)
	// Replace if an entry with the same id already exists, else append.
	replaced := false
	for i := range s.entries {
		if s.entries[i].ID == e.ID {
			s.entries[i] = e
			replaced = true
			break
		}
	}
	if !replaced {
		s.entries = append(s.entries, e)
	}
	if err := s.persist(); err != nil {
		s.entries = prev // keep memory in sync with the unchanged disk file
		return model.GlossaryEntry{}, err
	}
	return e, nil
}

// UpdateEntry overwrites the fields of an existing entry (matched by id).
func (s *GlossaryStore) UpdateEntry(id string, patch model.GlossaryEntry) (model.GlossaryEntry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i := range s.entries {
		if s.entries[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return model.GlossaryEntry{}, false, nil
	}
	// Build the edited entry as a value first, then re-derive its id, so a
	// collision can be resolved before touching the store.
	cur := s.entries[idx]
	cur.Source = patch.Source
	cur.Translation = patch.Translation
	cur.Aliases = patch.Aliases
	cur.Note = patch.Note
	if patch.Category != "" {
		cur.Category = patch.Category
	}
	cur.SubCategory = patch.SubCategory
	// A hand-edited entry becomes user-authored so MergeImport's "user rows always
	// survive" rule protects it from being dropped on the next re-import/remote sync.
	cur.Origin = model.OriginUser
	// Keep the id consistent with the makeEntryID invariant that import/dedup
	// relies on (source/category/subCategory may have changed).
	cur.ID = makeEntryID(cur)

	prev := append([]model.GlossaryEntry(nil), s.entries...)
	// If the recomputed id now equals a DIFFERENT existing entry, the edit made
	// this row identical (by source/category/subCategory) to another one. Per
	// makeEntryID those are "the same entry", so the edited row wins and the other
	// is dropped — mirroring AddEntry's same-id replacement — to keep ids unique
	// (frontend :key, and update/delete-by-id unambiguous). Ids are unique in the
	// store, so at most one such collision exists.
	if cur.ID != id {
		for j := range s.entries {
			if j != idx && s.entries[j].ID == cur.ID {
				s.entries = append(s.entries[:j], s.entries[j+1:]...)
				if j < idx {
					idx--
				}
				break
			}
		}
	}
	s.entries[idx] = cur
	if err := s.persist(); err != nil {
		s.entries = prev // keep memory in sync with the unchanged disk file
		return model.GlossaryEntry{}, false, err
	}
	return cur, true, nil
}

// DeleteEntry removes an entry by id.
func (s *GlossaryStore) DeleteEntry(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.entries {
		if s.entries[i].ID == id {
			prev := append([]model.GlossaryEntry(nil), s.entries...)
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			if err := s.persist(); err != nil {
				s.entries = prev // keep memory in sync with the unchanged disk file
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}

// MergeImport merges imported entries into the store. Each incoming row updates
// the existing row with the same id, or is appended if new; a local user-authored
// entry always wins over an incoming row with the same id.
//
// File imports (OriginImport) are purely ADDITIVE — nothing local is deleted. This
// guards the "after sync only one entry shows" wipe: a smaller/incomplete import
// must never drop a user's locally-built library.
//
// Remote SYNC (OriginRemote) treats the server as authoritative for the rows IT
// owns, so stale entries ARE pruned: a local OriginRemote entry absent from the
// incoming set is deleted (a term removed on the team server disappears on the
// next sync — without this, deleting upstream was meaningless). The prune is
// deliberately scoped to avoid the historical data loss:
//   - OriginUser (hand-authored) rows are never touched.
//   - OriginImport (file-imported) rows are never touched — a separate local
//     library, not this server's to delete.
//   - It is skipped entirely when the incoming set is EMPTY, so a wrong URL or a
//     momentarily-empty server can't wipe the synced library (the original
//     data-loss trigger). A non-empty payload is trusted as the user syncs
//     deliberately; the pruned count is returned so the UI can surface it.
// Appellations/grammar fully replace, but only when the import carries them.
// Returns the number of stale remote entries pruned.
func (s *GlossaryStore) MergeImport(imported []model.GlossaryEntry, appellations []model.Appellation, grammar []model.GrammarUsage, origin string) (int, error) {
	// Stamp + id the incoming entries.
	for i := range imported {
		imported[i].Origin = origin
		imported[i].ID = makeEntryID(imported[i])
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Snapshots for rollback if the persist below fails, so memory stays in sync
	// with the unchanged disk file. entries is mutated in place (prune/replace),
	// so it needs an element copy; appellations/grammar are only ever replaced
	// wholesale, so a header snapshot restores them.
	prevEntries := append([]model.GlossaryEntry(nil), s.entries...)
	prevAppel := s.appellations
	prevGrammar := s.grammar

	incoming := make(map[string]bool, len(imported))
	for i := range imported {
		incoming[imported[i].ID] = true
	}

	// Remote sync prunes stale remote-origin rows (see doc). Skipped on an empty
	// payload so an error/misconfig can't wipe the synced library.
	pruned := 0
	if origin == model.OriginRemote && len(imported) > 0 {
		kept := s.entries[:0]
		for _, e := range s.entries {
			if e.Origin == model.OriginRemote && !incoming[e.ID] {
				pruned++
				continue // removed on the server -> drop locally
			}
			kept = append(kept, e)
		}
		s.entries = kept
	}

	byID := make(map[string]int, len(s.entries))
	for i := range s.entries {
		byID[s.entries[i].ID] = i
	}
	for _, e := range imported {
		if idx, ok := byID[e.ID]; ok {
			if s.entries[idx].Origin == model.OriginUser {
				continue // local user entry wins; never clobbered by an import
			}
			s.entries[idx] = e // refresh the existing import/remote row in place
			continue
		}
		s.entries = append(s.entries, e)
		byID[e.ID] = len(s.entries) - 1
	}

	if len(appellations) > 0 {
		s.appellations = appellations
	}
	if len(grammar) > 0 {
		// Stamp ids; grammar fully replaces (no user-authored grammar). Include
		// the row index so rows sharing the same Item with a blank Index don't
		// collide (the id must be unique for frontend v-for :key / row identity).
		for i := range grammar {
			grammar[i].ID = makeID(strconv.Itoa(i)+"\x00"+grammar[i].Item+"\x00"+grammar[i].Index, grammarSheet)
		}
		s.grammar = grammar
	}
	if err := s.persist(); err != nil {
		s.entries = prevEntries
		s.appellations = prevAppel
		s.grammar = prevGrammar
		return 0, err
	}
	return pruned, nil
}

// --- appellations (人称表 lookup) ---

// AppellationSpeakers returns the distinct speakers, in first-seen order.
func (s *GlossaryStore) AppellationSpeakers() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[string]bool{}
	out := []string{}
	for _, a := range s.appellations {
		if !seen[a.Speaker] {
			seen[a.Speaker] = true
			out = append(out, a.Speaker)
		}
	}
	return out
}

// AppellationTargets returns the targets a given speaker has entries for.
func (s *GlossaryStore) AppellationTargets(speaker string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[string]bool{}
	out := []string{}
	for _, a := range s.appellations {
		if a.Speaker == speaker && !seen[a.Target] {
			seen[a.Target] = true
			out = append(out, a.Target)
		}
	}
	return out
}

// AppellationLookup returns how speaker addresses target.
func (s *GlossaryStore) AppellationLookup(speaker, target string) (model.Appellation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.appellations {
		if a.Speaker == speaker && a.Target == target {
			return a, true
		}
	}
	return model.Appellation{}, false
}

// UpsertAppellation edits (or inserts) one matrix cell and persists.
func (s *GlossaryStore) UpsertAppellation(a model.Appellation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := append([]model.Appellation(nil), s.appellations...)
	for i := range s.appellations {
		if s.appellations[i].Speaker == a.Speaker && s.appellations[i].Target == a.Target {
			s.appellations[i].JP = a.JP
			s.appellations[i].CN = a.CN
			if err := s.persist(); err != nil {
				s.appellations = prev // keep memory in sync with the unchanged disk file
				return err
			}
			return nil
		}
	}
	s.appellations = append(s.appellations, a)
	if err := s.persist(); err != nil {
		s.appellations = prev // keep memory in sync with the unchanged disk file
		return err
	}
	return nil
}

// --- grammar (语法用例) ---

// Grammar returns a page of grammar usages (no filter). limit<=0 means all.
func (s *GlossaryStore) Grammar(offset, limit int) ([]model.GrammarUsage, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := len(s.grammar)
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := total
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return s.grammar[offset:end], total
}

// SearchGrammar matches q against item, connection and example (case-insensitive
// substring). Empty q returns the first `limit` usages.
func (s *GlossaryStore) SearchGrammar(q string, limit int) []model.GrammarUsage {
	q = strings.TrimSpace(strings.ToLower(q))
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.GrammarUsage, 0, 64)
	for _, g := range s.grammar {
		if limit > 0 && len(out) >= limit {
			break
		}
		if q == "" ||
			strings.Contains(strings.ToLower(g.Item), q) ||
			strings.Contains(strings.ToLower(g.Connection), q) ||
			strings.Contains(strings.ToLower(g.Example), q) ||
			strings.Contains(strings.ToLower(g.Note), q) {
			out = append(out, g)
		}
	}
	return out
}

// Export returns the full in-memory payload (for download / backup / sync seed).
// It hands back defensive copies because the handler encodes the result outside
// the lock, where a live slice would race the store's in-place writes.
func (s *GlossaryStore) Export() model.GlossaryData {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return model.GlossaryData{
		Entries:      cloneEntries(s.entries),
		Appellations: append([]model.Appellation(nil), s.appellations...),
		Grammar:      append([]model.GrammarUsage(nil), s.grammar...),
	}
}
