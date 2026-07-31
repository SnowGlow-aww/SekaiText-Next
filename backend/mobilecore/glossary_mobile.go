package mobilecore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"sekaitext/backend/internal/model"
	"sekaitext/backend/internal/service"
)

var mobileGlossaryState struct {
	initializeMu sync.Mutex
	mu           sync.RWMutex
	store        *service.GlossaryStore
	root         string
}

type glossarySearchRequest struct {
	Query    string `json:"q"`
	Category string `json:"category,omitempty"`
	Limit    *int   `json:"limit,omitempty"`
}

type glossaryEntriesRequest struct {
	Category string `json:"category,omitempty"`
	Offset   int    `json:"offset,omitempty"`
	Limit    *int   `json:"limit,omitempty"`
}

type glossaryEntriesResponse struct {
	Items []model.GlossaryEntry `json:"items"`
	Total int                   `json:"total"`
}

type glossaryUpdateEntryRequest struct {
	ID    string              `json:"id"`
	Entry model.GlossaryEntry `json:"entry"`
}

type glossaryEntryIDRequest struct {
	ID string `json:"id"`
}

type glossarySpeakerRequest struct {
	Speaker string `json:"speaker"`
}

type glossaryAppellationLookupRequest struct {
	Speaker string `json:"speaker"`
	Target  string `json:"target"`
}

type glossaryAppellationLookupResponse struct {
	Found   bool   `json:"found"`
	Speaker string `json:"speaker,omitempty"`
	Target  string `json:"target,omitempty"`
	JP      string `json:"jp,omitempty"`
	CN      string `json:"cn,omitempty"`
}

