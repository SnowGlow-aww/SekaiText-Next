package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrNotLoggedIn is returned when a team request is attempted without a session.
var ErrNotLoggedIn = errors.New("not logged in to a team server")

type tokenResp struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	User         *TeamUser `json:"user"`
	Error        string    `json:"error"`
}

// Login authenticates against serverURL and stores the session.
func (t *TeamService) Login(serverURL, username, password string) (*TeamUser, error) {
	serverURL = strings.TrimRight(strings.TrimSpace(serverURL), "/")
	if serverURL == "" {
		return nil, errors.New("missing server URL")
	}
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, err := t.client.Post(serverURL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("connect failed: %w", err)
	}
	defer resp.Body.Close()
	var tr tokenResp
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(raw, &tr)
	if resp.StatusCode != http.StatusOK || tr.AccessToken == "" {
		if tr.Error != "" {
			return nil, errors.New(tr.Error)
		}
		return nil, fmt.Errorf("login failed (HTTP %d)", resp.StatusCode)
	}
	t.mu.Lock()
	t.serverURL, t.access, t.refresh, t.user = serverURL, tr.AccessToken, tr.RefreshToken, tr.User
	t.mu.Unlock()
	t.persist()
	return tr.User, nil
}

// Connect sets the server URL for no-login readonly mode (verifies reachability
// via the public version endpoint) and persists it.
func (t *TeamService) Connect(serverURL string) error {
	serverURL = strings.TrimRight(strings.TrimSpace(serverURL), "/")
	if serverURL == "" {
		return errors.New("missing server URL")
	}
	resp, err := t.client.Get(serverURL + "/api/glossary/version")
	if err != nil {
		return fmt.Errorf("connect failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server unreachable (HTTP %d)", resp.StatusCode)
	}
	t.mu.Lock()
	t.serverURL = serverURL
	t.mu.Unlock()
	t.persist()
	return nil
}

// doRefresh exchanges the refresh token for a new access token.
func (t *TeamService) doRefresh() error {
	t.mu.RLock()
	url, refresh := t.serverURL, t.refresh
	t.mu.RUnlock()
	if url == "" || refresh == "" {
		return ErrNotLoggedIn
	}
	body, _ := json.Marshal(map[string]string{"refreshToken": refresh})
	resp, err := t.client.Post(url+"/api/auth/refresh", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var tr tokenResp
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(raw, &tr)
	if resp.StatusCode != http.StatusOK || tr.AccessToken == "" {
		return errors.New("refresh rejected")
	}
	t.mu.Lock()
	t.access, t.refresh = tr.AccessToken, tr.RefreshToken
	if tr.User != nil {
		t.user = tr.User
	}
	t.mu.Unlock()
	t.persist()
	return nil
}

// Logout clears the auth tokens and user but keeps the serverURL so the app
// drops to no-login readonly mode (still synced) rather than fully disconnecting.
func (t *TeamService) Logout() {
	t.mu.Lock()
	t.access, t.refresh, t.user = "", "", nil
	t.mu.Unlock()
	t.persist()
}

// Disconnect fully clears the session including the server URL (back to pure local).
func (t *TeamService) Disconnect() {
	t.mu.Lock()
	t.serverURL, t.access, t.refresh, t.user = "", "", "", nil
	t.mu.Unlock()
	t.persist()
}

// Status reports the current session (nil user = not logged in).
func (t *TeamService) Status() (string, *TeamUser) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.serverURL, t.user
}

// LoggedIn reports whether there is an active access token.
func (t *TeamService) LoggedIn() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.access != ""
}

// Connected reports whether a server URL is set (logged in or readonly).
func (t *TeamService) Connected() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.serverURL != ""
}
