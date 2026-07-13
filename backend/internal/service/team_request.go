package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// teamRefreshMu serializes token refreshes. The server rotates refresh tokens
// (each refresh invalidates the previous one), so concurrent 401s must not each
// POST the same soon-stale refresh token: the loser's stale-token rejection
// would clear the very session the winner just renewed. Package-level because
// there is a single TeamService; if there were ever more, sharing this only
// over-serializes their refreshes, it never corrupts state.
var teamRefreshMu sync.Mutex

// do performs an authenticated request to the remote server, transparently
// refreshing the access token once on 401. Returns the raw body and status.
func (t *TeamService) do(method, path string, payload any) ([]byte, int, error) {
	if !t.LoggedIn() {
		return nil, 0, ErrNotLoggedIn
	}
	// send issues the request and reports which access token it used, so the
	// 401 path can tell whether a concurrent goroutine already rotated it.
	send := func() (*http.Response, string, error) {
		t.mu.RLock()
		url, access := t.serverURL+path, t.access
		t.mu.RUnlock()
		var rdr io.Reader
		if payload != nil {
			b, _ := json.Marshal(payload)
			rdr = bytes.NewReader(b)
		}
		req, err := http.NewRequest(method, url, rdr)
		if err != nil {
			return nil, access, err
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+access)
		resp, err := t.client.Do(req)
		return resp, access, err
	}

	resp, usedAccess, err := send()
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		// Serialize the refresh so rotating refresh tokens can't race (see
		// teamRefreshMu). Once we hold the lock, re-check whether another
		// goroutine already refreshed the access token we used; if so, skip
		// straight to the retry rather than POSTing our now-stale token.
		teamRefreshMu.Lock()
		t.mu.RLock()
		alreadyRefreshed := t.access != usedAccess
		t.mu.RUnlock()
		if !alreadyRefreshed {
			if err := t.doRefresh(); err != nil {
				teamRefreshMu.Unlock()
				return nil, http.StatusUnauthorized, ErrNotLoggedIn
			}
		}
		teamRefreshMu.Unlock()
		resp, _, err = send()
		if err != nil {
			return nil, 0, err
		}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return body, resp.StatusCode, nil
}

// remoteErr extracts an {"error":...} message from a non-2xx body.
func remoteErr(body []byte, status int) error {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return fmt.Errorf("%s", e.Error)
	}
	return fmt.Errorf("remote returned HTTP %d", status)
}

// getPublic performs an unauthenticated GET against the (public) server path.
// Used for no-login readonly mode.
func (t *TeamService) getPublic(path string) ([]byte, int, error) {
	t.mu.RLock()
	url := t.serverURL + path
	t.mu.RUnlock()
	resp, err := t.client.Get(url)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return body, resp.StatusCode, nil
}

// snapshot returns the CDN snapshot base for the current serverURL, discovering
// it lazily on first read (or after switching servers) and caching it for the
// connection's lifetime. Empty means "no CDN" (old server, unreachable, or a
// non-JSON/empty config): callers fall back to the direct server endpoints.
func (t *TeamService) snapshot() string {
	t.mu.RLock()
	server, base, forURL := t.serverURL, t.snapshotBase, t.snapshotBaseFor
	t.mu.RUnlock()
	if server == "" {
		return ""
	}
	if forURL == server {
		// 已针对当前服务器发现过（base 可能是空串，代表老服务器，同样不再重探）。
		return base
	}
	// 首次或切服务器：向服务器发现 CDN 基址。config 是幂等 GET，无令牌轮换风险，
	// 并发多次探测顶多多打一两个请求，无需像刷新那样串行化。
	discovered := t.discoverSnapshotBase(server)
	t.mu.Lock()
	defer t.mu.Unlock()
	// 双检：期间 serverURL 若被切换/清空，本次结果作废，交给下次读路径重探。
	if t.serverURL == server {
		t.snapshotBase, t.snapshotBaseFor = discovered, server
		return discovered
	}
	if t.snapshotBaseFor == t.serverURL {
		return t.snapshotBase
	}
	return ""
}

// discoverSnapshotBase asks the team server where its CDN snapshot lives via the
// public GET /api/config. Returns "" for old servers (404), unreachable servers,
// or an unparseable/empty payload — callers then fall back to direct endpoints.
// Goes through t.client because the team server itself uses a self-signed cert.
func (t *TeamService) discoverSnapshotBase(server string) string {
	resp, err := t.client.Get(server + "/api/config")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var c struct {
		SnapshotBase string `json:"snapshotBase"`
	}
	if json.Unmarshal(raw, &c) != nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(c.SnapshotBase), "/")
}

