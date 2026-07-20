package service

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func unusableTeamDataDir(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("block child paths"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTeamSessionMutationsReportPersistenceFailures(t *testing.T) {
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
	fingerprint := certificateFingerprint(server.Certificate().Raw)

	t.Run("login and logout", func(t *testing.T) {
		svc := NewTeamService(unusableTeamDataDir(t))
		user, err := svc.Login(server.URL, "amia", "secret", fingerprint)
		if !errors.Is(err, ErrTeamPersistence) {
			t.Fatalf("Login error = %v, want ErrTeamPersistence", err)
		}
		if user == nil || !svc.LoggedIn() {
			t.Fatalf("successful remote login was not retained in memory: user=%+v loggedIn=%v", user, svc.LoggedIn())
		}

		err = svc.Logout()
		if !errors.Is(err, ErrTeamPersistence) {
			t.Fatalf("Logout error = %v, want ErrTeamPersistence", err)
		}
		url, _, statusUser := svc.Status()
		svc.mu.RLock()
		access, refresh := svc.access, svc.refresh
		svc.mu.RUnlock()
		if url != server.URL || statusUser != nil || access != "" || refresh != "" || svc.LoggedIn() {
			t.Fatalf("logout did not clear memory before persistence failure: url=%q user=%+v access=%q refresh=%q", url, statusUser, access, refresh)
		}
	})

	t.Run("connect and disconnect", func(t *testing.T) {
		svc := NewTeamService(unusableTeamDataDir(t))
		if err := svc.Connect(server.URL, fingerprint); !errors.Is(err, ErrTeamPersistence) {
			t.Fatalf("Connect error = %v, want ErrTeamPersistence", err)
		}
		if !svc.Connected() {
			t.Fatal("successful remote connection was not retained in memory")
		}

		if err := svc.Disconnect(); !errors.Is(err, ErrTeamPersistence) {
			t.Fatalf("Disconnect error = %v, want ErrTeamPersistence", err)
		}
		url, pin, user := svc.Status()
		if url != "" || pin != "" || user != nil || svc.Connected() {
			t.Fatalf("disconnect did not clear memory before persistence failure: url=%q pin=%q user=%+v", url, pin, user)
		}
	})
}

func writeRestorableTeamSession(t *testing.T, dir, serverURL string, certDER []byte, refresh string) {
	t.Helper()
	p := teamPersist{
		ServerURL:              serverURL,
		RefreshToken:           refresh,
		CertificateFingerprint: certificateFingerprint(certDER),
		CertificateDER:         base64.StdEncoding.EncodeToString(certDER),
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "team-session.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func persistedRefreshToken(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "team-session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p teamPersist
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	return p.RefreshToken
}

func TestTeamRestoreRetainsCredentialsOnTransientRefreshFailure(t *testing.T) {
	t.Run("server error", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/auth/refresh" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()
		dir := t.TempDir()
		writeRestorableTeamSession(t, dir, server.URL, server.Certificate().Raw, "retryable")

		svc := NewTeamService(dir)
		svc.mu.RLock()
		refresh := svc.refresh
		svc.mu.RUnlock()
		if refresh != "retryable" || persistedRefreshToken(t, dir) != "retryable" {
			t.Fatal("startup 5xx cleared retryable persisted credentials")
		}
	})

	t.Run("network error", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		dir := t.TempDir()
		writeRestorableTeamSession(t, dir, server.URL, server.Certificate().Raw, "retryable")
		server.Close()

		svc := NewTeamService(dir)
		svc.mu.RLock()
		refresh := svc.refresh
		svc.mu.RUnlock()
		if refresh != "retryable" || persistedRefreshToken(t, dir) != "retryable" {
			t.Fatal("startup network error cleared retryable persisted credentials")
		}
	})
}

func TestTeamRestoreClearsCredentialsOnTerminalAuthRejection(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/refresh" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	dir := t.TempDir()
	writeRestorableTeamSession(t, dir, server.URL, server.Certificate().Raw, "revoked")

	svc := NewTeamService(dir)
	svc.mu.RLock()
	refresh := svc.refresh
	svc.mu.RUnlock()
	if refresh != "" || persistedRefreshToken(t, dir) != "" {
		t.Fatal("terminal startup auth rejection retained revoked credentials")
	}
}

func TestDisconnectSyncsSessionDirectoryAfterRemoval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "team-session.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewTeamService(dir)
	wantErr := errors.New("directory sync failed")
	called := false
	svc.syncDir = func(got string) error {
		called = true
		if got != dir {
			t.Fatalf("sync dir = %q, want %q", got, dir)
		}
		return wantErr
	}
	err := svc.Disconnect()
	if !called || !errors.Is(err, wantErr) || !errors.Is(err, ErrTeamPersistence) {
		t.Fatalf("Disconnect error = %v, sync called = %v", err, called)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("session file was not removed before directory sync: %v", statErr)
	}
}
