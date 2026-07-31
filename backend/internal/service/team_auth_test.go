package service

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	roots := testServerRoots(t, server)
	t.Run("login and logout", func(t *testing.T) {
		svc := NewTeamServiceWithRootCAs(unusableTeamDataDir(t), roots)
		user, err := svc.Login(server.URL, "amia", "secret")
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
		url, statusUser := svc.Status()
		svc.mu.RLock()
		access, refresh := svc.access, svc.refresh
		svc.mu.RUnlock()
		if url != server.URL || statusUser != nil || access != "" || refresh != "" || svc.LoggedIn() {
			t.Fatalf("logout did not clear memory before persistence failure: url=%q user=%+v access=%q refresh=%q", url, statusUser, access, refresh)
		}
	})

	t.Run("connect and disconnect", func(t *testing.T) {
		svc := NewTeamServiceWithRootCAs(unusableTeamDataDir(t), roots)
		if err := svc.Connect(server.URL); !errors.Is(err, ErrTeamPersistence) {
			t.Fatalf("Connect error = %v, want ErrTeamPersistence", err)
		}
		if !svc.Connected() {
			t.Fatal("successful remote connection was not retained in memory")
		}

		if err := svc.Disconnect(); !errors.Is(err, ErrTeamPersistence) {
			t.Fatalf("Disconnect error = %v, want ErrTeamPersistence", err)
		}
		url, user := svc.Status()
		if url != "" || user != nil || svc.Connected() {
			t.Fatalf("disconnect did not clear memory before persistence failure: url=%q user=%+v", url, user)
		}
	})
}

func writeRestorableTeamSession(t *testing.T, dir, serverURL, refresh string) {
	t.Helper()
	p := teamPersist{
		ServerURL:    serverURL,
		RefreshToken: refresh,
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

func TestTeamAuthenticationErrorsDoNotExposeRemoteText(t *testing.T) {
	const sensitive = "access-secret refresh-secret server-stack-secret"

	t.Run("login rejection", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/auth/login" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"`+sensitive+`"}`)
		}))
		defer server.Close()

		svc := NewTeamServiceWithRootCAsDeferredRefresh(t.TempDir(), testServerRoots(t, server))
		_, err := svc.Login(server.URL, "amia", "password")
		if err == nil || err.Error() != "login failed (HTTP 401)" || strings.Contains(err.Error(), sensitive) {
			t.Fatalf("Login error = %v", err)
		}
	})

	t.Run("oversized successful login response", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/auth/login" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = io.WriteString(w, `{"accessToken":"access","refreshToken":"refresh","user":{"id":"1","username":"amia"}}`)
			_, _ = io.WriteString(w, strings.Repeat(" ", int(maxTeamConfigBytes)))
		}))
		defer server.Close()

		svc := NewTeamServiceWithRootCAsDeferredRefresh(t.TempDir(), testServerRoots(t, server))
		_, err := svc.Login(server.URL, "amia", "password")
		if err == nil || err.Error() != "login failed: invalid authentication response" || svc.LoggedIn() {
			t.Fatalf("oversized Login result: error=%v loggedIn=%v", err, svc.LoggedIn())
		}
	})

	for _, tc := range []struct {
		name         string
		status       int
		want         string
		wantRetained bool
	}{
		{name: "terminal rejection", status: http.StatusUnauthorized, want: "refresh failed (HTTP 401)"},
		{name: "transient rejection", status: http.StatusServiceUnavailable, want: "refresh failed (HTTP 503)", wantRetained: true},
		{name: "malformed success", status: http.StatusOK, want: "refresh failed: invalid authentication response", wantRetained: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/auth/refresh" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"error":"`+sensitive+`"}`)
			}))
			defer server.Close()

			dir := t.TempDir()
			writeRestorableTeamSession(t, dir, server.URL, "saved-refresh")
			svc := NewTeamServiceWithRootCAsDeferredRefresh(dir, testServerRoots(t, server))
			err := svc.RefreshSessionIfNeeded()
			if err == nil || err.Error() != tc.want || strings.Contains(err.Error(), sensitive) {
				t.Fatalf("RefreshSessionIfNeeded error = %v, want %q", err, tc.want)
			}
			retained := persistedRefreshToken(t, dir) == "saved-refresh"
			if retained != tc.wantRetained {
				t.Fatalf("refresh credential retained = %v, want %v", retained, tc.wantRetained)
			}
		})
	}

	t.Run("refresh requires a rotated refresh token", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/auth/refresh" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = io.WriteString(w, `{"accessToken":"renewed-access","user":{"id":"1","username":"amia"}}`)
		}))
		defer server.Close()

		dir := t.TempDir()
		writeRestorableTeamSession(t, dir, server.URL, "saved-refresh")
		svc := NewTeamServiceWithRootCAsDeferredRefresh(dir, testServerRoots(t, server))
		err := svc.RefreshSessionIfNeeded()
		if err == nil || err.Error() != "refresh failed: invalid authentication response" {
			t.Fatalf("RefreshSessionIfNeeded error = %v", err)
		}
		if got := persistedRefreshToken(t, dir); got != "saved-refresh" {
			t.Fatalf("malformed success replaced retryable credential with %q", got)
		}
	})

	t.Run("network failure", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		dir := t.TempDir()
		writeRestorableTeamSession(t, dir, server.URL, "saved-refresh")
		server.Close()

		svc := NewTeamServiceWithRootCAsDeferredRefresh(dir, nil)
		err := svc.RefreshSessionIfNeeded()
		if err == nil || err.Error() != "refresh failed: request could not be completed" {
			t.Fatalf("network refresh error = %v", err)
		}
	})
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
		writeRestorableTeamSession(t, dir, server.URL, "retryable")

		svc := NewTeamServiceWithRootCAs(dir, testServerRoots(t, server))
		if err := svc.RefreshSessionIfNeeded(); err == nil {
			t.Fatal("transient refresh failure unexpectedly succeeded")
		}
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
		writeRestorableTeamSession(t, dir, server.URL, "retryable")
		server.Close()

		svc := NewTeamService(dir)
		if err := svc.RefreshSessionIfNeeded(); err == nil {
			t.Fatal("network refresh failure unexpectedly succeeded")
		}
		svc.mu.RLock()
		refresh := svc.refresh
		svc.mu.RUnlock()
		if refresh != "retryable" || persistedRefreshToken(t, dir) != "retryable" {
			t.Fatal("startup network error cleared retryable persisted credentials")
		}
	})
}

