package service

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"sekaitext/backend/internal/fsutil"
	"sekaitext/backend/internal/model"
)

func findBySource(es []model.GlossaryEntry, src string) (model.GlossaryEntry, bool) {
	for _, e := range es {
		if e.Source == src {
			return e, true
		}
	}
	return model.GlossaryEntry{}, false
}

// TestMergeImportIsAdditive guards the fix for the "after sync only one entry
// shows" bug: syncing an incomplete remote set must never delete local rows.
func TestMergeImportIsAdditive(t *testing.T) {
	s := NewGlossaryStore(t.TempDir())

	// A locally-imported library spanning several categories.
	if _, err := s.MergeImport([]model.GlossaryEntry{
		{Source: "アキ", Translation: "秋", Category: "人名"},
		{Source: "ミク", Translation: "未来", Category: "人名"},
		{Source: "東京", Translation: "东京", Category: "地名"},
	}, nil, nil, model.OriginImport); err != nil {
		t.Fatal(err)
	}
	// Plus a user-authored entry.
	if _, err := s.AddEntry(model.GlossaryEntry{Source: "わたし", Translation: "我", Category: "自定义"}); err != nil {
		t.Fatal(err)
	}
	if n := len(s.entries); n != 4 {
		t.Fatalf("setup: want 4 entries, got %d", n)
	}

	// Sync a SMALL remote set containing a term that collides with a locally
	// imported row. The local row remains outside the server's authority.
	if _, err := s.MergeImport([]model.GlossaryEntry{
		{Source: "アキ", Translation: "AKI", Category: "人名"},
	}, nil, nil, model.OriginRemote); err != nil {
		t.Fatal(err)
	}

	// Nothing may be deleted — all four entries must survive the sync.
	if n := len(s.entries); n != 4 {
		t.Fatalf("sync deleted local entries: want 4, got %d", n)
	}
	if e, ok := findBySource(s.entries, "アキ"); !ok || e.Translation != "秋" || e.Origin != model.OriginImport {
		t.Errorf("remote snapshot clobbered locally imported row: %+v ok=%v", e, ok)
	}
	if e, ok := findBySource(s.entries, "わたし"); !ok || e.Translation != "我" || e.Origin != model.OriginUser {
		t.Errorf("user entry not preserved: %+v ok=%v", e, ok)
	}
	if _, ok := findBySource(s.entries, "東京"); !ok {
		t.Error("地名 entry 東京 was wiped by sync (other categories must survive)")
	}
	if _, ok := findBySource(s.entries, "ミク"); !ok {
		t.Error("人名 entry ミク was wiped by sync (untouched terms must survive)")
	}
}

