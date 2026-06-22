package service

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TeamSession holds the persisted bits of a team-mode login (server URL +
// refresh token) written to dataDir so the app can re-auth on startup.
type teamPersist struct {
	ServerURL    string `json:"serverUrl"`
	RefreshToken string `json:"refreshToken"`
}

// TeamUser mirrors the glossary-server's user object returned on login.
type TeamUser struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
	Status      string `json:"status"`
}

// TeamService proxies the remote glossary-server: it owns the access/refresh
// tokens (in memory), uses a TLS-skip client (the server uses a self-signed
// cert), and exposes login/refresh + authenticated request helpers. The
// frontend only ever talks to the local backend, never the remote directly
// (a webview can't accept the self-signed cert).
type TeamService struct {
	dataDir string
	client  *http.Client

	mu        sync.RWMutex
	serverURL string
	access    string
	refresh   string
	user      *TeamUser
	lastVer   int
}

// LastSyncedVersion returns the glossary version last merged locally.
func (t *TeamService) LastSyncedVersion() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastVer
}

// SetLastSyncedVersion records the version after a successful merge.
func (t *TeamService) SetLastSyncedVersion(v int) {
	t.mu.Lock()
	t.lastVer = v
	t.mu.Unlock()
}

// NewTeamService creates the service and attempts to restore a prior session.
func NewTeamService(dataDir string) *TeamService {
	t := &TeamService{
		dataDir: dataDir,
		client: &http.Client{
			Timeout:   20 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		},
	}
	t.restore()
	return t
}

func (t *TeamService) sessionPath() string {
	return filepath.Join(t.dataDir, "team-session.json")
}

func (t *TeamService) restore() {
	b, err := os.ReadFile(t.sessionPath())
	if err != nil {
		return
	}
	var p teamPersist
	if json.Unmarshal(b, &p) != nil || p.ServerURL == "" {
		return
	}
	t.mu.Lock()
	t.serverURL = strings.TrimRight(p.ServerURL, "/")
	t.refresh = p.RefreshToken
	t.mu.Unlock()
	// Best-effort: exchange refresh for a fresh access token. On failure we keep
	// the serverURL so the app can still operate in no-login readonly mode.
	if p.RefreshToken != "" {
		if err := t.doRefresh(); err != nil {
			t.mu.Lock()
			t.access, t.refresh, t.user = "", "", nil
			t.mu.Unlock()
		}
	}
}

func (t *TeamService) persist() {
	t.mu.RLock()
	p := teamPersist{ServerURL: t.serverURL, RefreshToken: t.refresh}
	t.mu.RUnlock()
	if p.ServerURL == "" {
		os.Remove(t.sessionPath())
		return
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	_ = os.WriteFile(t.sessionPath(), b, 0o600)
}
