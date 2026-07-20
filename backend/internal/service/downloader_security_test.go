package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testDownloader(t *testing.T, server *httptest.Server) *Downloader {
	t.Helper()
	d := NewDownloader(t.TempDir())
	d.allowedHosts = map[string]struct{}{"127.0.0.1": {}}
	d.client = server.Client()
	return d
}

func TestDownloaderURLPolicyAndCanonicalization(t *testing.T) {
	d := NewDownloader(t.TempDir())
	for _, raw := range []string{
		"http://storage.sekai.best/story.json",
		"https://storage.sekai.best.attacker.invalid/story.json",
		"https://user:pass@storage.sekai.best/story.json",
		"https://127.0.0.1/story.json",
	} {
		if _, err := d.validateURL(raw); err == nil {
			t.Errorf("validateURL(%q) unexpectedly succeeded", raw)
		}
	}

	a, err := canonicalHTTPSURL("https://STORAGE.SEKAI.BEST/a/../story.json?b=2&a=1#fragment")
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonicalHTTPSURL("https://storage.sekai.best/story.json?a=1&b=2")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("canonical URLs differ:\n%s\n%s", a, b)
	}
	c, err := canonicalHTTPSURL("https://storage.sekai.best:443/story.json?a=1&b=2")
	if err != nil {
		t.Fatal(err)
	}
	if b != c {
		t.Fatalf("default HTTPS port was not normalized:\n%s\n%s", b, c)
	}
}

func TestDownloaderTransportPinsValidatedPublicAddress(t *testing.T) {
	wantDialErr := errors.New("dial stopped")
	var dialed string
	transport := newPublicSnapshotTransport(
		func(_ context.Context, host string) ([]net.IPAddr, error) {
			if host != "storage.sekai.best" {
				t.Fatalf("lookup host = %q", host)
			}
			return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
		},
		func(_ context.Context, _, address string) (net.Conn, error) {
			dialed = address
			return nil, wantDialErr
		},
	)
	_, err := transport.DialContext(context.Background(), "tcp", "storage.sekai.best:443")
	if !errors.Is(err, wantDialErr) {
		t.Fatalf("dial error = %v, want %v", err, wantDialErr)
	}
	if dialed != "8.8.8.8:443" {
		t.Fatalf("transport re-resolved or dialed the hostname: %q", dialed)
	}
}

func TestDownloaderTransportRejectsPrivateResolutionBeforeDial(t *testing.T) {
	tests := []struct {
		name string
		ips  []net.IPAddr
	}{
		{name: "loopback", ips: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}},
		{name: "private", ips: []net.IPAddr{{IP: net.ParseIP("10.0.0.8")}}},
		{name: "link-local", ips: []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}},
		{name: "mixed answer", ips: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("127.0.0.1")}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dialed := false
			transport := newPublicSnapshotTransport(
				func(context.Context, string) ([]net.IPAddr, error) { return tc.ips, nil },
				func(context.Context, string, string) (net.Conn, error) {
					dialed = true
					return nil, errors.New("must not dial")
				},
			)
			if _, err := transport.DialContext(context.Background(), "tcp", "storage.sekai.best:443"); err == nil {
				t.Fatal("private DNS answer was accepted")
			}
			if dialed {
				t.Fatal("transport dialed before rejecting the unsafe DNS answer set")
			}
		})
	}
}

func TestStoryCacheIsolatedByCanonicalURLAndIgnoresLegacyFile(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = fmt.Fprintf(w, `{"path":%q}`, r.URL.Path)
	}))
	defer server.Close()
	d := testDownloader(t, server)

	legacy := filepath.Join(d.dataDir, "same.json")
	if err := os.WriteFile(legacy, []byte(`{"legacy":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	first, err := d.DownloadJSON(server.URL+"/source-a", "same.json")
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.DownloadJSON(server.URL+"/source-b", "same.json")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || first == legacy || second == legacy {
		t.Fatalf("cache paths are not URL-isolated: legacy=%q first=%q second=%q", legacy, first, second)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("upstream hits = %d, want 2", got)
	}
	if _, err := d.DownloadJSON(server.URL+"/source-a", "same.json"); err != nil {
		t.Fatal(err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("valid cache did not hit: upstream hits = %d", got)
	}
}

func TestStoryCacheRejectsCorruptAndZeroByteEntries(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	d := testDownloader(t, server)
	path, err := d.DownloadJSON(server.URL+"/story", "story.json")
	if err != nil {
		t.Fatal(err)
	}

	for _, bad := range [][]byte{nil, []byte(`{"truncated":`)} {
		if err := os.WriteFile(path, bad, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := d.DownloadJSON(server.URL+"/story", "story.json"); err != nil {
			t.Fatal(err)
		}
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("upstream hits = %d, want 3 after repairing both bad entries", got)
	}
}

func TestDownloadJSONToDirDoesNotReuseFileFromAnotherSource(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = fmt.Fprintf(w, `{"path":%q}`, r.URL.Path)
	}))
	defer server.Close()
	d := testDownloader(t, server)
	dir := t.TempDir()

	if _, err := d.DownloadJSONToDir(server.URL+"/source-a", dir, "story.json", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DownloadJSONToDir(server.URL+"/source-a", dir, "story.json", nil); err != nil {
		t.Fatal(err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("same-source cache hits = %d, want 1", got)
	}
	if _, err := d.DownloadJSONToDir(server.URL+"/source-b", dir, "story.json", nil); err != nil {
		t.Fatal(err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("source change upstream hits = %d, want 2", got)
	}
	body, err := os.ReadFile(filepath.Join(dir, "story.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "/source-b") {
		t.Fatalf("output retained another source: %s", body)
	}
	// A valid-JSON local edit must not be accepted under the old source marker.
	if err := os.WriteFile(filepath.Join(dir, "story.json"), []byte(`{"tampered":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DownloadJSONToDir(server.URL+"/source-b", dir, "story.json", nil); err != nil {
		t.Fatal(err)
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("content digest mismatch did not refetch: hits=%d", got)
	}
}

func TestDownloadJSONToDirLocksSymlinkAliases(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			time.Sleep(100 * time.Millisecond)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	d := testDownloader(t, server)
	dir := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, outputDir := range []string{dir, alias} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := d.DownloadJSONToDir(server.URL+"/story", outputDir, "story.json", nil)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("physical output path was not single-flighted: hits=%d", got)
	}
}

func TestDownloadJSONToDirPropagatesMarkerWriteFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	d := testDownloader(t, server)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".sekaitext-cache"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DownloadJSONToDir(server.URL+"/story", dir, "story.json", nil); err == nil || !strings.Contains(err.Error(), "cache marker") {
		t.Fatalf("marker failure was ignored: %v", err)
	}
}

func TestDownloaderRejectsOversizeAndInvalidJSON(t *testing.T) {
	tests := []struct {
		name string
		body string
		max  int64
	}{
		{"oversize", `{"value":"` + strings.Repeat("x", 32) + `"}`, 16},
		{"invalid json", `not-json`, 64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			d := testDownloader(t, server)
			d.maxResponseBytes = tt.max
			if _, err := d.DownloadJSON(server.URL+"/story", "story.json"); err == nil {
				t.Fatal("DownloadJSON unexpectedly accepted unsafe response")
			}
		})
	}
}