func TestAuthenticatedRequestRetriesTransientLazyRefresh(t *testing.T) {
	var refreshHits atomic.Int32
	var protectedHits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/refresh":
			if refreshHits.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, `{"accessToken":"renewed-access","refreshToken":"renewed-refresh","user":{"id":"1","username":"amia"}}`)
		case "/api/proposals/mine":
			protectedHits.Add(1)
			if r.Header.Get("Authorization") != "Bearer renewed-access" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `[]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	writeRestorableTeamSession(t, dir, server.URL, "retryable")
	svc := NewTeamServiceWithRootCAsDeferredRefresh(dir, testServerRoots(t, server))
	if refreshHits.Load() != 0 {
		t.Fatalf("deferred constructor performed network refresh: hits=%d", refreshHits.Load())
	}
	if _, _, err := svc.Proxy(http.MethodGet, "/api/proposals/mine", nil); err == nil {
		t.Fatal("first transient refresh failure unexpectedly reached authenticated endpoint")
	}
	body, status, err := svc.Proxy(http.MethodGet, "/api/proposals/mine", nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK || string(body) != `[]` || protectedHits.Load() != 1 {
		t.Fatalf("retry result: status=%d body=%s protectedHits=%d", status, body, protectedHits.Load())
	}
	if refreshHits.Load() != 2 || persistedRefreshToken(t, dir) != "renewed-refresh" {
		t.Fatalf("refresh was not safely retried: hits=%d persisted=%q", refreshHits.Load(), persistedRefreshToken(t, dir))
	}
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
	writeRestorableTeamSession(t, dir, server.URL, "revoked")

	svc := NewTeamServiceWithRootCAs(dir, testServerRoots(t, server))
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
