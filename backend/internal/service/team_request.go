package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// do performs an authenticated request to the remote server, transparently
// refreshing the access token once on 401. Returns the raw body and status.
func (t *TeamService) do(method, path string, payload any) ([]byte, int, error) {
	if !t.LoggedIn() {
		return nil, 0, ErrNotLoggedIn
	}
	send := func() (*http.Response, error) {
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
			return nil, err
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+access)
		return t.client.Do(req)
	}

	resp, err := send()
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		if err := t.doRefresh(); err != nil {
			return nil, http.StatusUnauthorized, ErrNotLoggedIn
		}
		resp, err = send()
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

// RemoteVersion fetches the current glossary version from the server. Works
// authenticated (logged in) or via the public endpoint (readonly mode).
func (t *TeamService) RemoteVersion() (int, error) {
	var body []byte
	var status int
	var err error
	if t.LoggedIn() {
		body, status, err = t.do(http.MethodGet, "/api/glossary/version", nil)
	} else if t.Connected() {
		body, status, err = t.getPublic("/api/glossary/version")
	} else {
		return 0, ErrNotLoggedIn
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

// FetchExport pulls the full authoritative GlossaryData (raw JSON bytes).
// Works authenticated or via the public endpoint (readonly mode).
func (t *TeamService) FetchExport() ([]byte, error) {
	var body []byte
	var status int
	var err error
	if t.LoggedIn() {
		body, status, err = t.do(http.MethodGet, "/api/glossary/export", nil)
	} else if t.Connected() {
		body, status, err = t.getPublic("/api/glossary/export")
	} else {
		return nil, ErrNotLoggedIn
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
