package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"sekaitext/backend/internal/model"
	"sekaitext/backend/internal/service"
)

type translationSaveResponse struct {
	Status         string `json:"status"`
	ExistingDigest string `json:"existingDigest"`
}

func callTranslationSave(t *testing.T, h *Handler, path, text, expectedDigest string) translationSaveResponse {
	t.Helper()
	body, err := json.Marshal(model.TranslationSaveRequest{
		FilePath: path,
		Talks: []model.DstTalk{{
			Idx: 1, Speaker: "瑞希", Text: text, Start: true, End: true, Save: true,
		}},
		SaveN:                  true,
		ExpectedExistingDigest: expectedDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.TranslationSave(rec, httptest.NewRequest("POST", "/translation/save", bytes.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response translationSaveResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestTranslationSaveRequiresExactOverwriteConfirmation(t *testing.T) {
	h := &Handler{editor: service.NewEditorService()}
	path := filepath.Join(t.TempDir(), "translation.txt")

	if got := callTranslationSave(t, h, path, "初稿", ""); got.Status != "saved" {
		t.Fatalf("first status=%q", got.Status)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if got := callTranslationSave(t, h, path, "初稿", ""); got.Status != "unchanged" {
		t.Fatalf("same-content status=%q", got.Status)
	}

	conflict := callTranslationSave(t, h, path, "修改稿", "")
	if conflict.Status != "overwrite-required" || conflict.ExistingDigest == "" {
		t.Fatalf("conflict=%+v", conflict)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("unconfirmed save changed file: %q, %v", got, err)
	}

	if got := callTranslationSave(t, h, path, "修改稿", conflict.ExistingDigest); got.Status != "saved" {
		t.Fatalf("confirmed status=%q", got.Status)
	}
	if got, err := os.ReadFile(path); err != nil || bytes.Equal(got, original) {
		t.Fatalf("confirmed save did not replace file: %q, %v", got, err)
	}
}

func TestTranslationSaveRejectsStaleOverwriteConfirmation(t *testing.T) {
	h := &Handler{editor: service.NewEditorService()}
	path := filepath.Join(t.TempDir(), "translation.txt")
	if err := os.WriteFile(path, []byte("external-v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	first := callTranslationSave(t, h, path, "应用内修改", "")
	if first.Status != "overwrite-required" {
		t.Fatalf("first=%+v", first)
	}
	if err := os.WriteFile(path, []byte("external-v2"), 0o644); err != nil {
		t.Fatal(err)
	}

	stale := callTranslationSave(t, h, path, "应用内修改", first.ExistingDigest)
	if stale.Status != "overwrite-stale" || stale.ExistingDigest == "" || stale.ExistingDigest == first.ExistingDigest {
		t.Fatalf("stale=%+v", stale)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "external-v2" {
		t.Fatalf("stale confirmation changed file: %q, %v", got, err)
	}

	if got := callTranslationSave(t, h, path, "应用内修改", stale.ExistingDigest); got.Status != "saved" {
		t.Fatalf("reconfirmed status=%q", got.Status)
	}
}
