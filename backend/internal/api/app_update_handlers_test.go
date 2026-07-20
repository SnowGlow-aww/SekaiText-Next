package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"sekaitext/backend/internal/config"
	"sekaitext/backend/internal/model"
)

func TestAppUpdateOpenRejectsUntrackedInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("launcher interception in this test uses a POSIX shell")
	}
	dir := t.TempDir()
	installer := filepath.Join(dir, "untracked.dmg")
	if err := os.WriteFile(installer, []byte("not a tracked update"), 0644); err != nil {
		t.Fatal(err)
	}

	interceptUpdateLauncher(t)

	h := &Handler{cfg: &config.AppConfig{DataDir: dir}}
	req := httptest.NewRequest(http.MethodPost, "/app/open", strings.NewReader(`{"path":`+strconv.Quote(installer)+`}`))
	rec := httptest.NewRecorder()
	h.AppUpdateOpen(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestAppUpdateOpenRechecksTrackedArtifact(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("launcher interception in this test uses a POSIX shell")
	}
	dir := t.TempDir()
	installer := filepath.Join(dir, "SekaiText.dmg")
	payload := []byte("verified installer")
	if err := os.WriteFile(installer, payload, 0644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	digest := "sha256:" + hex.EncodeToString(sum[:])

	interceptUpdateLauncher(t)
	h := &Handler{cfg: &config.AppConfig{DataDir: dir}}
	task := &model.DownloadTaskProgress{
		TaskID:            "test-update",
		Status:            "done",
		FilePath:          installer,
		Purpose:           "app-update",
		Digest:            digest,
		ExpectedSize:      int64(len(payload)),
		IntegrityVerified: true,
	}
	h.downloadTasks.Store(task.TaskID, task)
	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/app/open", strings.NewReader(`{"path":`+strconv.Quote(installer)+`}`))
		rec := httptest.NewRecorder()
		h.AppUpdateOpen(rec, req)
		return rec
	}

	if rec := call(); rec.Code != http.StatusOK {
		t.Fatalf("verified artifact status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if err := os.WriteFile(installer, []byte("Verified installer"), 0644); err != nil {
		t.Fatal(err)
	}
	if rec := call(); rec.Code != http.StatusConflict {
		t.Fatalf("tampered artifact status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	task.Mu.Lock()
	verified := task.IntegrityVerified
	status := task.Status
	task.Mu.Unlock()
	if verified || status != "error" {
		t.Fatalf("tampered task remained usable: verified=%v status=%q", verified, status)
	}
}

func interceptUpdateLauncher(t *testing.T) {
	t.Helper()
	// Keep endpoint tests safe even if a regression reaches cmd.Start.
	launcher := "xdg-open"
	if runtime.GOOS == "darwin" {
		launcher = "open"
	}
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, launcher), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
}
