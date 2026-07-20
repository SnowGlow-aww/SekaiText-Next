package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"sekaitext/backend/internal/config"
	"sekaitext/backend/internal/fsutil"
	"sekaitext/backend/internal/model"
	"sekaitext/backend/internal/service"
)

func TestWriteFileAtomicUsesIndependentTemporaryFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "translation.txt")
	payloads := make([][]byte, 12)
	for i := range payloads {
		payloads[i] = bytes.Repeat([]byte{byte('a' + i)}, 64*1024)
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(payloads))
	for i, payload := range payloads {
		wg.Add(1)
		go func(i int, payload []byte) {
			defer wg.Done()
			if err := fsutil.WriteFileAtomic(path, payload, 0o644); err != nil {
				errs <- fmt.Errorf("write %d: %w", i, err)
			}
		}(i, payload)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	valid := false
	for _, payload := range payloads {
		if bytes.Equal(got, payload) {
			valid = true
			break
		}
	}
	if !valid {
		t.Fatalf("final file is a torn write (%d bytes)", len(got))
	}
}

func recoveryTestHandler(dir string) *Handler {
	return &Handler{
		cfg:    &config.AppConfig{DataDir: dir},
		editor: service.NewEditorService(),
	}
}

func TestRecoveryV2SavesAllModesAndSourceContext(t *testing.T) {
	dir := t.TempDir()
	h := recoveryTestHandler(dir)
	body := `{
		"version":2,
		"activeMode":1,
		"saveN":true,
		"talks":[{"idx":1,"speaker":"瑞希","text":"兼容镜像","start":true,"end":true,"save":true}],
		"filePath":"/proofread.txt",
		"editorMode":1,
		"modes":[
			{
				"editorMode":0,
				"talks":[{"idx":1,"speaker":"瑞希","text":"翻译稿","start":true,"end":true,"save":true}],
				"filePath":"/translate.txt",
				"titleOverride":"翻译标题",
				"hasUnsavedChanges":true,
				"sourceTalks":[{"speaker":"瑞希","text":"原文 A","charIndex":0}],
				"docMeta":{"saveTitle":"event-01","chapterTitle":"章节 A","type":"event","index":"1","indexLabel":"1 活动","chapter":0,"source":"haruki","scenarioId":"scenario-a"}
			},
			{
				"editorMode":1,
				"talks":[{"idx":1,"speaker":"瑞希","text":"校对稿","start":true,"end":true,"save":true}],
				"filePath":"/proofread.txt",
				"titleOverride":"校对标题",
				"hasUnsavedChanges":true,
				"sourceTalks":[{"speaker":"瑞希","text":"原文 B","charIndex":0}],
				"docMeta":{"saveTitle":"event-02","chapterTitle":"章节 B","type":"event","index":"2","indexLabel":"2 活动","chapter":1,"source":"haruki","scenarioId":"scenario-b"}
			}
		]
	}`
	req := httptest.NewRequest("POST", "/recovery/save", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.RecoverySave(rec, req)
	if rec.Code != 200 {
		t.Fatalf("save status=%d body=%s", rec.Code, rec.Body.String())
	}

	raw, err := os.ReadFile(filepath.Join(dir, "autosave.json"))
	if err != nil {
		t.Fatal(err)
	}
	var data model.RecoveryData
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if data.Version != 2 || data.ActiveMode != 1 || len(data.Modes) != 2 {
		t.Fatalf("bad V2 envelope: %+v", data)
	}
	if !strings.Contains(data.Modes[0].Content, "翻译稿") {
		t.Fatalf("mode content not serialized: %q", data.Modes[0].Content)
	}
	if got := data.Modes[1].SourceTalks[0].Text; got != "原文 B" {
		t.Fatalf("source context lost: %q", got)
	}
	if data.Modes[1].DocMeta == nil || data.Modes[1].DocMeta.ScenarioID != "scenario-b" {
		t.Fatalf("document context lost: %+v", data.Modes[1].DocMeta)
	}
	if data.Content != data.Modes[1].Content || data.EditorMode != 1 {
		t.Fatal("active mode was not mirrored to legacy fields")
	}
}

func TestRecoveryLoadAcceptsLegacySingleModeJSON(t *testing.T) {
	dir := t.TempDir()
	h := recoveryTestHandler(dir)
	legacy := `{"content":"瑞希：旧译文","filePath":"/old.txt","editorMode":2,"savedAt":"yesterday","storyType":"event","storyIndex":"17","storyChapter":3,"storySource":"haruki"}`
	if err := os.WriteFile(filepath.Join(dir, "autosave.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/recovery/load", nil)
	rec := httptest.NewRecorder()
	h.RecoveryLoad(rec, req)
	var response struct {
		Exists     bool                     `json:"exists"`
		Content    string                   `json:"content"`
		EditorMode int                      `json:"editorMode"`
		Modes      []model.RecoveryModeData `json:"modes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Exists || response.Content != "瑞希：旧译文" || response.EditorMode != 2 {
		t.Fatalf("legacy recovery not loaded: %+v", response)
	}
	if len(response.Modes) != 0 {
		t.Fatalf("legacy data should remain single-mode, got %+v", response.Modes)
	}
}

func TestRecoveryClearReportsFailureSoClientCanRetry(t *testing.T) {
	dir := t.TempDir()
	h := recoveryTestHandler(dir)
	recoveryDir := filepath.Join(dir, "autosave.json")
	if err := os.Mkdir(recoveryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recoveryDir, "blocker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("DELETE", "/recovery/clear", nil)
	rec := httptest.NewRecorder()
	h.RecoveryClear(rec, req)
	if rec.Code != 500 {
		t.Fatalf("failed clear status=%d body=%s", rec.Code, rec.Body.String())
	}

	if err := os.RemoveAll(recoveryDir); err != nil {
		t.Fatal(err)
	}
	retry := httptest.NewRecorder()
	h.RecoveryClear(retry, req)
	if retry.Code != 200 {
		t.Fatalf("retry status=%d body=%s", retry.Code, retry.Body.String())
	}
}
