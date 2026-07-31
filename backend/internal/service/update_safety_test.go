package service

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		Festivals: []FestivalEntry{{ID: 1, Cards: []int{1}}},
		Cards:     []CardEntry{{ID: 1, CharacterID: 1, CardNo: "001"}},
		MainStory: []MainStoryEntry{{Unit: "light_sound", AssetName: "main", Chapters: []EventChapter{{Title: "chapter", AssetName: "main_01"}}}},
		AreaTalks: []AreaTalkEntry{{ID: 1, TalkID: "0001", ScenarioID: "area_01", Type: "normal"}},
		Specials:  []SpecialEntry{{Title: "special", DirName: "special", FileName: "special_01"}},
	}
}

func TestCatalogStateRequiresPublishedGenerationAndFestivals(t *testing.T) {
	catalog := safetyTestCatalog("state")
	lm := &ListManager{
		Events:     catalog.Events,
		Festivals:  catalog.Festivals,
		Cards:      catalog.Cards,
		MainStory:  catalog.MainStory,
		AreaTalks:  catalog.AreaTalks,
		Specials:   catalog.Specials,
		generation: 0,
	}
	if ready, generation := lm.CatalogState(); ready || generation != 0 {
		t.Fatalf("legacy catalog state = ready %t generation %d, want false/0", ready, generation)
	}

	lm.generation = 1
	lm.Festivals = nil
	if ready, generation := lm.CatalogState(); ready || generation != 1 {
		t.Fatalf("festival-less catalog state = ready %t generation %d, want false/1", ready, generation)
	}

	lm.Festivals = catalog.Festivals
	if ready, generation := lm.CatalogState(); !ready || generation != 1 {
		t.Fatalf("complete catalog state = ready %t generation %d, want true/1", ready, generation)
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

func TestPublishCatalogCharacter2DGuardRunsOutsideListManagerLock(t *testing.T) {
	char2dMu.Lock()
	previous := char2dMap
	char2dMap = map[int]cdnCharacter2D{
		1: {ID: 1, AssetName: "current", Unit: "unit"},
	}
	char2dMu.Unlock()
	t.Cleanup(func() {
		char2dMu.Lock()
		char2dMap = previous
		char2dMu.Unlock()
	})

	lm := &ListManager{}
	guardCalled := false
	lm.SetCharacter2DPublishGuard(func(func()) {
		// A mobile guard takes runtimeState here. Taking lm.mu in the test proves
		// publishCatalog released it before invoking the external guard.
		lm.mu.Lock()
		lm.mu.Unlock()
		guardCalled = true
	})

	done := make(chan struct{})
	go func() {
		lm.publishCatalog(safetyTestCatalog("guarded"), 1, map[int]cdnCharacter2D{
			1: {ID: 1, AssetName: "stale", Unit: "unit"},
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("character2d publish guard was called while ListManager.mu was held")
	}
	if !guardCalled {
		t.Fatal("character2d publish guard was not called")
	}
	if got, ok := Character2dByID(1); !ok || got.AssetName != "current" {
		t.Fatalf("suppressed character2d publication changed lookup: %+v, ok=%t", got, ok)
	}
	if ready, generation := lm.CatalogState(); !ready || generation != 1 {
		t.Fatalf("guard suppressed ListManager catalog publication: ready=%t generation=%d", ready, generation)
	}
}

func TestPublishCatalogPublishesCharacter2DByDefault(t *testing.T) {
	char2dMu.Lock()
	previous := char2dMap
	char2dMap = map[int]cdnCharacter2D{
		1: {ID: 1, AssetName: "previous", Unit: "unit"},
	}
	char2dMu.Unlock()
	t.Cleanup(func() {
		char2dMu.Lock()
		char2dMap = previous
		char2dMu.Unlock()
	})

	lm := &ListManager{}
	lm.publishCatalog(safetyTestCatalog("desktop"), 1, map[int]cdnCharacter2D{
		1: {ID: 1, AssetName: "desktop", Unit: "unit"},
	})
	if got, ok := Character2dByID(1); !ok || got.AssetName != "desktop" {
		t.Fatalf("default desktop publication did not replace character2d lookup: %+v, ok=%t", got, ok)
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
	requests := 0
	oldClient := httpClient
	httpClient = &http.Client{Transport: updateRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		body := "<html>edge error</html>"
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Header:        make(http.Header),
			Body:          io.NopCloser(strings.NewReader(body)),
			ContentLength: int64(len(body)),
			Request:       req,
		}, nil
	})}
	defer func() { httpClient = oldClient }()

	url := harukiNeoMasterOrigin + "/haruki-sekai-master/master/events.json"
	if _, _, err := fetchURL(url); err == nil {
		t.Fatal("fetchURL accepted a non-JSON network body")
	}
	if requests != 1 {
		t.Fatalf("network requests = %d, want 1", requests)
	}
}

type updateRoundTripFunc func(*http.Request) (*http.Response, error)

func (f updateRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func updateHTTPResponse(req *http.Request, status int, location, body, etag string) *http.Response {
	header := make(http.Header)
	if location != "" {
		header.Set("Location", location)
	}
	if etag != "" {
		header.Set("ETag", etag)
	}
	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Header:        header,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

func TestMasterCatalogRequestsWithoutRedirectRemainUnchanged(t *testing.T) {
	var requests []string
	oldClient := httpClient
	httpClient = &http.Client{Transport: updateRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.Method+" "+req.URL.String())
		if req.Method == http.MethodHead {
			return updateHTTPResponse(req, http.StatusOK, "", "", `W/"head-v1-gzip"`), nil
		}
		return updateHTTPResponse(req, http.StatusOK, "", `[{"id":1}]`, `"get-v1"`), nil
	})}
	defer func() { httpClient = oldClient }()

	getURL := harukiNeoMasterOrigin + "/haruki-sekai-master/master/events.json"
	data, etag, err := fetchURL(getURL)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `[{"id":1}]` || etag != `"get-v1"` {
		t.Fatalf("GET result data=%q etag=%q", data, etag)
	}

	headURL := harukiMirrorMasterOrigin + "/haruki-sekai-master/master/unitStories.json"
	if etag := headETag(headURL, time.Second); etag != "head-v1" {
		t.Fatalf("HEAD ETag = %q, want head-v1", etag)
	}
	want := []string{http.MethodGet + " " + getURL, http.MethodHead + " " + headURL}
	if len(requests) != len(want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
	for i := range want {
		if requests[i] != want[i] {
			t.Fatalf("requests = %v, want %v", requests, want)
		}
	}
}

func TestMasterCatalogFetchRedirectsStayHTTPSAndSameOriginOnEveryHop(t *testing.T) {
	originStart := harukiNeoMasterOrigin + "/start"
	mirrorStart := harukiMirrorMasterOrigin + "/start"
	tests := []struct {
		name         string
		start        string
		redirects    map[string]string
		wantRequests []string
		wantErr      bool
	}{
		{
			name:         "source same-origin HTTPS",
			start:        originStart,
			redirects:    map[string]string{"/start": "/final"},
			wantRequests: []string{originStart, harukiNeoMasterOrigin + "/final"},
		},
		{
			name:         "mirror same-origin HTTPS",
			start:        mirrorStart,
			redirects:    map[string]string{"/start": harukiMirrorMasterOrigin + "/final"},
			wantRequests: []string{mirrorStart, harukiMirrorMasterOrigin + "/final"},
		},
		{
			name:  "HTTP downgrade after safe hop",
			start: originStart,
			redirects: map[string]string{
				"/start": "/safe",
				"/safe":  "http://sekai-master-direct.haruki.seiunx.com/final",
			},
			wantRequests: []string{originStart, harukiNeoMasterOrigin + "/safe"},
			wantErr:      true,
		},
		{
			name:         "source to mirror",
			start:        originStart,
			redirects:    map[string]string{"/start": harukiMirrorMasterOrigin + "/final"},
			wantRequests: []string{originStart},
			wantErr:      true,
		},
		{
			name:         "localhost",
			start:        originStart,
			redirects:    map[string]string{"/start": "https://localhost/final"},
			wantRequests: []string{originStart},
			wantErr:      true,
		},
		{
			name:         "private address",
			start:        originStart,
			redirects:    map[string]string{"/start": "https://10.0.0.1/final"},
			wantRequests: []string{originStart},
			wantErr:      true,
		},
		{
			name:         "other host",
			start:        originStart,
			redirects:    map[string]string{"/start": "https://example.com/final"},
			wantRequests: []string{originStart},
			wantErr:      true,
		},
		{
			name:         "same host different port",
			start:        originStart,
			redirects:    map[string]string{"/start": "https://sekai-master-direct.haruki.seiunx.com:8443/final"},
			wantRequests: []string{originStart},
			wantErr:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var requests []string
			oldClient := httpClient
			httpClient = &http.Client{Transport: updateRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests = append(requests, req.URL.String())
				if location, ok := tc.redirects[req.URL.Path]; ok {
					return updateHTTPResponse(req, http.StatusFound, location, "", ""), nil
				}
				return updateHTTPResponse(req, http.StatusOK, "", `[]`, `"v1"`), nil
			})}
			defer func() { httpClient = oldClient }()

			_, _, err := fetchURL(tc.start)
			if tc.wantErr && err == nil {
				t.Fatal("unsafe redirect was followed")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("safe redirect failed: %v", err)
			}
			if len(requests) != len(tc.wantRequests) {
				t.Fatalf("requests = %v, want %v", requests, tc.wantRequests)
			}
			for i := range tc.wantRequests {
				if requests[i] != tc.wantRequests[i] {
					t.Fatalf("requests = %v, want %v", requests, tc.wantRequests)
				}
			}
		})
	}
}

