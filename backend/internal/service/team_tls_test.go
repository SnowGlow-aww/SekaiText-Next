package service

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func newUniqueTeamTLSServer(t *testing.T) *httptest.Server {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	return server
}

func TestTeamProbeSendsNoHTTPRequestAndPinnedLoginSucceeds(t *testing.T) {
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
	probe, _, err := svc.ProbeCertificate(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 0 {
		t.Fatalf("certificate probe sent %d HTTP requests", hits.Load())
	}
	if probe.Fingerprint == "" || probe.Trusted || probe.Changed {
		t.Fatalf("unexpected first probe: %+v", probe)
	}

	user, err := svc.Login(server.URL, "amia", "secret", probe.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if user == nil || user.Username != "amia" || hits.Load() != 1 {
		t.Fatalf("unexpected login result: user=%+v hits=%d", user, hits.Load())
	}
	url, fingerprint, statusUser := svc.Status()
	if url != server.URL || fingerprint != probe.Fingerprint || statusUser == nil {
		t.Fatalf("pinned status not retained: url=%q fingerprint=%q user=%+v", url, fingerprint, statusUser)
	}
}

func TestTeamLoginRefusesCredentialsWithoutConfirmedPin(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	defer server.Close()

	svc := NewTeamService(t.TempDir())
	if _, err := svc.Login(server.URL, "user", "password", ""); !errors.Is(err, ErrTeamFingerprintRequired) {
		t.Fatalf("Login error = %v, want ErrTeamFingerprintRequired", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("unpinned login sent %d HTTP requests", hits.Load())
	}
}

func TestPinnedTeamClientBlocksCertificateChange(t *testing.T) {
	first := newUniqueTeamTLSServer(t)
	defer first.Close()
	second := newUniqueTeamTLSServer(t)
	defer second.Close()

	firstDER := first.Certificate().Raw
	client, err := newPinnedTeamClient(second.URL, firstDER, certificateFingerprint(firstDER))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(second.URL); err == nil {
		t.Fatal("request unexpectedly accepted a changed certificate")
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
	probe, _, err := svc.ProbeCertificate(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(source.URL, "user", "password", probe.Fingerprint); err == nil {
		t.Fatal("Login unexpectedly followed a cross-origin redirect")
	}
	if targetHits.Load() != 0 {
		t.Fatalf("redirect target received %d credential requests", targetHits.Load())
	}
}

func TestLegacyTeamSessionDoesNotRefreshWithoutPin(t *testing.T) {
	dir := t.TempDir()
	raw, _ := json.Marshal(teamPersist{ServerURL: "https://example.com", RefreshToken: "secret-refresh"})
	if err := os.WriteFile(filepath.Join(dir, "team-session.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	svc := NewTeamService(dir)
	svc.mu.RLock()
	defer svc.mu.RUnlock()
	if svc.refresh != "" || svc.client != nil || svc.fingerprint != "" {
		t.Fatal("legacy unpinned session retained request credentials")
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
