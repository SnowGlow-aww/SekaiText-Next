package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"sekaitext/backend/internal/config"
	"sekaitext/backend/internal/service"
)

func TestImportSaveDirOrganizesKnownAndUnknownDocuments(t *testing.T) {
	tmp := t.TempDir()
	catalogDir := filepath.Join(tmp, "catalog")
	dataDir := filepath.Join(tmp, "data")
	srcDir := filepath.Join(tmp, "imported_src")
	targetDir := filepath.Join(tmp, "target_save")

	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	eventsJSON := `[
		{"id": 211, "title": "Leap Beyond The Limits！", "name": "event_limits_2026", "chapters": [{"title": "始まりの時", "assetName": "event_211_01"}]}
	]`
	if err := os.WriteFile(filepath.Join(catalogDir, "events.json"), []byte(eventsJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	specialsJSON := `[
		{"title": "Special Story With Spaces", "dirName": "special_dir", "fileName": "special_01"}
	]`
	if err := os.WriteFile(filepath.Join(catalogDir, "specials.json"), []byte(specialsJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.AppConfig{
		CatalogDir:  catalogDir,
		DataDir:     dataDir,
		DataBaseDir: tmp,
	}
	h := NewHandler(cfg, service.NewLogBuffer(10))

	// 1. Event story file with 【翻译】 prefix
	if err := os.WriteFile(filepath.Join(srcDir, "【翻译】211-01 始まりの時.txt"), []byte("爱莉：测试译文"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 2. Special story without 【翻译】 prefix (should normalize to 【翻译】)
	if err := os.WriteFile(filepath.Join(srcDir, "Special Story With Spaces.txt"), []byte("特殊剧情正文"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 3. Nested unclassified custom document
	sub := filepath.Join(srcDir, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "custom_story.txt"), []byte("自由文稿"), 0o644); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{
		"srcDir":    srcDir,
		"targetDir": targetDir,
	})
	rec := httptest.NewRecorder()
	h.ImportSaveDir(rec, httptest.NewRequest("POST", "/save-dir/import", bytes.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var res struct {
		Total     int `json:"total"`
		Imported  int `json:"imported"`
		Unchanged int `json:"unchanged"`
		Failed    int `json:"failed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Total != 3 || res.Imported != 3 || res.Failed != 0 {
		t.Fatalf("unexpected import result: %+v", res)
	}

	// Verify organized files exist in targetDir
	expectedEventFile := filepath.Join(targetDir, "活动剧情", "211 Leap Beyond The Limits！", "【翻译】211-01 始まりの時.txt")
	if data, err := os.ReadFile(expectedEventFile); err != nil || string(data) != "爱莉：测试译文" {
		t.Fatalf("organized event file missing or content mismatch: %v, data=%q", err, string(data))
	}

	expectedSpecialFile := filepath.Join(targetDir, "特殊剧情", "Special Story With Spaces", "【翻译】Special Story With Spaces.txt")
	if data, err := os.ReadFile(expectedSpecialFile); err != nil || string(data) != "特殊剧情正文" {
		t.Fatalf("organized special file missing or content mismatch: %v, data=%q", err, string(data))
	}

	expectedUnclassified := filepath.Join(targetDir, "未分类", "custom_story.txt")
	if data, err := os.ReadFile(expectedUnclassified); err != nil || string(data) != "自由文稿" {
		t.Fatalf("unclassified file missing or content mismatch: %v, data=%q", err, string(data))
	}

	// Re-run import: identical files must be reported as unchanged (idempotence)
	rec2 := httptest.NewRecorder()
	h.ImportSaveDir(rec2, httptest.NewRequest("POST", "/save-dir/import", bytes.NewReader(body)))
	if rec2.Code != 200 {
		t.Fatalf("status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	var res2 struct {
		Total     int `json:"total"`
		Imported  int `json:"imported"`
		Unchanged int `json:"unchanged"`
	}
	json.Unmarshal(rec2.Body.Bytes(), &res2)
	if res2.Imported != 0 || res2.Unchanged != 3 {
		t.Fatalf("idempotent import mismatch: %+v", res2)
	}

	// Import conflicting differing content: must save safely with suffix rather than overwrite
	if err := os.WriteFile(filepath.Join(srcDir, "【翻译】211-01 始まりの時.txt"), []byte("爱莉：修改后的新译文"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec3 := httptest.NewRecorder()
	h.ImportSaveDir(rec3, httptest.NewRequest("POST", "/save-dir/import", bytes.NewReader(body)))
	if rec3.Code != 200 {
		t.Fatalf("status=%d body=%s", rec3.Code, rec3.Body.String())
	}
	var res3 struct {
		Total     int `json:"total"`
		Imported  int `json:"imported"`
		Unchanged int `json:"unchanged"`
	}
	json.Unmarshal(rec3.Body.Bytes(), &res3)
	if res3.Imported != 1 || res3.Unchanged != 2 {
		t.Fatalf("conflict import mismatch: %+v", res3)
	}

	// Original file must be preserved intact
	if data, err := os.ReadFile(expectedEventFile); err != nil || string(data) != "爱莉：测试译文" {
		t.Fatalf("original file was overwritten: %q", string(data))
	}
	// Suffix conflict file must contain new content
	conflictFile := filepath.Join(targetDir, "活动剧情", "211 Leap Beyond The Limits！", "【翻译】211-01 始まりの時 (2).txt")
	if data, err := os.ReadFile(conflictFile); err != nil || string(data) != "爱莉：修改后的新译文" {
		t.Fatalf("conflict file missing or content mismatch: %v, data=%q", err, string(data))
	}
}
