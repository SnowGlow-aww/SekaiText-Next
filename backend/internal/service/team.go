package service

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"sekaitext/backend/internal/fsutil"
)

// teamPersist holds the persisted team URL and refresh token so the app can
// re-authenticate on startup.
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
	AvatarColor string `json:"avatarColor"`
}

// ErrTeamPersistence identifies failures to durably update the team session.
var ErrTeamPersistence = errors.New("persist team session")

// TeamService proxies the remote glossary-server: it owns the access/refresh
// tokens (in memory) and exposes login/refresh + authenticated request helpers.
// The frontend only ever talks to the local backend, never the remote directly
// (a webview can't accept the self-signed cert).
type TeamService struct {
	dataDir string
	syncDir func(string) error
	// client is scoped to the selected server origin and verifies its certificate.
	// Redirects are restricted to that same origin.
	client *http.Client
	// teamRootCAs is nil in production so Go uses the system trust store. Tests
	// inject a private CA to exercise trusted TLS without changing process trust.
	teamRootCAs *x509.CertPool
	// cdnClient only permits public HTTPS destinations and never carries team
	// credentials. snapshotURLAllowed is a dependency seam for local unit tests;
	// production always uses publicSnapshotURLAllowed.
	cdnClient          *http.Client
	snapshotURLAllowed func(string) bool

	mu           sync.RWMutex
	persistMu    sync.Mutex
	syncMu       sync.Mutex
	sessionMu    sync.Mutex
	sessionEpoch uint64
	invalidated  bool
	serverURL    string
	access       string
	refresh      string
	user         *TeamUser
	lastVer      int
	// snapshotBase 是团队服务器经 GET /api/config 暴露的 CDN 快照基址（形如
	// https://sakimizuki.accr.cc/sekaitext-glossary）；为空表示老服务器或发现失败，
	// 读路径回退服务器直连。snapshotBaseFor 和 snapshotBaseEpoch 记录该值
	// 对应的服务器会话，URL 或会话变化后据此惰性重新发现。
	snapshotBase      string
	snapshotBaseFor   string
	snapshotBaseEpoch uint64
}

type teamSessionIdentity struct {
	epoch     uint64
	serverURL string
}

func (t *TeamService) sessionIdentityCurrentLocked(session teamSessionIdentity) bool {
	return t.sessionEpoch == session.epoch &&
		t.serverURL == session.serverURL
}

func (t *TeamService) sessionIdentityCurrent(session teamSessionIdentity) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.sessionIdentityCurrentLocked(session)
}

// LastSyncedVersion returns the glossary version last merged locally.
func (t *TeamService) LastSyncedVersion() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastVer
}

// SetLastSyncedVersion records the version after a successful merge.
func (t *TeamService) SetLastSyncedVersion(v int) {
	t.sessionMu.Lock()
	defer t.sessionMu.Unlock()
	t.mu.Lock()
	if v > t.lastVer {
		t.lastVer = v
	}
	t.mu.Unlock()
}

func (t *TeamService) resetServerCachesLocked(serverURL string) {
	if serverURL == t.serverURL {
		return
	}
	t.lastVer = 0
	t.snapshotBase, t.snapshotBaseFor, t.snapshotBaseEpoch = "", "", 0
}

// NewTeamService creates the service and best-effort refreshes a prior session.
func NewTeamService(dataDir string) *TeamService {
	return NewTeamServiceWithRootCAs(dataDir, nil)
}

// NewTeamServiceWithRootCAs uses the supplied trust pool instead of the system
// pool. Callers providing a private team CA remain subject to hostname checks.
func NewTeamServiceWithRootCAs(dataDir string, teamRootCAs *x509.CertPool) *TeamService {
	return newTeamService(dataDir, teamRootCAs, true)
}

// NewTeamServiceWithRootCAsDeferredRefresh loads persisted session metadata but
// performs no network I/O. Mobile startup uses this constructor and refreshes on
// explicit status/sync or the first authenticated request.
func NewTeamServiceWithRootCAsDeferredRefresh(dataDir string, teamRootCAs *x509.CertPool) *TeamService {
	return newTeamService(dataDir, teamRootCAs, false)
}

func newTeamService(dataDir string, teamRootCAs *x509.CertPool, refreshOnRestore bool) *TeamService {
	t := &TeamService{
		dataDir:            dataDir,
		syncDir:            fsutil.SyncDir,
		snapshotURLAllowed: publicSnapshotURLAllowed,
		teamRootCAs:        teamRootCAs,
	}
	t.cdnClient = newSnapshotHTTPClient()
	t.restore()
	if refreshOnRestore {
		_ = t.RefreshSessionIfNeeded()
	}
	return t
}

func (t *TeamService) newHTTPClient(rawServerURL string) (string, *http.Client, error) {
	return newTeamHTTPClient(rawServerURL, t.teamRootCAs)
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
	serverURL, client, err := t.newHTTPClient(p.ServerURL)
	if err != nil {
		return
	}
	t.mu.Lock()
	t.serverURL = serverURL
	t.refresh = p.RefreshToken
	t.client = client
	t.mu.Unlock()
	// Do not contact the network here. Android constructs this service during
	// cold start; RefreshSessionIfNeeded performs the exchange lazily and keeps
	// transiently-failing refresh credentials available for a later retry.
}

// Invalidate retires a façade-owned service before InitializeTeam installs its
// replacement. It first drains any rotating-token refresh, then advances the
// epoch; together with the persist lock this ensures no retired operation can
// write the shared session file after the method returns.
func (t *TeamService) Invalidate() {
	teamRefreshMu.Lock()
	defer teamRefreshMu.Unlock()
	t.persistMu.Lock()
	defer t.persistMu.Unlock()
	t.sessionMu.Lock()
	defer t.sessionMu.Unlock()
	t.mu.Lock()
	t.sessionEpoch++
	t.invalidated = true
	t.resetServerCachesLocked("")
	t.serverURL, t.access, t.refresh, t.user = "", "", "", nil
	t.client = nil
	t.mu.Unlock()
}

func (t *TeamService) persist() error {
	// Snapshot only after acquiring the persistence lock. A delayed persist from
	// an older request therefore writes the latest session, never stale tokens.
	t.persistMu.Lock()
	defer t.persistMu.Unlock()
	t.mu.RLock()
	if t.invalidated {
		t.mu.RUnlock()
		return ErrStaleTeamSession
	}
	p := teamPersist{
		ServerURL:    t.serverURL,
		RefreshToken: t.refresh,
	}
	t.mu.RUnlock()
	if p.ServerURL == "" {
		removed := false
		if err := os.Remove(t.sessionPath()); err == nil {
			removed = true
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("%w: remove session: %w", ErrTeamPersistence, err)
		}
		if removed {
			syncDir := t.syncDir
			if syncDir == nil {
				syncDir = fsutil.SyncDir
			}
			if err := syncDir(filepath.Dir(t.sessionPath())); err != nil {
				return fmt.Errorf("%w: sync session directory: %w", ErrTeamPersistence, err)
			}
		}
		return nil
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: encode session: %w", ErrTeamPersistence, err)
	}
	if err := fsutil.WriteFileAtomic(t.sessionPath(), b, 0o600); err != nil {
		return fmt.Errorf("%w: write session: %w", ErrTeamPersistence, err)
	}
	return nil
}
