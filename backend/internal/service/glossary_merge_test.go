package service

import (
	"testing"

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

	// Sync a SMALL/incomplete remote set (mirrors a nearly-empty shared server):
	// only one category, one term, with a changed translation.
	if _, err := s.MergeImport([]model.GlossaryEntry{
		{Source: "アキ", Translation: "AKI", Category: "人名"},
	}, nil, nil, model.OriginRemote); err != nil {
		t.Fatal(err)
	}

	// Nothing may be deleted — all four entries must survive the sync.
	if n := len(s.entries); n != 4 {
		t.Fatalf("sync deleted local entries: want 4, got %d", n)
	}
	if e, ok := findBySource(s.entries, "アキ"); !ok || e.Translation != "AKI" {
		t.Errorf("remote update not applied in place: %+v ok=%v", e, ok)
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

// TestSyncPrunesStaleRemoteEntries locks in deletion propagation: a remote sync
// removes remote-origin rows the server no longer has, but never touches
// user-authored or file-imported rows, and never prunes on an empty payload.
func TestSyncPrunesStaleRemoteEntries(t *testing.T) {
	s := NewGlossaryStore(t.TempDir())

	// First sync brings two remote terms.
	if _, err := s.MergeImport([]model.GlossaryEntry{
		{Source: "アキ", Translation: "秋", Category: "人名"},
		{Source: "ミク", Translation: "未来", Category: "人名"},
	}, nil, nil, model.OriginRemote); err != nil {
		t.Fatal(err)
	}
	// A hand-authored entry + a file-imported entry the server never has.
	if _, err := s.AddEntry(model.GlossaryEntry{Source: "わたし", Translation: "我"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeImport([]model.GlossaryEntry{
		{Source: "東京", Translation: "东京", Category: "地名"},
	}, nil, nil, model.OriginImport); err != nil {
		t.Fatal(err)
	}

	// Second sync drops ミク on the server.
	removed, err := s.MergeImport([]model.GlossaryEntry{
		{Source: "アキ", Translation: "秋", Category: "人名"},
	}, nil, nil, model.OriginRemote)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("want 1 pruned remote entry, got %d", removed)
	}
	if _, ok := findBySource(s.entries, "ミク"); ok {
		t.Error("stale remote entry ミク was not pruned after server deletion")
	}
	if _, ok := findBySource(s.entries, "アキ"); !ok {
		t.Error("remote entry still on server (アキ) was wrongly pruned")
	}
	if _, ok := findBySource(s.entries, "わたし"); !ok {
		t.Error("user-authored entry わたし was pruned by sync")
	}
	if _, ok := findBySource(s.entries, "東京"); !ok {
		t.Error("file-imported entry 東京 was pruned by remote sync")
	}

	// An EMPTY remote payload must never prune (guards the historical wipe).
	before := len(s.entries)
	removed, err = s.MergeImport(nil, nil, nil, model.OriginRemote)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 || len(s.entries) != before {
		t.Fatalf("empty payload pruned entries: removed=%d, %d -> %d", removed, before, len(s.entries))
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
