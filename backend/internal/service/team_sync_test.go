package service

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type teamSyncCallResult struct {
	result TeamSyncResult
	err    error
}

func TestTeamSyncSerializesVersionExportMergeAndCommit(t *testing.T) {
	firstExportStarted := make(chan struct{})
	releaseFirstExport := make(chan struct{})
	secondVersionRequested := make(chan struct{})
	var versionHits atomic.Int32
	var exportHits atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/config":
			w.WriteHeader(http.StatusNotFound)
		case "/api/glossary/version":
			n := versionHits.Add(1)
			if n == 2 {
				close(secondVersionRequested)
			}
			_, _ = io.WriteString(w, `{"version":`+strconv.Itoa(int(n))+`}`)
		case "/api/glossary/export":
			n := exportHits.Add(1)
			if n == 1 {
				close(firstExportStarted)
				<-releaseFirstExport
			}
			_, _ = io.WriteString(w, `{"entries":[{"source":"v`+strconv.Itoa(int(n))+`"}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	svc := newReadonlyTeam(t, server.URL)
	var mergedMu sync.Mutex
	var merged []string
	merge := func(raw []byte) (int, error) {
		var payload struct {
			Entries []struct {
				Source string `json:"source"`
			} `json:"entries"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return 0, err
		}
		mergedMu.Lock()
		merged = append(merged, payload.Entries[0].Source)
		mergedMu.Unlock()
		return 0, nil
	}

	firstDone := make(chan teamSyncCallResult, 1)
	go func() {
		result, err := svc.Sync(false, merge)
		firstDone <- teamSyncCallResult{result: result, err: err}
	}()
	<-firstExportStarted

	secondStarted := make(chan struct{})
	secondDone := make(chan teamSyncCallResult, 1)
	go func() {
		close(secondStarted)
		result, err := svc.Sync(false, merge)
		secondDone <- teamSyncCallResult{result: result, err: err}
	}()
	<-secondStarted
	select {
	case <-secondVersionRequested:
		t.Fatal("second sync polled its version before the first sync committed")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirstExport)

	first := <-firstDone
	second := <-secondDone
	if first.err != nil || second.err != nil {
		t.Fatalf("sync errors: first=%v second=%v", first.err, second.err)
	}
	if first.result.Version != 1 || second.result.Version != 2 {
		t.Fatalf("sync versions = %d, %d; want 1, 2", first.result.Version, second.result.Version)
	}
	if got := svc.LastSyncedVersion(); got != 2 {
		t.Fatalf("last synced version = %d, want 2", got)
	}
	mergedMu.Lock()
	gotMerged := strings.Join(merged, ",")
	mergedMu.Unlock()
	if gotMerged != "v1,v2" {
		t.Fatalf("merge order = %q, want v1,v2", gotMerged)
	}
}

func TestTeamSyncRejectsSessionChangeBeforeMerge(t *testing.T) {
	exportStarted := make(chan struct{})
	releaseExport := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/config":
			w.WriteHeader(http.StatusNotFound)
		case "/api/glossary/version":
			_, _ = io.WriteString(w, `{"version":1}`)
		case "/api/glossary/export":
			close(exportStarted)
			<-releaseExport
			_, _ = io.WriteString(w, `{"entries":[{"source":"stale"}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	svc := newReadonlyTeam(t, server.URL)
	var merged atomic.Bool
	done := make(chan error, 1)
	go func() {
		_, err := svc.Sync(false, func([]byte) (int, error) {
			merged.Store(true)
			return 0, nil
		})
		done <- err
	}()
	<-exportStarted
	if err := svc.Disconnect(); err != nil {
		t.Fatal(err)
	}
	close(releaseExport)
	if err := <-done; !errors.Is(err, ErrStaleTeamSession) {
		t.Fatalf("Sync error = %v, want ErrStaleTeamSession", err)
	}
	if merged.Load() {
		t.Fatal("stale session export was merged")
	}
	if got := svc.LastSyncedVersion(); got != 0 {
		t.Fatalf("last synced version = %d after disconnect, want 0", got)
	}
}

func TestSnapshotDiscoveryDoesNotCrossFingerprintChange(t *testing.T) {
	firstConfigStarted := make(chan struct{})
	releaseFirstConfig := make(chan struct{})
	var configHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/config" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		n := configHits.Add(1)
		if n == 1 {
			close(firstConfigStarted)
			<-releaseFirstConfig
			_, _ = io.WriteString(w, `{"snapshotBase":"https://old.example"}`)
			return
		}
		_, _ = io.WriteString(w, `{"snapshotBase":"https://new.example"}`)
	}))
	defer server.Close()

	svc := newReadonlyTeam(t, server.URL)
	firstDone := make(chan string, 1)
	go func() { firstDone <- svc.snapshot() }()
	<-firstConfigStarted

	newFingerprint := strings.Repeat("b", 64)
	svc.sessionMu.Lock()
	svc.mu.Lock()
	svc.sessionEpoch++
	svc.resetServerCachesLocked(server.URL, newFingerprint)
	svc.fingerprint = newFingerprint
	svc.mu.Unlock()
	svc.sessionMu.Unlock()
	close(releaseFirstConfig)

	if got := <-firstDone; got != "" {
		t.Fatalf("stale discovery returned %q, want empty", got)
	}
	if got := svc.snapshot(); got != "https://new.example" {
		t.Fatalf("new fingerprint discovery = %q, want https://new.example", got)
	}
	if got := configHits.Load(); got != 2 {
		t.Fatalf("config requests = %d, want 2", got)
	}
}
