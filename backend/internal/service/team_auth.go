package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// ErrNotLoggedIn is returned when a team request is attempted without a session.
var ErrNotLoggedIn = errors.New("not logged in to a team server")
var ErrStaleTeamSession = errors.New("team server session changed while request was in flight")

type tokenResp struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	User         *TeamUser `json:"user"`
}

// Login authenticates against the selected team server.
func (t *TeamService) Login(serverURL, username, password string) (*TeamUser, error) {
	t.sessionMu.Lock()
	t.mu.Lock()
	if t.invalidated {
		t.mu.Unlock()
		t.sessionMu.Unlock()
		return nil, ErrStaleTeamSession
	}
	t.sessionEpoch++
	epoch := t.sessionEpoch
	t.mu.Unlock()
	t.sessionMu.Unlock()
	serverURL, client, err := t.newHTTPClient(serverURL)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, err := client.Post(serverURL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("connect failed: %w", err)
	}
	defer resp.Body.Close()
	raw, readErr := readBoundedResponse(resp.Body, maxTeamConfigBytes)
	if resp.StatusCode != http.StatusOK {
		return nil, remoteErr("login", resp.StatusCode)
	}
	var tr tokenResp
	if readErr != nil || json.Unmarshal(raw, &tr) != nil || tr.AccessToken == "" || tr.RefreshToken == "" {
		return nil, errors.New("login failed: invalid authentication response")
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
	if t.invalidated {
		t.mu.Unlock()
		t.sessionMu.Unlock()
		return ErrStaleTeamSession
	}
	t.sessionEpoch++
	epoch := t.sessionEpoch
	t.mu.Unlock()
	t.sessionMu.Unlock()
	serverURL, client, err := t.newHTTPClient(serverURL)
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

// RefreshSessionIfNeeded lazily exchanges a restored refresh token for an
// access token. It is safe for concurrent status, sync, and authenticated calls:
// only one exchange runs, and waiters reuse the access token it produced.
// Readonly connections (no refresh token) are already usable and return nil.
func (t *TeamService) RefreshSessionIfNeeded() error {
	t.mu.RLock()
	if t.access != "" || t.refresh == "" {
		t.mu.RUnlock()
		return nil
	}
	epoch := t.sessionEpoch
	t.mu.RUnlock()

	teamRefreshMu.Lock()
	defer teamRefreshMu.Unlock()

	t.mu.RLock()
	if t.sessionEpoch != epoch {
		t.mu.RUnlock()
		return ErrStaleTeamSession
	}
	if t.access != "" || t.refresh == "" {
		t.mu.RUnlock()
		return nil
	}
	t.mu.RUnlock()
	return t.doRefreshFor(epoch)
}

// doRefreshFor exchanges the current refresh token for a new access token,
// provided the captured session generation is still active.
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
		return errors.New("refresh failed: request could not be completed")
	}
	defer resp.Body.Close()
	raw, readErr := readBoundedResponse(resp.Body, maxTeamConfigBytes)
	var tr tokenResp
	parseErr := json.Unmarshal(raw, &tr)
	if resp.StatusCode == http.StatusOK && readErr == nil && parseErr == nil &&
		tr.AccessToken != "" && tr.RefreshToken != "" {
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
		return errors.Join(remoteErr("refresh", resp.StatusCode), t.persist())
	}
	// Transient failures keep the refresh token intact so a later request can
	// retry instead of forcing a re-login. The server-provided error body is
	// deliberately ignored because this error crosses frontend boundaries.
	if resp.StatusCode != http.StatusOK {
		return remoteErr("refresh", resp.StatusCode)
	}
	return errors.New("refresh failed: invalid authentication response")
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