type glossaryGrammarRequest struct {
	Query string `json:"q,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// InitializeGlossary binds the local mobile glossary to dataDir independently
// from the story/editor runtime. The service persists to
// {dataDir}/resources/glossary/glossary.json.
func InitializeGlossary(dataDir string) error {
	if strings.TrimSpace(dataDir) == "" {
		return fmt.Errorf("initialize mobile glossary: data directory is required")
	}

	root, err := filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("initialize mobile glossary: resolve data directory: %w", err)
	}
	glossaryDir := filepath.Join(root, "resources", "glossary")
	if err := os.MkdirAll(glossaryDir, 0o755); err != nil {
		return fmt.Errorf("initialize mobile glossary: create data directory %q: %w", glossaryDir, err)
	}

	mobileGlossaryState.initializeMu.Lock()
	defer mobileGlossaryState.initializeMu.Unlock()
	mobileGlossaryState.mu.RLock()
	alreadyInitialized := mobileGlossaryState.store != nil && mobileGlossaryState.root == root
	mobileGlossaryState.mu.RUnlock()
	if alreadyInitialized {
		return nil
	}

	mobileTeamBindingState.mu.Lock()
	defer mobileTeamBindingState.mu.Unlock()

	// GlossaryStore intentionally logs load failures and starts empty. At the
	// mobile boundary, reject unreadable/corrupt persisted data instead so an
	// Android caller gets an actionable error rather than an apparently empty
	// library.
	persistedPath := filepath.Join(glossaryDir, "glossary.json")
	persisted, err := os.ReadFile(persistedPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("initialize mobile glossary: read persisted data %q: %w", persistedPath, err)
	}
	if err == nil && len(bytes.TrimSpace(persisted)) > 0 {
		var data model.GlossaryData
		if err := json.Unmarshal(persisted, &data); err != nil {
			return fmt.Errorf("initialize mobile glossary: decode persisted data %q: %w", persistedPath, err)
		}
	}

	store := service.NewGlossaryStore(root)
	mobileGlossaryState.mu.Lock()
	mobileGlossaryState.store = store
	mobileGlossaryState.root = root
	mobileGlossaryState.mu.Unlock()
	mobileTeamBindingState.generation++
	return nil
}

func currentGlossaryStore() (*service.GlossaryStore, error) {
	mobileGlossaryState.mu.RLock()
	store := mobileGlossaryState.store
	mobileGlossaryState.mu.RUnlock()
	if store == nil {
		return nil, fmt.Errorf("mobile glossary is not initialized; call InitializeGlossary first")
	}
	return store, nil
}

func decodeGlossaryRequest(operation, requestJSON string, target any) error {
	if strings.TrimSpace(requestJSON) == "" {
		return fmt.Errorf("decode %s request: JSON payload is required", operation)
	}
	if err := json.Unmarshal([]byte(requestJSON), target); err != nil {
		return fmt.Errorf("decode %s request: %w", operation, err)
	}
	return nil
}

func encodeGlossaryResponse(operation string, value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode %s response: %w", operation, err)
	}
	return string(encoded), nil
}

// GlossarySearch searches source terms, translations, and aliases. requestJSON:
// {"q":"...","category":"...","limit":50}. Omitted limit defaults to 50.
func GlossarySearch(requestJSON string) (string, error) {
	store, err := currentGlossaryStore()
	if err != nil {
		return "", err
	}
	var req glossarySearchRequest
	if err := decodeGlossaryRequest("glossary search", requestJSON, &req); err != nil {
		return "", err
	}
	limit := 50
	if req.Limit != nil {
		limit = *req.Limit
	}
	return encodeGlossaryResponse("glossary search", store.Search(req.Query, req.Category, limit))
}

// GlossaryCategories returns [{"category":"...","count":n}, ...].
func GlossaryCategories() (string, error) {
	store, err := currentGlossaryStore()
	if err != nil {
		return "", err
	}
	return encodeGlossaryResponse("glossary categories", store.Categories())
}

// GlossaryEntries browses entries with requestJSON:
// {"category":"...","offset":0,"limit":200}. Omitted limit defaults to 200.
func GlossaryEntries(requestJSON string) (string, error) {
	store, err := currentGlossaryStore()
	if err != nil {
		return "", err
	}
	var req glossaryEntriesRequest
	if err := decodeGlossaryRequest("glossary entries", requestJSON, &req); err != nil {
		return "", err
	}
	limit := 200
	if req.Limit != nil {
		limit = *req.Limit
	}
	items, total := store.Entries(req.Category, req.Offset, limit)
	if items == nil {
		items = []model.GlossaryEntry{}
	}
	return encodeGlossaryResponse("glossary entries", glossaryEntriesResponse{Items: items, Total: total})
}

// GlossaryAddEntry inserts a user-authored entry. requestJSON is a
// model.GlossaryEntry JSON object; id and origin are assigned by GlossaryStore.
func GlossaryAddEntry(requestJSON string) (string, error) {
	store, err := currentGlossaryStore()
	if err != nil {
		return "", err
	}
	var entry model.GlossaryEntry
	if err := decodeGlossaryRequest("glossary add entry", requestJSON, &entry); err != nil {
		return "", err
	}
	if strings.TrimSpace(entry.Source) == "" {
		return "", fmt.Errorf("add glossary entry: source is required")
	}
	saved, err := store.AddEntry(entry)
	if err != nil {
		return "", fmt.Errorf("add glossary entry: persist entry: %w", err)
	}
	return encodeGlossaryResponse("glossary add entry", saved)
}

// GlossaryUpdateEntry updates an entry with requestJSON:
// {"id":"...","entry":{...full replacement fields...}}.
func GlossaryUpdateEntry(requestJSON string) (string, error) {
	store, err := currentGlossaryStore()
	if err != nil {
		return "", err
	}
	var req glossaryUpdateEntryRequest
	if err := decodeGlossaryRequest("glossary update entry", requestJSON, &req); err != nil {
		return "", err
	}
	if strings.TrimSpace(req.ID) == "" {
		return "", fmt.Errorf("update glossary entry: id is required")
	}
	if strings.TrimSpace(req.Entry.Source) == "" {
		return "", fmt.Errorf("update glossary entry %q: source is required", req.ID)
	}
	saved, found, err := store.UpdateEntry(req.ID, req.Entry)
	if err != nil {
		return "", fmt.Errorf("update glossary entry %q: persist entry: %w", req.ID, err)
	}
	if !found {
		return "", fmt.Errorf("update glossary entry %q: entry not found", req.ID)
	}
	return encodeGlossaryResponse("glossary update entry", saved)
}

// GlossaryDeleteEntry deletes an entry with requestJSON: {"id":"..."}.
func GlossaryDeleteEntry(requestJSON string) (string, error) {
	store, err := currentGlossaryStore()
	if err != nil {
		return "", err
	}
	var req glossaryEntryIDRequest
	if err := decodeGlossaryRequest("glossary delete entry", requestJSON, &req); err != nil {
		return "", err
	}
	if strings.TrimSpace(req.ID) == "" {
		return "", fmt.Errorf("delete glossary entry: id is required")
	}
	found, err := store.DeleteEntry(req.ID)
	if err != nil {
		return "", fmt.Errorf("delete glossary entry %q: persist deletion: %w", req.ID, err)
	}
	if !found {
		return "", fmt.Errorf("delete glossary entry %q: entry not found", req.ID)
	}
	return encodeGlossaryResponse("glossary delete entry", map[string]string{"status": "deleted"})
}

// GlossaryAppellationSpeakers returns the distinct appellation speakers.
func GlossaryAppellationSpeakers() (string, error) {
	store, err := currentGlossaryStore()
	if err != nil {
		return "", err
	}
	return encodeGlossaryResponse("glossary appellation speakers", store.AppellationSpeakers())
}

// GlossaryAppellationTargets returns targets for requestJSON:
// {"speaker":"..."}.
func GlossaryAppellationTargets(requestJSON string) (string, error) {
	store, err := currentGlossaryStore()
	if err != nil {
		return "", err
	}
	var req glossarySpeakerRequest
	if err := decodeGlossaryRequest("glossary appellation targets", requestJSON, &req); err != nil {
		return "", err
	}
	if strings.TrimSpace(req.Speaker) == "" {
		return "", fmt.Errorf("list glossary appellation targets: speaker is required")
	}
	return encodeGlossaryResponse("glossary appellation targets", store.AppellationTargets(req.Speaker))
}

// GlossaryAppellationLookup looks up requestJSON:
// {"speaker":"...","target":"..."}.
func GlossaryAppellationLookup(requestJSON string) (string, error) {
	store, err := currentGlossaryStore()
	if err != nil {
		return "", err
	}
	var req glossaryAppellationLookupRequest
	if err := decodeGlossaryRequest("glossary appellation lookup", requestJSON, &req); err != nil {
		return "", err
	}
	if strings.TrimSpace(req.Speaker) == "" || strings.TrimSpace(req.Target) == "" {
		return "", fmt.Errorf("lookup glossary appellation: speaker and target are required")
	}
	appellation, found := store.AppellationLookup(req.Speaker, req.Target)
	response := glossaryAppellationLookupResponse{Found: found}
	if found {
		response.Speaker = appellation.Speaker
		response.Target = appellation.Target
		response.JP = appellation.JP
		response.CN = appellation.CN
	}
	return encodeGlossaryResponse("glossary appellation lookup", response)
}

// GlossaryAppellationUpsert inserts or updates a model.Appellation JSON object.
func GlossaryAppellationUpsert(requestJSON string) (string, error) {
	store, err := currentGlossaryStore()
	if err != nil {
		return "", err
	}
	var appellation model.Appellation
	if err := decodeGlossaryRequest("glossary appellation upsert", requestJSON, &appellation); err != nil {
		return "", err
	}
	if strings.TrimSpace(appellation.Speaker) == "" || strings.TrimSpace(appellation.Target) == "" {
		return "", fmt.Errorf("upsert glossary appellation: speaker and target are required")
	}
	if err := store.UpsertAppellation(appellation); err != nil {
		return "", fmt.Errorf("upsert glossary appellation %q -> %q: persist appellation: %w", appellation.Speaker, appellation.Target, err)
	}
	return encodeGlossaryResponse("glossary appellation upsert", appellation)
}

// GlossaryGrammar searches grammar usages with requestJSON:
// {"q":"...","limit":0}. An empty query returns the first limit rows;
// limit <= 0 returns all rows.
func GlossaryGrammar(requestJSON string) (string, error) {
	store, err := currentGlossaryStore()
	if err != nil {
		return "", err
	}
	var req glossaryGrammarRequest
	if err := decodeGlossaryRequest("glossary grammar", requestJSON, &req); err != nil {
		return "", err
	}
	return encodeGlossaryResponse("glossary grammar", store.SearchGrammar(req.Query, req.Limit))
}

// GlossaryExport returns the complete local model.GlossaryData payload as JSON.
func GlossaryExport() (string, error) {
	store, err := currentGlossaryStore()
	if err != nil {
		return "", err
	}
	data := store.Export()
	if data.Entries == nil {
		data.Entries = []model.GlossaryEntry{}
	}
	if data.Appellations == nil {
		data.Appellations = []model.Appellation{}
	}
	return encodeGlossaryResponse("glossary export", data)
}
