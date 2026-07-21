package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ErrNotLoggedIn is returned when a team request is attempted without a session.
var ErrNotLoggedIn = errors.New("not logged in to a team server")
var ErrStaleTeamSession = errors.New("team server session changed while request was in flight")

type tokenResp struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	User         *TeamUser `json:"user"`
	Error        string    `json:"error"`
}

// Login authenticates against the selected team server.
func (t *TeamService) Login(serverURL, username, password string) (*TeamUser, error) {
	t.sessionMu.Lock()
	t.mu.Lock()
	t.sessionEpoch++
	epoch := t.sessionEpoch
	t.mu.Unlock()
	t.sessionMu.Unlock()
	serverURL, client, err := newTeamHTTPClient(serverURL)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, err := client.Post(serverURL+"/api/auth/login", "application/json", bytes.NewReader(body))
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
	t.sessionMu.Lock()
	t.mu.Lock()
	if t.sessionEpoch != epoch {
		t.mu.Unlock()
		t.sessionMu.Unlock()
		return nil, ErrStaleTeamSession
	}
	t.resetServerCachesLocked(serverURL)
	t.serverURL, t.access, t.refresh, t.user = serverURL, tr.AccessToken, tr.RefreshToken, tr.User
	t.client = client
	t.mu.Unlock()
	t.sessionMu.Unlock()
	if err := t.persist(); err != nil {
		return tr.User, err
	}
	return tr.User, nil
}

// Connect sets the server URL for no-login readonly mode.
func (t *TeamService) Connect(serverURL string) error {
	t.sessionMu.Lock()
	t.mu.Lock()
	t.sessionEpoch++
	epoch := t.sessionEpoch
	t.mu.Unlock()
	t.sessionMu.Unlock()
	serverURL, client, err := newTeamHTTPClient(serverURL)
	if err != nil {
		return err
	}
	resp, err := client.Get(serverURL + "/api/glossary/version")
	if err != nil {
		return fmt.Errorf("connect failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server unreachable (HTTP %d)", resp.StatusCode)
	}
	t.sessionMu.Lock()
	t.mu.Lock()
	if t.sessionEpoch != epoch {
		t.mu.Unlock()
		t.sessionMu.Unlock()
		return ErrStaleTeamSession
	}
	t.resetServerCachesLocked(serverURL)
	t.serverURL = serverURL
	t.access, t.refresh, t.user = "", "", nil
	t.client = client
	t.mu.Unlock()
	t.sessionMu.Unlock()
	return t.persist()
}

// doRefresh exchanges the refresh token for a new access token.
func (t *TeamService) doRefresh() error {
	t.mu.RLock()
	epoch := t.sessionEpoch
	t.mu.RUnlock()
	return t.doRefreshFor(epoch)
}

func (t *TeamService) doRefreshFor(epoch uint64) error {
	t.mu.RLock()
	if t.sessionEpoch != epoch {
		t.mu.RUnlock()
		return ErrStaleTeamSession
	}
	url, refresh, client := t.serverURL, t.refresh, t.client
	t.mu.RUnlock()
	if url == "" || refresh == "" || client == nil {
		return ErrNotLoggedIn
	}
	body, _ := json.Marshal(map[string]string{"refreshToken": refresh})
	resp, err := client.Post(url+"/api/auth/refresh", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var tr tokenResp
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(raw, &tr)
	if resp.StatusCode == http.StatusOK && tr.AccessToken != "" {
		t.mu.Lock()
		if t.sessionEpoch != epoch || t.serverURL != url || t.client != client {
			t.mu.Unlock()
			return ErrStaleTeamSession
		}
		t.access, t.refresh = tr.AccessToken, tr.RefreshToken
		if tr.User != nil {
			t.user = tr.User
		}
		t.mu.Unlock()
		return t.persist()
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// Refresh was definitively rejected (token expired/revoked, account
		// disabled, server key rotated): clear the session so LoggedIn() goes
		// false and the app drops to no-login readonly instead of looping on
		// 401s. Keep serverURL (mirrors Logout) so readonly stays synced.
		t.mu.Lock()
		if t.sessionEpoch != epoch || t.serverURL != url || t.client != client {
			t.mu.Unlock()
			return ErrStaleTeamSession
		}
		t.access, t.refresh, t.user = "", "", nil
		t.mu.Unlock()
		persistErr := t.persist()
		if tr.Error != "" {
			return errors.Join(errors.New(tr.Error), persistErr)
		}
		return errors.Join(errors.New("refresh rejected"), persistErr)
	}
	// Transient failure (5xx/429/gateway blip, or a malformed 200 with no token):
	// the refresh token was NOT rejected, so keep the session intact and just
	// return an error. A later request can retry instead of forcing a re-login.
	if tr.Error != "" {
		return errors.New(tr.Error)
	}
	return fmt.Errorf("refresh failed (HTTP %d)", resp.StatusCode)
}

// Logout clears the auth tokens and user but keeps the serverURL so the app
// drops to no-login readonly mode (still synced) rather than fully disconnecting.
func (t *TeamService) Logout() error {
	t.sessionMu.Lock()
	t.mu.Lock()
	t.sessionEpoch++
	t.access, t.refresh, t.user = "", "", nil
	t.mu.Unlock()
	t.sessionMu.Unlock()
	return t.persist()
}

// Disconnect fully clears the session including the server URL (back to pure local).
func (t *TeamService) Disconnect() error {
	t.sessionMu.Lock()
	t.mu.Lock()
	t.sessionEpoch++
	t.resetServerCachesLocked("")
	t.serverURL, t.access, t.refresh, t.user = "", "", "", nil
	t.client = nil
	t.mu.Unlock()
	t.sessionMu.Unlock()
	return t.persist()
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