func TestMasterCatalogHeadProbeRejectsUnsafeRedirect(t *testing.T) {
	requests := 0
	oldClient := httpClient
	httpClient = &http.Client{Transport: updateRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if requests > 1 {
			t.Fatalf("unsafe HEAD redirect reached transport: %s", req.URL)
		}
		return updateHTTPResponse(req, http.StatusFound, "https://127.0.0.1/private", "", ""), nil
	})}
	defer func() { httpClient = oldClient }()

	url := harukiNeoMasterOrigin + "/haruki-sekai-master/master/events.json"
	if etag := headETag(url, time.Second); etag != "" {
		t.Fatalf("HEAD ETag = %q, want empty after rejected redirect", etag)
	}
	if requests != 1 {
		t.Fatalf("network requests = %d, want 1", requests)
	}
}

func TestMasterCatalogRejectsUntrustedInitialURLWithoutNetwork(t *testing.T) {
	requests := 0
	oldClient := httpClient
	httpClient = &http.Client{Transport: updateRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return updateHTTPResponse(req, http.StatusOK, "", `[]`, ""), nil
	})}
	defer func() { httpClient = oldClient }()

	urls := []string{
		"http://sekai-master-direct.haruki.seiunx.com/events.json",
		"https://localhost/events.json",
		"https://192.168.1.10/events.json",
		"https://example.com/events.json",
	}
	for _, url := range urls {
		if _, _, err := fetchURL(url); err == nil {
			t.Fatalf("fetchURL accepted untrusted initial URL %q", url)
		}
	}
	if requests != 0 {
		t.Fatalf("untrusted initial URLs made %d network requests", requests)
	}
}

func TestUpdateAllFromCDNLowMemoryUsesUpdateMutex(t *testing.T) {
	requested := make(chan struct{}, 1)
	oldClient := httpClient
	httpClient = &http.Client{Transport: updateRoundTripFunc(func(*http.Request) (*http.Response, error) {
		select {
		case requested <- struct{}{}:
		default:
		}
		return nil, errors.New("offline")
	})}
	defer func() { httpClient = oldClient }()

	lm := &ListManager{}
	progress := NewProgressTracker()
	dir := t.TempDir()
	lm.updateMu.Lock()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- lm.UpdateAllFromCDNLowMemory(dir, progress)
	}()
	<-started

	select {
	case <-requested:
		lm.updateMu.Unlock()
		t.Fatal("low-memory update issued a request while updateMu was held")
	case err := <-done:
		lm.updateMu.Unlock()
		t.Fatalf("low-memory update returned while updateMu was held: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if current, total, _, _ := progress.Status(); current != 0 || total != 0 {
		lm.updateMu.Unlock()
		t.Fatalf("progress advanced before updateMu acquisition: current=%d total=%d", current, total)
	}

	lm.updateMu.Unlock()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("offline low-memory update unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("low-memory update remained blocked after updateMu was released")
	}
}
