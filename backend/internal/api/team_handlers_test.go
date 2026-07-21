package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sekaitext/backend/internal/service"
)

func teamHandlerWithUnwritableSession(t *testing.T) *Handler {
	t.Helper()
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("block child paths"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &Handler{team: service.NewTeamService(path)}
}

func TestTeamSessionHandlersExposePersistenceFailures(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			_, _ = io.WriteString(w, `{"accessToken":"access","refreshToken":"refresh","user":{"id":"1","username":"amia"}}`)
		case "/api/glossary/version":
			_, _ = io.WriteString(w, `{"version":1}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	h := teamHandlerWithUnwritableSession(t)
	loginBody := `{"serverUrl":"` + server.URL + `","username":"amia","password":"secret"}`
	login := httptest.NewRecorder()
	h.TeamLogin(login, httptest.NewRequest(http.MethodPost, "/team/login", strings.NewReader(loginBody)))
	if login.Code != http.StatusInternalServerError {
		t.Fatalf("login status = %d, want 500; body=%s", login.Code, login.Body.String())
	}

	logout := httptest.NewRecorder()
	h.TeamLogout(logout, httptest.NewRequest(http.MethodPost, "/team/logout", nil))
	if logout.Code != http.StatusInternalServerError {
		t.Fatalf("logout status = %d, want 500; body=%s", logout.Code, logout.Body.String())
	}
	if h.team.LoggedIn() {
		t.Fatal("logout persistence failure left the in-memory session logged in")
	}

	h = teamHandlerWithUnwritableSession(t)
	connectBody := `{"serverUrl":"` + server.URL + `"}`
	connect := httptest.NewRecorder()
	h.TeamConnect(connect, httptest.NewRequest(http.MethodPost, "/team/connect", strings.NewReader(connectBody)))
	if connect.Code != http.StatusInternalServerError {
		t.Fatalf("connect status = %d, want 500; body=%s", connect.Code, connect.Body.String())
	}

	disconnect := httptest.NewRecorder()
	h.TeamDisconnect(disconnect, httptest.NewRequest(http.MethodPost, "/team/disconnect", nil))
	if disconnect.Code != http.StatusInternalServerError {
		t.Fatalf("disconnect status = %d, want 500; body=%s", disconnect.Code, disconnect.Body.String())
	}
	if h.team.Connected() {
		t.Fatal("disconnect persistence failure left the in-memory session connected")
	}
}