// getCDN performs a clean GET against the public CDN (real certificate, default
// cdnClient — never the self-signed-tolerant t.client). It deliberately sends no
// If-None-Match: the CDN serves gzip variants whose ETag differs from the identity
// ETag, so a conditional request would mismatch and defeat caching. Freshness rides
// on the object's short CDN TTL (version.json) plus the ?v= cache key (export.json).
func (t *TeamService) getCDN(url string) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := t.cdnClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return body, resp.StatusCode, nil
}

// RemoteVersion fetches the current glossary version. Prefers the CDN snapshot
// probe (version.json, no cache-buster, no conditional request — rides its short
// TTL) and falls back to the direct server endpoint (authenticated when logged in,
// public in readonly mode) when there's no CDN or the CDN read fails.
func (t *TeamService) RemoteVersion() (int, error) {
	if !t.Connected() {
		return 0, ErrNotLoggedIn
	}
	if base := t.snapshot(); base != "" {
		if body, status, err := t.getCDN(base + "/version.json"); err == nil && status == http.StatusOK {
			// version 用指针探测字段是否真的存在：CDN 可能对故障返回 200 的软错误
			// JSON（如 {"error":...}），缺 version 键不能当作版本 0 采信——否则新
			// 连接会被 0==0「已最新」钉住静默停摆。这种 200 同样落直连兜底。
			var v struct {
				Version *int `json:"version"`
			}
			if json.Unmarshal(body, &v) == nil && v.Version != nil {
				return *v.Version, nil
			}
		}
		// CDN 失败/解析不出 → 落到服务器直连回退。
	}
	return t.remoteVersionDirect()
}

// remoteVersionDirect reads the version straight from the server (the pre-CDN path).
func (t *TeamService) remoteVersionDirect() (int, error) {
	var body []byte
	var status int
	var err error
	if t.LoggedIn() {
		body, status, err = t.do(http.MethodGet, "/api/glossary/version", nil)
	} else {
		body, status, err = t.getPublic("/api/glossary/version")
	}
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return 0, remoteErr(body, status)
	}
	var v struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return 0, err
	}
	return v.Version, nil
}

// FetchExport pulls the full authoritative GlossaryData (raw JSON bytes) for the
// given version. Prefers the CDN snapshot (export.json?v=version, where ?v= is the
// per-version cache key so a bumped version busts the CDN cache) and falls back to
// the direct server endpoint. version is the value the caller already learned from
// RemoteVersion, threaded through so the CDN URL carries the right cache key.
func (t *TeamService) FetchExport(version int) ([]byte, error) {
	if !t.Connected() {
		return nil, ErrNotLoggedIn
	}
	if base := t.snapshot(); base != "" {
		url := fmt.Sprintf("%s/export.json?v=%d", base, version)
		if body, status, err := t.getCDN(url); err == nil && status == http.StatusOK {
			// 与 RemoteVersion 同理：200 不代表 body 可信（CDN 软错误页/截断对象
			// 也可能是 200）。快照必须解析成带 entries 数组的 GlossaryData 形状
			// （服务器导出恒有 entries:[]，绝不缺键）才采信，否则落直连兜底——
			// 不校验的话坏 body 会一路传到 MergeImport 才炸，且绝不会触发回退。
			var probe struct {
				Entries []json.RawMessage `json:"entries"`
			}
			if json.Unmarshal(body, &probe) == nil && probe.Entries != nil {
				return body, nil
			}
		}
		// CDN 失败/body 不可信 → 落到服务器直连回退。
	}
	return t.fetchExportDirect()
}

// fetchExportDirect reads the full export straight from the server (the pre-CDN path).
func (t *TeamService) fetchExportDirect() ([]byte, error) {
	var body []byte
	var status int
	var err error
	if t.LoggedIn() {
		body, status, err = t.do(http.MethodGet, "/api/glossary/export", nil)
	} else {
		body, status, err = t.getPublic("/api/glossary/export")
	}
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, remoteErr(body, status)
	}
	return body, nil
}

// Proxy forwards an arbitrary authenticated call and returns body+status, so
// handlers for proposals/admin can pass through transparently.
func (t *TeamService) Proxy(method, path string, payload any) ([]byte, int, error) {
	body, status, err := t.do(method, path, payload)
	if err != nil {
		return nil, 0, err
	}
	return body, status, nil
}
