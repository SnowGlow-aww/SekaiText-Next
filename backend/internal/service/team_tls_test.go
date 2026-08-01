package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func testServerRoots(t *testing.T, servers ...*httptest.Server) *x509.CertPool {
	t.Helper()
	roots := x509.NewCertPool()
	for _, server := range servers {
		roots.AddCert(server.Certificate())
	}
	return roots
}

func TestOfficialTeamRootIsValidAndOriginScoped(t *testing.T) {
	block, rest := pem.Decode(officialTeamRootPEM)
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		t.Fatal("embedded official team root is not one canonical PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !cert.IsCA || cert.Subject.CommonName != "Caddy Local Authority - 2026 ECC Root" {
		t.Fatalf("unexpected embedded root identity: subject=%q isCA=%v", cert.Subject, cert.IsCA)
	}
	sum := sha256.Sum256(cert.Raw)
	if got := hex.EncodeToString(sum[:]); got != "d2e9b36c337e7203d30b8741ab523822fe35b0d31e6dc2519d5f998c0f190925" {
		t.Fatalf("embedded official root fingerprint = %s", got)
	}

	serverURL, client, err := newTeamHTTPClient(officialTeamServerOrigin+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if serverURL != officialTeamServerOrigin {
		t.Fatalf("normalized official server URL = %q", serverURL)
	}
	roots := client.Transport.(*http.Transport).TLSClientConfig.RootCAs
	if roots == nil {
		t.Fatal("official server client did not receive the embedded trust root")
	}
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		t.Fatalf("embedded root is not trusted by the official server client: %v", err)
	}

	for _, raw := range []string{
		"https://8.140.254.217",
		"https://8.140.254.217:443",
		"https://8.140.254.217.evil.example:8443",
	} {
		_, otherClient, err := newTeamHTTPClient(raw, nil)
		if err != nil {
			t.Fatalf("newTeamHTTPClient(%q): %v", raw, err)
		}
		if got := otherClient.Transport.(*http.Transport).TLSClientConfig.RootCAs; got != nil {
			t.Fatalf("embedded official root leaked to non-official origin %q", raw)
		}
	}
}

func TestTeamLoginRejectsUntrustedCertificate(t *testing.T) {
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
	_, err := svc.Login(server.URL, "amia", "secret")
	var unknownAuthority x509.UnknownAuthorityError
	if !errors.As(err, &unknownAuthority) {
		t.Fatalf("Login error = %v, want x509.UnknownAuthorityError", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("server received %d credential requests before certificate verification", hits.Load())
	}
}

func TestTeamLoginAcceptsExplicitlyTrustedCertificate(t *testing.T) {
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

	svc := NewTeamServiceWithRootCAs(t.TempDir(), testServerRoots(t, server))
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

func TestTeamClientRejectsTrustedCertificateForWrongHostname(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	serverURL, client, err := newTeamHTTPClient("https://wrong-host.invalid", testServerRoots(t, server))
	if err != nil {
		t.Fatal(err)
	}
	transport := client.Transport.(*http.Transport)
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	_, err = client.Get(serverURL)
	var hostnameErr x509.HostnameError
	if !errors.As(err, &hostnameErr) {
		t.Fatalf("request error = %v, want x509.HostnameError", err)
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

	svc := NewTeamServiceWithRootCAs(t.TempDir(), testServerRoots(t, source, target))
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

			svc := NewTeamServiceWithRootCAs(dir, testServerRoots(t, server))
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

func TestSnapshotClientAllowsClashFakeIPForHostname(t *testing.T) {
	wantErr := errors.New("dial reached")
	transport := newPublicSnapshotTransport(
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("198.18.0.73")}}, nil
		},
		func(_ context.Context, _, address string) (net.Conn, error) {
			if address != "198.18.0.73:443" {
				t.Fatalf("dial address = %q", address)
			}
			return nil, wantErr
		},
	)
	_, err := transport.DialContext(context.Background(), "tcp", "cdn.example.com:443")
	if !errors.Is(err, wantErr) {
		t.Fatalf("DialContext error = %v, want dial sentinel", err)
	}
	if publicSnapshotURLAllowed("https://198.18.0.73/app-release.json") {
		t.Fatal("raw Fake-IP URL must remain blocked")
	}
}
