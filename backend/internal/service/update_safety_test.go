package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildFestivalsClassifiesEventCardsBySet(t *testing.T) {
	cards := make([]CardEntry, 8)
	for i := range cards {
		cards[i] = CardEntry{ID: i + 1, CharacterID: 1, CardNo: "001"}
	}
	events := []EventEntry{
		{ID: 1, Cards: []int{5, 2}},
		{ID: 2, Cards: []int{7}},
	}
	festivals, err := buildFestivals(events, cards)
	if err != nil {
		t.Fatal(err)
	}
	eventCards := map[int]bool{2: true, 5: true, 7: true}
	for _, festival := range festivals {
		for _, cardID := range festival.Cards {
			if eventCards[cardID] {
				t.Fatalf("event card %d was classified as festival: %+v", cardID, festivals)
			}
		}
	}
}

func safetyTestCatalog(label string) *catalogData {
	return &catalogData{
		Events: []EventEntry{{
			ID: 1, KdyicrID: 1, Title: label, Name: "event",
			Chapters: []EventChapter{{Title: "chapter", AssetName: "event_1_01"}}, Cards: []int{1},
		}},
		Cards:     []CardEntry{{ID: 1, CharacterID: 1, CardNo: "001"}},
		MainStory: []MainStoryEntry{{Unit: "light_sound", AssetName: "main", Chapters: []EventChapter{{Title: "chapter", AssetName: "main_01"}}}},
		AreaTalks: []AreaTalkEntry{{ID: 1, TalkID: "0001", ScenarioID: "area_01", Type: "normal"}},
		Specials:  []SpecialEntry{{Title: "special", DirName: "special", FileName: "special_01"}},
	}
}

func TestLoadCatalogGenerationRecoversRetainedPrevious(t *testing.T) {
	dir := t.TempDir()
	first, err := persistCatalogGeneration(dir, 1, safetyTestCatalog("generation one"))
	if err != nil {
		t.Fatal(err)
	}
	current, err := persistCatalogGeneration(dir, 2, safetyTestCatalog("generation two"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, catalogGenerationDir, current.Dir, "events.json"), []byte("truncated"), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog, generation, err := loadCatalogGeneration(dir)
	if err != nil {
		t.Fatal(err)
	}
	if generation != 1 || catalog.Events[0].Title != "generation one" {
		t.Fatalf("recovered generation=%d catalog=%+v", generation, catalog.Events)
	}
	manifest, err := readCatalogManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Dir != first.Dir || manifest.Generation != 1 {
		t.Fatalf("manifest was not repaired: %+v", manifest)
	}
}

func TestPublishCatalogRefreshesVoiceAndFlashbackIndexes(t *testing.T) {
	lm := &ListManager{}
	fb := NewFlashbackAnalyzer(lm)
	catalog := safetyTestCatalog("fresh event")
	catalog.Events[0].ID = 42
	catalog.Events[0].Chapters[0] = EventChapter{Title: "fresh chapter", AssetName: "fresh_01"}
	catalog.AreaTalks = []AreaTalkEntry{{
		ID: 1, ScenarioID: "areatalk_ev_fresh_01", AddEventID: 42,
	}}
	catalog.MainStory = []MainStoryEntry{{
		Unit: "school_refusal", Chapters: []EventChapter{{Title: "fresh main", AssetName: "main_01"}},
	}}

	lm.publishCatalog(catalog, 1, nil)
	clues := lm.BuildVoiceIDClues()
	if got := clues["fresh"].Title; got != "fresh event" {
		t.Fatalf("voice clues still reference old generation: %q", got)
	}
	eventHints := fb.GetClueHints("ev_fresh_1", "zh-cn")
	if !containsString(eventHints, "fresh event") || !containsString(eventHints, "fresh chapter") {
		t.Fatalf("event flashback index was not refreshed: %v", eventHints)
	}
	mainHints := fb.GetClueHints("ms_night_1", "zh-cn")
	if !containsString(mainHints, "fresh main") {
		t.Fatalf("main-story flashback index was not refreshed: %v", mainHints)
	}
}

func TestCatalogManifestPostCommitErrorRetainsReferencedGeneration(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, catalogGenerationDir)
	finalDir := filepath.Join(root, "generation-00000000000000000003-1")
	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := catalogManifest{Version: 1, Generation: 3, Dir: filepath.Base(finalDir)}
	wantErr := errors.New("post-rename directory sync failed")
	err := commitCatalogManifest(dir, root, finalDir, manifest, func(dir string, manifest catalogManifest) error {
		data, marshalErr := json.Marshal(manifest)
		if marshalErr != nil {
			return marshalErr
		}
		if writeErr := os.WriteFile(filepath.Join(dir, catalogManifestFile), data, 0o644); writeErr != nil {
			return writeErr
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if _, statErr := os.Stat(finalDir); statErr != nil {
		t.Fatalf("manifest-referenced generation was deleted: %v", statErr)
	}
	got, readErr := readCatalogManifest(dir)
	if readErr != nil || got != manifest {
		t.Fatalf("committed manifest = %+v, %v", got, readErr)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestFetchURLRejectsNonJSONBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>edge error</html>"))
	}))
	defer server.Close()
	oldClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = oldClient }()
	if _, _, err := fetchURL(server.URL); err == nil {
		t.Fatal("fetchURL accepted a non-JSON network body")
	}
}
