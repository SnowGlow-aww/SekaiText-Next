package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"sekaitext/backend/internal/model"
	"sekaitext/backend/internal/service"
)

func glossarySyncHandler(t *testing.T) (*Handler, *service.GlossaryStore) {
	t.Helper()
	store := service.NewGlossaryStore(t.TempDir())
	if _, err := store.MergeImport(
		[]model.GlossaryEntry{{ID: "remote-entry", Source: "ミク", Translation: "未来", Category: "人名"}},
		[]model.Appellation{{Speaker: "ミク", Target: "リン", JP: "リン"}},
		[]model.GrammarUsage{{ID: "remote-grammar", Item: "〜ながら"}},
		model.OriginRemote,
	); err != nil {
		t.Fatal(err)
	}
	return &Handler{glossary: store}, store
}

func runGlossarySync(t *testing.T, h *Handler, payload string) *httptest.ResponseRecorder {
	t.Helper()
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	}))
	defer remote.Close()

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/glossary/sync",
		strings.NewReader(`{"remoteUrl":"`+remote.URL+`"}`),
	)
	rr := httptest.NewRecorder()
	h.GlossarySync(rr, req)
	return rr
}

func TestGlossarySyncRejectsIncompleteSnapshotsWithoutDeletingRemoteData(t *testing.T) {
	for _, tt := range []struct {
		name    string
		payload string
	}{
		{name: "null root", payload: `null`},
		{name: "empty object", payload: `{}`},
		{name: "missing entries", payload: `{"appellations":[]}`},
		{name: "missing appellations", payload: `{"entries":[]}`},
		{name: "null entries", payload: `{"entries":null,"appellations":[]}`},
		{name: "null appellations", payload: `{"entries":[],"appellations":null}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, store := glossarySyncHandler(t)
			before := store.Export()
			rr := runGlossarySync(t, h, tt.payload)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
			if after := store.Export(); !reflect.DeepEqual(after, before) {
				t.Fatalf("invalid snapshot changed remote data:\nbefore=%#v\nafter=%#v", before, after)
			}
		})
	}
}

func TestGlossarySyncAcceptsExplicitEmptyAuthoritativeSnapshot(t *testing.T) {
	h, store := glossarySyncHandler(t)
	rr := runGlossarySync(t, h, `{"entries":[],"appellations":[]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	snapshot := store.Export()
	if len(snapshot.Entries) != 0 || len(snapshot.Appellations) != 0 || len(snapshot.Grammar) != 0 {
		t.Fatalf("explicit empty snapshot was not applied authoritatively: %#v", snapshot)
	}
}

func TestGlossarySyncRejectsOversizedRemoteSnapshot(t *testing.T) {
	h, store := glossarySyncHandler(t)
	before := store.Export()
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, strings.Repeat("x", maxRemoteGlossarySnapshotBytes+1))
	}))
	defer remote.Close()

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/glossary/sync",
		strings.NewReader(`{"remoteUrl":"`+remote.URL+`"}`),
	)
	rr := httptest.NewRecorder()
	h.GlossarySync(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "exceeds 32 MiB") {
		t.Fatalf("oversized response did not report the size limit: %s", rr.Body.String())
	}
	if after := store.Export(); !reflect.DeepEqual(after, before) {
		t.Fatalf("oversized snapshot changed remote data:\nbefore=%#v\nafter=%#v", before, after)
	}
}
