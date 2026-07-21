package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestTeamLoginAcceptsSelfSignedCertificate(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/api/auth/login" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"accessToken":"access","refreshToken":"refresh","user":{"id":"1","username":"amia","displayName":"Amia","role":"member","status":"active"}}`))
	}))
	defer server.Close()

	svc := NewTeamService(t.TempDir())
	user, err := svc.Login(server.URL, "amia", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if user == nil || user.Username != "amia" || hits.Load() != 1 {
		t.Fatalf("unexpected login result: user=%+v hits=%d", user, hits.Load())
	}
	url, statusUser := svc.Status()
	if url != server.URL || statusUser == nil {
		t.Fatalf("status not retained: url=%q user=%+v", url, statusUser)
	}
}

func TestTeamAuthenticationBlocksCrossOriginRedirect(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	svc := NewTeamService(t.TempDir())
	if _, err := svc.Login(source.URL, "user", "password"); err == nil {
		t.Fatal("Login unexpectedly followed a cross-origin redirect")
	}
	if targetHits.Load() != 0 {
		t.Fatalf("redirect target received %d credential requests", targetHits.Load())
	}
}

func TestTeamSessionRestoreIgnoresCertificateFingerprint(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/refresh" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"accessToken":"access","refreshToken":"renewed","user":{"id":"1","username":"amia"}}`)
	}))
	defer server.Close()

	for _, tc := range []struct {
		name       string
		storedHash bool
	}{
		{name: "pre-fingerprint session"},
		{name: "session with stale fingerprint", storedHash: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			persisted := map[string]string{
				"serverUrl":    server.URL,
				"refreshToken": "secret-refresh",
			}
			if tc.storedHash {
				persisted["certificateFingerprint"] = "stale-fingerprint"
				persisted["certificateDer"] = "stale-certificate"
			}
			raw, _ := json.Marshal(persisted)
			path := filepath.Join(dir, "team-session.json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}

			svc := NewTeamService(dir)
			if !svc.LoggedIn() || persistedRefreshToken(t, dir) != "renewed" {
				t.Fatal("persisted session did not refresh and remain logged in")
			}
			updated, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(updated, []byte("certificateFingerprint")) || bytes.Contains(updated, []byte("certificateDer")) {
				t.Fatalf("obsolete certificate fields remained after refresh: %s", updated)
			}
		})
	}
}

func TestSnapshotURLMustBePublicHTTPS(t *testing.T) {
	for _, raw := range []string{
		"http://cdn.example.com/export.json",
		"https://localhost/export.json",
		"https://127.0.0.1/export.json",
		"https://10.0.0.1/export.json",
		"https://100.64.0.1/export.json",
		"https://169.254.169.254/export.json",
		"https://192.0.2.1/export.json",
		"https://198.18.0.1/export.json",
		"https://[::1]/export.json",
		"https://[fe80::1]/export.json",
		"https://[fec0::1]/export.json",
		"https://[2001:db8::1]/export.json",
	} {
		if publicSnapshotURLAllowed(raw) {
			t.Errorf("publicSnapshotURLAllowed(%q) = true", raw)
		}
	}
	if raw := "https://cdn.example.com/snapshots/export.json"; !publicSnapshotURLAllowed(raw) {
		t.Errorf("publicSnapshotURLAllowed(%q) = false", raw)
	}
}

func TestSnapshotClientIgnoresEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:65535")
	client := newSnapshotHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("snapshot transport type = %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("snapshot transport inherited ProxyFromEnvironment")
	}
}
