package mobilecore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"sekaitext/backend/internal/model"
)

func decodeGlossaryTestJSON[T any](t *testing.T, raw string) T {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("decode response %q: %v", raw, err)
	}
	return value
}

func encodeGlossaryTestJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	return string(encoded)
}

func writeGlossarySeed(t *testing.T, dataDir string, data model.GlossaryData) {
	t.Helper()
	dir := filepath.Join(dataDir, "resources", "glossary")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "glossary.json"), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

func findGlossaryTestEntry(entries []model.GlossaryEntry, source string) (model.GlossaryEntry, bool) {
	for _, entry := range entries {
		if entry.Source == source {
			return entry, true
		}
	}
	return model.GlossaryEntry{}, false
}

func TestGlossaryMobileCRUDAndRestartPersistence(t *testing.T) {
	dataDir := t.TempDir()
	writeGlossarySeed(t, dataDir, model.GlossaryData{
		Entries: []model.GlossaryEntry{{
			Source:      "世界计划",
			Translation: "世界计划",
			Aliases:     []string{"プロセカ"},
			Category:    "作品",
			Origin:      model.OriginImport,
		}},
		Appellations: []model.Appellation{{
			Speaker: "初音ミク",
			Target:  "镜音铃",
			JP:      "リン",
			CN:      "铃",
		}},
		Grammar: []model.GrammarUsage{{
			ID:         "grammar-seed",
			Item:       "〜ながら",
			Connection: "动词ます形",
			Example:    "歌いながら踊る",
			Reference:  "一边唱歌一边跳舞",
		}},
	})

	if err := InitializeGlossary(dataDir); err != nil {
		t.Fatal(err)
	}
	mobileGlossaryState.mu.RLock()
	initialStore := mobileGlossaryState.store
	mobileGlossaryState.mu.RUnlock()
	mobileTeamBindingState.mu.RLock()
	initialGeneration := mobileTeamBindingState.generation
	mobileTeamBindingState.mu.RUnlock()
	if err := InitializeGlossary(dataDir); err != nil {
		t.Fatal(err)
	}
	mobileGlossaryState.mu.RLock()
	repeatedStore := mobileGlossaryState.store
	mobileGlossaryState.mu.RUnlock()
	mobileTeamBindingState.mu.RLock()
	repeatedGeneration := mobileTeamBindingState.generation
	mobileTeamBindingState.mu.RUnlock()
	if repeatedStore != initialStore || repeatedGeneration != initialGeneration {
		t.Fatal("repeated same-root InitializeGlossary replaced the active store")
	}

	searchedJSON, err := GlossarySearch(`{"q":"プロセカ"}`)
	if err != nil {
		t.Fatal(err)
	}
	searched := decodeGlossaryTestJSON[[]model.GlossaryEntry](t, searchedJSON)
	if len(searched) != 1 || searched[0].Source != "世界计划" {
		t.Fatalf("unexpected search response: %s", searchedJSON)
	}

	categoriesJSON, err := GlossaryCategories()
	if err != nil {
		t.Fatal(err)
	}
	categories := decodeGlossaryTestJSON[[]struct {
		Category string `json:"category"`
		Count    int    `json:"count"`
	}](t, categoriesJSON)
	if len(categories) != 1 || categories[0].Category != "作品" || categories[0].Count != 1 {
		t.Fatalf("unexpected categories response: %s", categoriesJSON)
	}

	entriesJSON, err := GlossaryEntries(`{"category":"作品"}`)
	if err != nil {
		t.Fatal(err)
	}
	entriesPage := decodeGlossaryTestJSON[glossaryEntriesResponse](t, entriesJSON)
	if entriesPage.Total != 1 || len(entriesPage.Items) != 1 || entriesPage.Items[0].Source != "世界计划" {
		t.Fatalf("unexpected entries response: %s", entriesJSON)
	}

	addedJSON, err := GlossaryAddEntry(`{
		"source":"セカイ",
		"translation":"世界",
		"aliases":["SEKAI"],
		"note":"mobile add",
		"category":"专有名词表"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	added := decodeGlossaryTestJSON[model.GlossaryEntry](t, addedJSON)
	if added.ID == "" || added.Origin != model.OriginUser {
		t.Fatalf("store did not assign mobile entry identity: %s", addedJSON)
	}

	updateRequest := glossaryUpdateEntryRequest{
		ID: added.ID,
		Entry: model.GlossaryEntry{
			Source:      "セカイ",
			Translation: "世界（更新）",
			Aliases:     []string{"SEKAI", "世界"},
			Note:        "mobile update",
			Category:    "专有名词表",
		},
	}
	updatedJSON, err := GlossaryUpdateEntry(encodeGlossaryTestJSON(t, updateRequest))
	if err != nil {
		t.Fatal(err)
	}
	updated := decodeGlossaryTestJSON[model.GlossaryEntry](t, updatedJSON)
	if updated.ID != added.ID || updated.Translation != "世界（更新）" || updated.Origin != model.OriginUser {
		t.Fatalf("unexpected updated entry: %s", updatedJSON)
	}

	deleteCandidateJSON, err := GlossaryAddEntry(`{
		"source":"削除候補",
		"translation":"待删除",
		"category":"临时"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	deleteCandidate := decodeGlossaryTestJSON[model.GlossaryEntry](t, deleteCandidateJSON)
	deletedJSON, err := GlossaryDeleteEntry(encodeGlossaryTestJSON(t, glossaryEntryIDRequest{ID: deleteCandidate.ID}))
	if err != nil {
		t.Fatal(err)
	}
	deleted := decodeGlossaryTestJSON[map[string]string](t, deletedJSON)
	if deleted["status"] != "deleted" {
		t.Fatalf("unexpected delete response: %s", deletedJSON)
	}

	speakersJSON, err := GlossaryAppellationSpeakers()
	if err != nil {
		t.Fatal(err)
	}
	speakers := decodeGlossaryTestJSON[[]string](t, speakersJSON)
	if len(speakers) != 1 || speakers[0] != "初音ミク" {
		t.Fatalf("unexpected speakers response: %s", speakersJSON)
	}

	targetsJSON, err := GlossaryAppellationTargets(`{"speaker":"初音ミク"}`)
	if err != nil {
		t.Fatal(err)
	}
	targets := decodeGlossaryTestJSON[[]string](t, targetsJSON)
	if len(targets) != 1 || targets[0] != "镜音铃" {
		t.Fatalf("unexpected targets response: %s", targetsJSON)
	}

	lookupJSON, err := GlossaryAppellationLookup(`{"speaker":"初音ミク","target":"镜音铃"}`)
	if err != nil {
		t.Fatal(err)
	}
	lookup := decodeGlossaryTestJSON[glossaryAppellationLookupResponse](t, lookupJSON)
	if !lookup.Found || lookup.JP != "リン" || lookup.CN != "铃" {
		t.Fatalf("unexpected appellation lookup: %s", lookupJSON)
	}

	missingLookupJSON, err := GlossaryAppellationLookup(`{"speaker":"初音ミク","target":"不存在"}`)
	if err != nil {
		t.Fatal(err)
	}
	missingLookup := decodeGlossaryTestJSON[glossaryAppellationLookupResponse](t, missingLookupJSON)
	if missingLookup.Found {
		t.Fatalf("missing appellation reported as found: %s", missingLookupJSON)
	}

	upsertJSON, err := GlossaryAppellationUpsert(`{
		"speaker":"初音ミク",
		"target":"巡音流歌",
		"jp":"ルカ",
		"cn":"流歌"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	upserted := decodeGlossaryTestJSON[model.Appellation](t, upsertJSON)
	if upserted.Target != "巡音流歌" || upserted.CN != "流歌" {
		t.Fatalf("unexpected appellation upsert: %s", upsertJSON)
	}

	grammarJSON, err := GlossaryGrammar(`{"q":"歌い","limit":10}`)
	if err != nil {
		t.Fatal(err)
	}
	grammar := decodeGlossaryTestJSON[[]model.GrammarUsage](t, grammarJSON)
	if len(grammar) != 1 || grammar[0].Item != "〜ながら" {
		t.Fatalf("unexpected grammar response: %s", grammarJSON)
	}

	exportJSON, err := GlossaryExport()
	if err != nil {
		t.Fatal(err)
	}
	exported := decodeGlossaryTestJSON[model.GlossaryData](t, exportJSON)
	if len(exported.Entries) != 2 || len(exported.Appellations) != 2 || len(exported.Grammar) != 1 {
		t.Fatalf("unexpected export before restart: %s", exportJSON)
	}

	// Recreate the façade/store against the same Android-private data directory.
	// Reset the package binding to simulate a fresh process; repeated calls within
	// one Activity lifecycle are intentionally idempotent.
	resetMobileGlossaryTestState()
	// The updated entry and appellation must survive, while the deleted entry must
	// remain absent.
	if err := InitializeGlossary(dataDir); err != nil {
		t.Fatal(err)
	}

	restartedExportJSON, err := GlossaryExport()
	if err != nil {
		t.Fatal(err)
	}
	restarted := decodeGlossaryTestJSON[model.GlossaryData](t, restartedExportJSON)
	persistedEntry, found := findGlossaryTestEntry(restarted.Entries, "セカイ")
	if !found || persistedEntry.Translation != "世界（更新）" || persistedEntry.Note != "mobile update" {
		t.Fatalf("updated entry did not survive restart: %s", restartedExportJSON)
	}
	if _, found := findGlossaryTestEntry(restarted.Entries, "削除候補"); found {
		t.Fatalf("deleted entry reappeared after restart: %s", restartedExportJSON)
	}
	if len(restarted.Grammar) != 1 || restarted.Grammar[0].Item != "〜ながら" {
		t.Fatalf("grammar did not survive restart: %s", restartedExportJSON)
	}

	restartedLookupJSON, err := GlossaryAppellationLookup(`{"speaker":"初音ミク","target":"巡音流歌"}`)
	if err != nil {
		t.Fatal(err)
	}
	restartedLookup := decodeGlossaryTestJSON[glossaryAppellationLookupResponse](t, restartedLookupJSON)
	if !restartedLookup.Found || restartedLookup.JP != "ルカ" || restartedLookup.CN != "流歌" {
		t.Fatalf("appellation did not survive restart: %s", restartedLookupJSON)
	}

	if _, err := GlossaryDeleteEntry(encodeGlossaryTestJSON(t, glossaryEntryIDRequest{ID: deleteCandidate.ID})); err == nil || !strings.Contains(err.Error(), "entry not found") {
		t.Fatalf("deleted entry should remain absent after restart, got: %v", err)
	}
}

func TestGlossaryMobileErrorsAreClear(t *testing.T) {
	if err := InitializeGlossary(" \t "); err == nil || !strings.Contains(err.Error(), "data directory is required") {
		t.Fatalf("unexpected empty-directory error: %v", err)
	}

	corruptDir := t.TempDir()
	writeGlossarySeed(t, corruptDir, model.GlossaryData{})
	persistedPath := filepath.Join(corruptDir, "resources", "glossary", "glossary.json")
	if err := os.WriteFile(persistedPath, []byte(`{"entries":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InitializeGlossary(corruptDir); err == nil || !strings.Contains(err.Error(), "decode persisted data") {
		t.Fatalf("unexpected corrupt-data error: %v", err)
	}

	if err := InitializeGlossary(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := GlossarySearch(`{"q":`); err == nil || !strings.Contains(err.Error(), "decode glossary search request") {
		t.Fatalf("unexpected malformed-search error: %v", err)
	}
	if _, err := GlossaryAddEntry(`{"translation":"missing source"}`); err == nil || !strings.Contains(err.Error(), "source is required") {
		t.Fatalf("unexpected add validation error: %v", err)
	}
	if _, err := GlossaryUpdateEntry(`{"id":"missing","entry":{"source":"term"}}`); err == nil || !strings.Contains(err.Error(), "entry not found") {
		t.Fatalf("unexpected missing-update error: %v", err)
	}
	if _, err := GlossaryAppellationTargets(`{"speaker":""}`); err == nil || !strings.Contains(err.Error(), "speaker is required") {
		t.Fatalf("unexpected targets validation error: %v", err)
	}
	if _, err := GlossaryAppellationUpsert(`{"speaker":"初音ミク"}`); err == nil || !strings.Contains(err.Error(), "speaker and target are required") {
		t.Fatalf("unexpected appellation validation error: %v", err)
	}
}

func TestGlossaryMobileExportedFunctionsUseStringJSONBoundary(t *testing.T) {
	functions := map[string]any{
		"InitializeGlossary":          InitializeGlossary,
		"GlossarySearch":              GlossarySearch,
		"GlossaryCategories":          GlossaryCategories,
		"GlossaryEntries":             GlossaryEntries,
		"GlossaryAddEntry":            GlossaryAddEntry,
		"GlossaryUpdateEntry":         GlossaryUpdateEntry,
		"GlossaryDeleteEntry":         GlossaryDeleteEntry,
		"GlossaryAppellationSpeakers": GlossaryAppellationSpeakers,
		"GlossaryAppellationTargets":  GlossaryAppellationTargets,
		"GlossaryAppellationLookup":   GlossaryAppellationLookup,
		"GlossaryAppellationUpsert":   GlossaryAppellationUpsert,
		"GlossaryGrammar":             GlossaryGrammar,
		"GlossaryExport":              GlossaryExport,
	}
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	for name, function := range functions {
		functionType := reflect.TypeOf(function)
		for i := 0; i < functionType.NumIn(); i++ {
			if functionType.In(i).Kind() != reflect.String {
				t.Fatalf("%s input %d is %s; gomobile boundary must use strings", name, i, functionType.In(i))
			}
		}
		for i := 0; i < functionType.NumOut(); i++ {
			output := functionType.Out(i)
			if output.Kind() != reflect.String && output != errorType {
				t.Fatalf("%s output %d is %s; gomobile boundary must use string/error", name, i, output)
			}
		}
	}
}
