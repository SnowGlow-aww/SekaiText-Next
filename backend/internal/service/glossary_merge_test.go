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
	if err := s.MergeImport([]model.GlossaryEntry{
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
	if err := s.MergeImport([]model.GlossaryEntry{
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