// TestSyncPrunesStaleRemoteSnapshot locks in deletion propagation: a remote
// full snapshot removes remote-owned rows and can clear all three sections while
// preserving user-authored and file-imported entries.
func TestSyncPrunesStaleRemoteSnapshot(t *testing.T) {
	s := NewGlossaryStore(t.TempDir())

	// First sync brings two remote terms and both auxiliary sections.
	remoteAppellations := []model.Appellation{{Speaker: "ミク", Target: "リン", JP: "リン"}}
	remoteGrammar := []model.GrammarUsage{{Item: "〜ながら", Example: "歌いながら踊る"}}
	if _, err := s.MergeImport([]model.GlossaryEntry{
		{Source: "アキ", Translation: "秋", Category: "人名"},
		{Source: "ミク", Translation: "未来", Category: "人名"},
	}, remoteAppellations, remoteGrammar, model.OriginRemote); err != nil {
		t.Fatal(err)
	}
	// A hand-authored entry + a file-imported entry the server never has.
	if _, err := s.AddEntry(model.GlossaryEntry{Source: "わたし", Translation: "我"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeImport([]model.GlossaryEntry{
		{Source: "東京", Translation: "东京", Category: "地名"},
		{Source: "アキ", Translation: "本地秋", Category: "人名"},
	}, nil, nil, model.OriginImport); err != nil {
		t.Fatal(err)
	}

	// Second sync drops ミク on the server. The colliding file-imported アキ
	// remains locally owned rather than being converted into a remote row.
	removed, err := s.MergeImport([]model.GlossaryEntry{
		{Source: "アキ", Translation: "服务器秋", Category: "人名"},
		{Source: "ルカ", Translation: "流歌", Category: "人名"},
	}, remoteAppellations, remoteGrammar, model.OriginRemote)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("want 1 pruned remote entry, got %d", removed)
	}
	if _, ok := findBySource(s.entries, "ミク"); ok {
		t.Error("stale remote entry ミク was not pruned after server deletion")
	}
	if entry, ok := findBySource(s.entries, "アキ"); !ok || entry.Origin != model.OriginImport || entry.Translation != "本地秋" {
		t.Errorf("local import colliding with remote snapshot was not preserved: %+v ok=%v", entry, ok)
	}
	if _, ok := findBySource(s.entries, "わたし"); !ok {
		t.Error("user-authored entry わたし was pruned by sync")
	}
	if _, ok := findBySource(s.entries, "東京"); !ok {
		t.Error("file-imported entry 東京 was pruned by remote sync")
	}

	// An EMPTY remote full snapshot clears all remaining remote-owned data while
	// retaining every local entry.
	removed, err = s.MergeImport(nil, nil, nil, model.OriginRemote)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 { // ルカ is the sole remaining remote-owned row.
		t.Fatalf("empty snapshot removed %d remote entries, want 1", removed)
	}
	if len(s.entries) != 3 {
		t.Fatalf("empty snapshot damaged local entries: %+v", s.entries)
	}
	if _, ok := findBySource(s.entries, "ルカ"); ok {
		t.Fatal("remote entry survived authoritative empty snapshot")
	}
	if len(s.appellations) != 0 || len(s.grammar) != 0 {
		t.Fatalf("authoritative empty snapshot did not clear auxiliary sections: appellations=%d grammar=%d", len(s.appellations), len(s.grammar))
	}
}

func TestRemoteSnapshotPersistenceFailureRollsBackAllSections(t *testing.T) {
	s := &GlossaryStore{path: filepath.Join(t.TempDir(), "glossary.json")}
	if _, err := s.MergeImport(
		[]model.GlossaryEntry{{Source: "旧远端", Translation: "old", Category: "remote"}},
		[]model.Appellation{{Speaker: "旧", Target: "称呼", JP: "old"}},
		[]model.GrammarUsage{{Item: "旧语法"}},
		model.OriginRemote,
	); err != nil {
		t.Fatal(err)
	}
	before := s.Export()

	// Point persistence at an existing directory so the atomic rename fails.
	s.path = t.TempDir()
	if _, err := s.MergeImport(nil, nil, nil, model.OriginRemote); err == nil {
		t.Fatal("authoritative clear unexpectedly succeeded with an unwritable target")
	}
	after := s.Export()
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("persistence failure changed in-memory snapshot:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestRemoteSnapshotPostCommitWarningKeepsMemoryAlignedWithDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "glossary.json")
	s := &GlossaryStore{path: path}
	if _, err := s.MergeImport(
		[]model.GlossaryEntry{{Source: "旧远端", Translation: "old", Category: "remote"}},
		[]model.Appellation{{Speaker: "旧", Target: "称呼", JP: "old"}},
		[]model.GrammarUsage{{Item: "旧语法"}},
		model.OriginRemote,
	); err != nil {
		t.Fatal(err)
	}

	wantWarning := errors.New("sync glossary directory")
	s.writeFileAtomic = func(path string, data []byte, mode os.FileMode) error {
		if err := fsutil.WriteFileAtomic(path, data, mode); err != nil {
			return err
		}
		return &fsutil.PostCommitError{Err: wantWarning}
	}
	removed, err := s.MergeImport(
		[]model.GlossaryEntry{{Source: "新远端", Translation: "new", Category: "remote"}},
		[]model.Appellation{{Speaker: "新", Target: "称呼", JP: "new"}},
		[]model.GrammarUsage{{Item: "新语法"}},
		model.OriginRemote,
	)
	if err != nil {
		t.Fatalf("committed durability warning was returned as a persistence failure: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	memory := s.Export()
	if _, ok := findBySource(memory.Entries, "新远端"); !ok {
		t.Fatalf("memory rolled back after committed write: %+v", memory)
	}
	if _, ok := findBySource(memory.Entries, "旧远端"); ok {
		t.Fatalf("stale memory survived committed replacement: %+v", memory)
	}

	disk := &GlossaryStore{path: path}
	if err := disk.load(); err != nil {
		t.Fatal(err)
	}
	if got := disk.Export(); !reflect.DeepEqual(got, memory) {
		t.Fatalf("memory and committed disk diverged:\nmemory=%+v\ndisk=%+v", memory, got)
	}
}

func TestWriteSyncBackupUsesUniqueNamesAndKeepsNewestTen(t *testing.T) {
	fixed := time.Date(2026, time.July, 30, 12, 34, 56, 123456789, time.Local)
	s := &GlossaryStore{
		path: filepath.Join(t.TempDir(), "glossary.json"),
		now:  func() time.Time { return fixed },
	}
	for i := 0; i < 12; i++ {
		s.WriteSyncBackup([]byte(strconv.Itoa(i)))
	}

	dir := filepath.Join(filepath.Dir(s.path), "backups")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 10 {
		t.Fatalf("backup count = %d, want 10: %+v", len(entries), entries)
	}
	for i, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("unexpected backup directory: %s", entry.Name())
		}
		got, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		want := strconv.Itoa(i + 2)
		if string(got) != want {
			t.Fatalf("backup %s content = %q, want %q", entry.Name(), got, want)
		}
	}
}

// TestMakeEntryIDDistinguishesSubCategory guards against the id collapse: entries
// differing only in subCategory must get distinct ids (previously they collided
// on (source,category) and overwrote each other / rendered as one).
func TestMakeEntryIDDistinguishesSubCategory(t *testing.T) {
	a := makeEntryID(model.GlossaryEntry{Source: "x", Category: "c", SubCategory: "s1"})
	b := makeEntryID(model.GlossaryEntry{Source: "x", Category: "c", SubCategory: "s2"})
	if a == b {
		t.Fatal("entries differing only in subCategory collapsed to one id")
	}
}
