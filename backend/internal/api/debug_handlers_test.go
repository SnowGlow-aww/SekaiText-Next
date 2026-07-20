package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sekaitext/backend/internal/config"
	"sekaitext/backend/internal/service"
)

func TestDebugSaveLogsWritesMergedRedactedPayload(t *testing.T) {
	dir := t.TempDir()
	h := &Handler{
		cfg:    &config.AppConfig{DataBaseDir: dir, DataDir: dir},
		logBuf: service.NewLogBuffer(10),
	}
	body := `{"lines":["[10:00:00] [front] password=front-secret /Users/amia/project","[10:00:01] [server] Authorization: Bearer server-secret"]}`
	req := httptest.NewRequest(http.MethodPost, "/debug/save", strings.NewReader(body))
	rec := httptest.NewRecorder()

	h.DebugSaveLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "debug.log"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{"[front]", "[server]", "[REDACTED]", "/Users/[USER]/project"} {
		if !strings.Contains(got, want) {
			t.Errorf("debug.log missing %q:\n%s", want, got)
		}
	}
	for _, secret := range []string{"front-secret", "server-secret", "/Users/amia"} {
		if strings.Contains(got, secret) {
			t.Errorf("debug.log leaked %q:\n%s", secret, got)
		}
	}
}
