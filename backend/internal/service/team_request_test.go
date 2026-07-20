package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// newReadonlyTeam builds a connected-but-not-logged-in TeamService pointed at
// serverURL (readonly mode), skipping the session-restore network path since the
// temp dataDir has no session file.
func newReadonlyTeam(t *testing.T, serverURL string) *TeamService {
	t.Helper()
	svc := NewTeamService(t.TempDir())
	svc.mu.Lock()
	svc.serverURL = serverURL
	// These request-path tests use local plain-HTTP fixtures. Production can only
	// populate these fields through the HTTPS probe/pin flow.
	svc.fingerprint = strings.Repeat("a", 64)
	svc.client = &http.Client{}
	svc.cdnClient = &http.Client{}
	svc.snapshotURLAllowed = func(string) bool { return true }
	svc.mu.Unlock()
	return svc
}

// TestRemoteVersionAndFetchExportViaCDN: 服务器经 /api/config 暴露 CDN 基址后，
// 版本探测与全量拉取都走 CDN（version.json / export.json?v=N），且不打服务器直连。
func TestRemoteVersionAndFetchExportViaCDN(t *testing.T) {
	var cfgHits, cdnVerHits, cdnExpHits, srvVerHits, srvExpHits int32
	var exportQuery string

	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			t.Errorf("CDN 请求不应带 If-None-Match（gzip 变体 ETag 有坑）")
		}
		switch r.URL.Path {
		case "/version.json":
			atomic.AddInt32(&cdnVerHits, 1)
			if r.URL.RawQuery != "" {
				t.Errorf("version.json 不应带缓存穿透参数, got %q", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"version":42,"publishedAt":"2026-07-13T00:00:00Z"}`)
		case "/export.json":
			atomic.AddInt32(&cdnExpHits, 1)
			exportQuery = r.URL.RawQuery
			_, _ = io.WriteString(w, `{"entries":[{"id":"cdn"}],"appellations":[]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer cdn.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/config":
			atomic.AddInt32(&cfgHits, 1)
			_, _ = io.WriteString(w, `{"snapshotBase":"`+cdn.URL+`"}`)
		case "/api/glossary/version":
			atomic.AddInt32(&srvVerHits, 1)
			_, _ = io.WriteString(w, `{"version":7}`)
		case "/api/glossary/export":
			atomic.AddInt32(&srvExpHits, 1)
			_, _ = io.WriteString(w, `{"entries":[{"id":"srv"}],"appellations":[]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	svc := newReadonlyTeam(t, srv.URL)

	ver, err := svc.RemoteVersion()
	if err != nil {
		t.Fatalf("RemoteVersion: %v", err)
	}
	if ver != 42 {
		t.Fatalf("version = %d, want 42 (CDN)；服务器直连返回的是 7", ver)
	}

	raw, err := svc.FetchExport(ver)
	if err != nil {
		t.Fatalf("FetchExport: %v", err)
	}
	if !strings.Contains(string(raw), `"id":"cdn"`) {
		t.Fatalf("export 未走 CDN, got %s", raw)
	}
	if exportQuery != "v=42" {
		t.Fatalf("export 缓存键 = %q, want v=42", exportQuery)
	}
	// config 只探一次（结果缓存在连接生命周期内）。
	if got := atomic.LoadInt32(&cfgHits); got != 1 {
		t.Fatalf("config 探测次数 = %d, want 1（应缓存）", got)
	}
	if v, e := atomic.LoadInt32(&srvVerHits), atomic.LoadInt32(&srvExpHits); v != 0 || e != 0 {
		t.Fatalf("不该打服务器直连: version=%d export=%d", v, e)
	}
	if v, e := atomic.LoadInt32(&cdnVerHits), atomic.LoadInt32(&cdnExpHits); v != 1 || e != 1 {
		t.Fatalf("CDN 命中异常: version=%d export=%d", v, e)
	}
}

// TestConfigNotFoundFallsBackDirect: 老服务器（/api/config 返回 404）→ snapshotBase
// 为空并缓存，版本探测与拉取都回退服务器直连，且不再反复探测 config。
func TestConfigNotFoundFallsBackDirect(t *testing.T) {
	var cfgHits, srvVerHits, srvExpHits int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/config":
			atomic.AddInt32(&cfgHits, 1)
			w.WriteHeader(http.StatusNotFound)
		case "/api/glossary/version":
			atomic.AddInt32(&srvVerHits, 1)
			_, _ = io.WriteString(w, `{"version":7}`)
		case "/api/glossary/export":
			atomic.AddInt32(&srvExpHits, 1)
			_, _ = io.WriteString(w, `{"entries":[{"id":"srv"}],"appellations":[]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	svc := newReadonlyTeam(t, srv.URL)

	ver, err := svc.RemoteVersion()
	if err != nil {
		t.Fatalf("RemoteVersion: %v", err)
	}
	if ver != 7 {
		t.Fatalf("version = %d, want 7（服务器直连回退）", ver)
	}
	raw, err := svc.FetchExport(ver)
	if err != nil {
		t.Fatalf("FetchExport: %v", err)
	}
	if !strings.Contains(string(raw), `"id":"srv"`) {
		t.Fatalf("export 未走服务器直连, got %s", raw)
	}
	// 空 snapshotBase 应被缓存：两次读路径只探一次 config。
	if got := atomic.LoadInt32(&cfgHits); got != 1 {
		t.Fatalf("config 探测次数 = %d, want 1（空结果也应缓存）", got)
	}
	if v, e := atomic.LoadInt32(&srvVerHits), atomic.LoadInt32(&srvExpHits); v != 1 || e != 1 {
		t.Fatalf("服务器直连命中异常: version=%d export=%d", v, e)
	}
	svc.mu.RLock()
	base, forURL := svc.snapshotBase, svc.snapshotBaseFor
	svc.mu.RUnlock()
	if base != "" || forURL != srv.URL {
		t.Fatalf("发现结果缓存异常: base=%q for=%q", base, forURL)
	}
}

// TestCDNErrorFallsBackDirect: config 给出了 CDN 基址，但 CDN 读失败（5xx）→ 版本
// 与拉取都回退服务器直连（CDN 故障兜底）。
func TestCDNErrorFallsBackDirect(t *testing.T) {
	var srvVerHits, srvExpHits int32

	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer cdn.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/config":
			_, _ = io.WriteString(w, `{"snapshotBase":"`+cdn.URL+`"}`)
		case "/api/glossary/version":
			atomic.AddInt32(&srvVerHits, 1)
			_, _ = io.WriteString(w, `{"version":9}`)
		case "/api/glossary/export":
			atomic.AddInt32(&srvExpHits, 1)
			_, _ = io.WriteString(w, `{"entries":[{"id":"srv"}],"appellations":[]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	svc := newReadonlyTeam(t, srv.URL)

	ver, err := svc.RemoteVersion()
	if err != nil {
		t.Fatalf("RemoteVersion: %v", err)
	}
	if ver != 9 {
		t.Fatalf("version = %d, want 9（CDN 故障回退直连）", ver)
	}
	raw, err := svc.FetchExport(ver)
	if err != nil {
		t.Fatalf("FetchExport: %v", err)
	}
	if !strings.Contains(string(raw), `"id":"srv"`) {
		t.Fatalf("export 未回退服务器直连, got %s", raw)
	}
	if v, e := atomic.LoadInt32(&srvVerHits), atomic.LoadInt32(&srvExpHits); v != 1 || e != 1 {
		t.Fatalf("服务器直连命中异常: version=%d export=%d", v, e)
	}
}

// TestCDNSoftErrorFallsBackDirect: CDN 返回 200 但 body 不可信（version.json 缺
// version 键 / export.json 不是 GlossaryData 形状，如 ESA 缓存的软错误页）→ 两条
// 读路径都必须回退服务器直连，而不是把垃圾当数据（version 被当 0 会钉死同步）。
func TestCDNSoftErrorFallsBackDirect(t *testing.T) {
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 200 + 合法 JSON，但形状不对：无 version 键、无 entries 数组。
		_, _ = io.WriteString(w, `{"error":"Forbidden"}`)
	}))
	defer cdn.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/config":
			_, _ = io.WriteString(w, `{"snapshotBase":"`+cdn.URL+`"}`)
		case "/api/glossary/version":
			_, _ = io.WriteString(w, `{"version":7}`)
		case "/api/glossary/export":
			_, _ = io.WriteString(w, `{"entries":[{"id":"srv"}],"appellations":[]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	svc := newReadonlyTeam(t, srv.URL)

	ver, err := svc.RemoteVersion()
	if err != nil {
		t.Fatalf("RemoteVersion: %v", err)
	}
	if ver != 7 {
		t.Fatalf("version = %d, want 7（CDN 软错误 200 应回退直连，不得当 0）", ver)
	}
	raw, err := svc.FetchExport(ver)
	if err != nil {
		t.Fatalf("FetchExport: %v", err)
	}
	if !strings.Contains(string(raw), `"id":"srv"`) {
		t.Fatalf("export 未回退服务器直连, got %s", raw)
	}
}

// TestRemoteVersionNotConnected: 未连接任何服务器时返回 ErrNotLoggedIn。
func TestRemoteVersionNotConnected(t *testing.T) {
	svc := NewTeamService(t.TempDir())
	if _, err := svc.RemoteVersion(); err != ErrNotLoggedIn {
		t.Fatalf("RemoteVersion err = %v, want ErrNotLoggedIn", err)
	}
	if _, err := svc.FetchExport(0); err != ErrNotLoggedIn {
		t.Fatalf("FetchExport err = %v, want ErrNotLoggedIn", err)
	}
}
