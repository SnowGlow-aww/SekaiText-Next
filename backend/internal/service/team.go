package service

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"sekaitext/backend/internal/fsutil"
)

// teamPersist holds the persisted team URL, refresh token, and confirmed TLS
// certificate so the app can re-authenticate without weakening verification.
type teamPersist struct {
	ServerURL              string `json:"serverUrl"`
	RefreshToken           string `json:"refreshToken"`
	CertificateFingerprint string `json:"certificateFingerprint,omitempty"`
	CertificateDER         string `json:"certificateDer,omitempty"`
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
// tokens (in memory), pins the user-confirmed server certificate, and exposes
// login/refresh + authenticated request helpers. The
// frontend only ever talks to the local backend, never the remote directly
// (a webview can't accept the self-signed cert).
type TeamService struct {
	dataDir string
	syncDir func(string) error
	// client remains nil until a certificate fingerprint has been confirmed.
	client *http.Client
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
	serverURL    string
	access       string
	refresh      string
	fingerprint  string
	certDER      []byte
	user         *TeamUser
	lastVer      int
	// snapshotBase 是团队服务器经 GET /api/config 暴露的 CDN 快照基址（形如
	// https://sakimizuki.accr.cc/sekaitext-glossary）；为空表示老服务器或发现失败，
	// 读路径回退服务器直连。snapshotBaseFor 和 snapshotBaseFingerprint 记录该值
	// 对应的服务器身份，URL 或证书 pin 变化后据此惰性重新发现。
	snapshotBase            string
	snapshotBaseFor         string
	snapshotBaseFingerprint string
}

type teamSessionIdentity struct {
	epoch       uint64
	serverURL   string
	fingerprint string
}

func (t *TeamService) sessionIdentityCurrentLocked(session teamSessionIdentity) bool {
	return t.sessionEpoch == session.epoch &&
		t.serverURL == session.serverURL &&
		t.fingerprint == session.fingerprint
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

func (t *TeamService) resetServerCachesLocked(serverURL, fingerprint string) {
	if serverURL == t.serverURL && fingerprint == t.fingerprint {
		return
	}
	t.lastVer = 0
	t.snapshotBase, t.snapshotBaseFor, t.snapshotBaseFingerprint = "", "", ""
}

// NewTeamService creates the service and attempts to restore a prior session.
func NewTeamService(dataDir string) *TeamService {
	t := &TeamService{
		dataDir:            dataDir,
		syncDir:            fsutil.SyncDir,
		snapshotURLAllowed: publicSnapshotURLAllowed,
	}
	t.cdnClient = newSnapshotHTTPClient()
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
	serverURL, err := normalizeTeamServerURL(p.ServerURL)
	if err != nil {
		return
	}
	certDER, err := base64.StdEncoding.DecodeString(p.CertificateDER)
	if err != nil || len(certDER) == 0 || certificateFingerprint(certDER) != p.CertificateFingerprint {
		// Pre-TOFU sessions may contain a refresh token but no pin. Keep only the
		// address for readonly UI state and never send the unpinned credential.
		t.mu.Lock()
		t.serverURL = serverURL
		t.mu.Unlock()
		_ = t.persist()
		return
	}
	client, err := newPinnedTeamClient(serverURL, certDER, p.CertificateFingerprint)
	if err != nil {
		return
	}
	t.mu.Lock()
	t.serverURL = serverURL
	t.refresh = p.RefreshToken
	t.fingerprint = p.CertificateFingerprint
	t.certDER = append([]byte(nil), certDER...)
	t.client = client
	t.mu.Unlock()
	// Best-effort: doRefresh clears and persists credentials only for a terminal
	// 401/403 rejection. Network, 5xx, rate-limit, and malformed-response errors
	// retain the refresh token so startup does not destroy a retryable session.
	if p.RefreshToken != "" {
		_ = t.doRefresh()
	}
}

func (t *TeamService) persist() error {
	// Snapshot only after acquiring the persistence lock. A delayed persist from
	// an older request therefore writes the latest session, never stale tokens.
	t.persistMu.Lock()
	defer t.persistMu.Unlock()
	t.mu.RLock()
	p := teamPersist{
		ServerURL:              t.serverURL,
		RefreshToken:           t.refresh,
		CertificateFingerprint: t.fingerprint,
		CertificateDER:         base64.StdEncoding.EncodeToString(t.certDER),
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

func certificateFingerprint(certDER []byte) string {
	sum := sha256.Sum256(certDER)
	return hex.EncodeToString(sum[:])
}

func certificateFromDER(certDER []byte) (*x509.Certificate, error) {
	return x509.ParseCertificate(certDER)
}
