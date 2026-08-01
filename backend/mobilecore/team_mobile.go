package mobilecore

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"sekaitext/backend/internal/model"
	"sekaitext/backend/internal/service"
)

var mobileTeamState struct {
	initializeMu sync.Mutex
	mu           sync.RWMutex
	service      *service.TeamService
	root         string
}

// mobileTeamBindingState coordinates TeamService and GlossaryStore replacement.
// Network syncs do not hold this lock, but their local merge callback does; any
// repeated InitializeTeam/InitializeGlossary advances the generation while
// swapping state, so an older in-flight result cannot persist into replacement.
var mobileTeamBindingState struct {
	mu         sync.RWMutex
	generation uint64
}

var errStaleMobileTeamBinding = errors.New("mobile team/glossary binding changed while sync was in flight")

type teamLoginRequest struct {
	ServerURL string `json:"serverUrl"`
	Username  string `json:"username"`
	Password  string `json:"password"`
}

type teamConnectRequest struct {
	ServerURL string `json:"serverUrl"`
}

type teamSyncRequest struct {
	Force bool `json:"force,omitempty"`
}

type teamStatusResponse struct {
	LoggedIn  bool              `json:"loggedIn"`
	Connected bool              `json:"connected"`
	Readonly  bool              `json:"readonly"`
	ServerURL string            `json:"serverUrl"`
	User      *service.TeamUser `json:"user"`
}

type teamCreateProposalRequest struct {
	Kind        string          `json:"kind"`
	TargetType  string          `json:"targetType,omitempty"`
	TargetID    string          `json:"targetId,omitempty"`
	Category    string          `json:"category"`
	Payload     json.RawMessage `json:"payload"`
	BaseVersion *int            `json:"baseVersion,omitempty"`
}

type teamIDRequest struct {
	ID string `json:"id"`
}

type teamPendingProposalsRequest struct {
	Category string `json:"category,omitempty"`
}

type teamReviewProposalRequest struct {
	ID   string `json:"id"`
	Note string `json:"note,omitempty"`
}

type teamSetReviewerRequest struct {
	UserID     string   `json:"userId"`
	Categories []string `json:"categories"`
}

type teamChangePasswordRequest struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

type teamUpdateProfileRequest struct {
	DisplayName string  `json:"displayName"`
	AvatarColor *string `json:"avatarColor,omitempty"`
}

type teamCreateUserRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	Role        string `json:"role"`
	DisplayName string `json:"displayName"`
}

type teamSetUserRoleRequest struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

type teamSetUserStatusRequest struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type teamResetUserPasswordRequest struct {
	ID          string `json:"id"`
	NewPassword string `json:"newPassword"`
}

type teamGlossaryPayload struct {
	Entries      []model.GlossaryEntry `json:"entries"`
	Appellations []model.Appellation   `json:"appellations"`
	Grammar      []model.GrammarUsage  `json:"grammar"`
}

// InitializeTeam binds the Android team façade to an app-private data
// directory. TeamService persists its selected server and refresh token in this
// directory; access and refresh tokens never cross the exported JSON boundary.
func InitializeTeam(dataDir string) error {
	return initializeTeamWithRootCAs(dataDir, nil)
}

// initializeTeamWithRootCAs is a test seam for trusted local TLS fixtures.
// Production always passes nil so TeamService uses the platform trust store,
// including user-installed roots, plus the exact-origin first-party public CA.
func initializeTeamWithRootCAs(dataDir string, rootCAs *x509.CertPool) error {
	if strings.TrimSpace(dataDir) == "" {
		return fmt.Errorf("initialize mobile team: data directory is required")
	}
	root, err := filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("initialize mobile team: resolve data directory: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("initialize mobile team: create data directory %q: %w", root, err)
	}

	mobileTeamState.initializeMu.Lock()
	defer mobileTeamState.initializeMu.Unlock()
	mobileTeamState.mu.RLock()
	previousService, previousRoot := mobileTeamState.service, mobileTeamState.root
	mobileTeamState.mu.RUnlock()
	if previousService != nil && previousRoot == root {
		// Production reinitialization for the same Android data directory is
		// idempotent. Reusing the service cannot wait on or invalidate an explicit
		// refresh already in flight during Activity recreation.
		if rootCAs == nil {
			return nil
		}
		// Tests may intentionally replace the same-root service with another trust
		// pool. Drain it before reusing the shared session file.
		previousService.Invalidate()
	}

	mobileTeamBindingState.mu.Lock()
	defer mobileTeamBindingState.mu.Unlock()

	teamService := service.NewTeamServiceWithRootCAsDeferredRefresh(root, rootCAs)
	mobileTeamState.mu.Lock()
	mobileTeamState.service = teamService
	mobileTeamState.root = root
	mobileTeamState.mu.Unlock()
	mobileTeamBindingState.generation++
	return nil
}

func currentTeamService() (*service.TeamService, error) {
	mobileTeamState.mu.RLock()
	teamService := mobileTeamState.service
	mobileTeamState.mu.RUnlock()
	if teamService == nil {
		return nil, fmt.Errorf("mobile team is not initialized; call InitializeTeam first")
	}
	return teamService, nil
}

type mobileTeamSyncBinding struct {
	generation    uint64
	teamService   *service.TeamService
	glossaryStore *service.GlossaryStore
	root          string
}

func currentMobileTeamSyncBinding() (mobileTeamSyncBinding, error) {
	mobileTeamBindingState.mu.RLock()
	defer mobileTeamBindingState.mu.RUnlock()

	mobileTeamState.mu.RLock()
	teamService, teamRoot := mobileTeamState.service, mobileTeamState.root
	mobileTeamState.mu.RUnlock()
	if teamService == nil {
		return mobileTeamSyncBinding{}, fmt.Errorf("mobile team is not initialized; call InitializeTeam first")
	}

	mobileGlossaryState.mu.RLock()
	glossaryStore, glossaryRoot := mobileGlossaryState.store, mobileGlossaryState.root
	mobileGlossaryState.mu.RUnlock()
	if glossaryStore == nil {
		return mobileTeamSyncBinding{}, fmt.Errorf("mobile glossary is not initialized; call InitializeGlossary first")
	}
	if teamRoot == "" || teamRoot != glossaryRoot {
		return mobileTeamSyncBinding{}, fmt.Errorf("mobile team and glossary must be initialized with the same data directory")
	}
	return mobileTeamSyncBinding{
		generation:    mobileTeamBindingState.generation,
		teamService:   teamService,
		glossaryStore: glossaryStore,
		root:          teamRoot,
	}, nil
}

func (binding mobileTeamSyncBinding) mergeRemoteGlossary(glossary model.GlossaryData) (int, error) {
	mobileTeamBindingState.mu.RLock()
	defer mobileTeamBindingState.mu.RUnlock()
	if mobileTeamBindingState.generation != binding.generation {
		return 0, errStaleMobileTeamBinding
	}

	mobileTeamState.mu.RLock()
	teamCurrent := mobileTeamState.service == binding.teamService && mobileTeamState.root == binding.root
	mobileTeamState.mu.RUnlock()
	mobileGlossaryState.mu.RLock()
	glossaryCurrent := mobileGlossaryState.store == binding.glossaryStore && mobileGlossaryState.root == binding.root
	mobileGlossaryState.mu.RUnlock()
	if !teamCurrent || !glossaryCurrent {
		return 0, errStaleMobileTeamBinding
	}

	// Hold the binding read lock through persistence so an initializer cannot
	// install a replacement store between the consistency check and disk commit.
	return binding.glossaryStore.MergeImport(
		glossary.Entries,
		glossary.Appellations,
		glossary.Grammar,
		model.OriginRemote,
	)
}

func decodeTeamRequest(operation, requestJSON string, target any) error {
	trimmed := strings.TrimSpace(requestJSON)
	if trimmed == "" {
		return fmt.Errorf("decode %s request: JSON payload is required", operation)
	}
	if trimmed[0] != '{' {
		return fmt.Errorf("decode %s request: JSON object is required", operation)
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s request: %w", operation, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return fmt.Errorf("decode %s request: %w", operation, err)
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func decodeEmptyTeamRequest(operation, requestJSON string) error {
	var request map[string]json.RawMessage
	if err := decodeTeamRequest(operation, requestJSON, &request); err != nil {
		return err
	}
	if request == nil {
		return fmt.Errorf("decode %s request: JSON object is required", operation)
	}
	return nil
}

func encodeTeamResponse(operation string, value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode %s response: %w", operation, err)
	}
	return string(encoded), nil
}

// TeamStatus returns the token-free session state. requestJSON must be a JSON
// object (normally {}).
func TeamStatus(requestJSON string) (string, error) {
	if err := decodeEmptyTeamRequest("team status", requestJSON); err != nil {
		return "", err
	}
	teamService, err := currentTeamService()
	if err != nil {
		return "", err
	}
	// Status is an explicit, user-driven recovery point. A transient refresh
	// failure is intentionally ignored here so callers still receive readonly
	// state and can retry later without re-entering credentials.
	_ = teamService.RefreshSessionIfNeeded()
	serverURL, user := teamService.Status()
	if user != nil {
		copy := *user
		user = &copy
	}
	return encodeTeamResponse("team status", teamStatusResponse{
		LoggedIn:  user != nil,
		Connected: serverURL != "",
		Readonly:  serverURL != "" && user == nil,
		ServerURL: serverURL,
		User:      user,
	})
}

// TeamLogin authenticates with {"serverUrl","username","password"}. The
// TeamService keeps both tokens; only the public TeamUser is returned.
func TeamLogin(requestJSON string) (string, error) {
	teamService, err := currentTeamService()
	if err != nil {
		return "", err
	}
	var request teamLoginRequest
	if err := decodeTeamRequest("team login", requestJSON, &request); err != nil {
		return "", err
	}
	user, err := teamService.Login(request.ServerURL, request.Username, request.Password)
	if err != nil {
		return "", fmt.Errorf("team login: %w", err)
	}
	return encodeTeamResponse("team login", map[string]any{"loggedIn": true, "user": user})
}

// TeamLogout drops authentication but keeps the selected server for readonly
// synchronization. requestJSON must be a JSON object (normally {}).
func TeamLogout(requestJSON string) (string, error) {
	if err := decodeEmptyTeamRequest("team logout", requestJSON); err != nil {
		return "", err
	}
	teamService, err := currentTeamService()
	if err != nil {
		return "", err
	}
	if err := teamService.Logout(); err != nil {
		return "", fmt.Errorf("team logout: %w", err)
	}
	return encodeTeamResponse("team logout", map[string]string{"status": "logged out"})
}

// TeamConnect enters no-login readonly mode with {"serverUrl":"https://..."}.
func TeamConnect(requestJSON string) (string, error) {
	teamService, err := currentTeamService()
	if err != nil {
		return "", err
	}
	var request teamConnectRequest
	if err := decodeTeamRequest("team connect", requestJSON, &request); err != nil {
		return "", err
	}
	if err := teamService.Connect(request.ServerURL); err != nil {
		return "", fmt.Errorf("team connect: %w", err)
	}
	return encodeTeamResponse("team connect", map[string]bool{"connected": true, "readonly": true})
}

// TeamDisconnect fully leaves team mode. requestJSON must be a JSON object.
func TeamDisconnect(requestJSON string) (string, error) {
	if err := decodeEmptyTeamRequest("team disconnect", requestJSON); err != nil {
		return "", err
	}
	teamService, err := currentTeamService()
	if err != nil {
		return "", err
	}
	if err := teamService.Disconnect(); err != nil {
		return "", fmt.Errorf("team disconnect: %w", err)
	}
	return encodeTeamResponse("team disconnect", map[string]string{"status": "disconnected"})
}

// TeamSync pulls and merges the authoritative remote glossary into the mobile
// GlossaryStore. InitializeGlossary must have been called for the same app data
// directory before synchronization.
func TeamSync(requestJSON string) (string, error) {
	binding, err := currentMobileTeamSyncBinding()
	if err != nil {
		return "", fmt.Errorf("team sync: %w", err)
	}
	var request teamSyncRequest
	if err := decodeTeamRequest("team sync", requestJSON, &request); err != nil {
		return "", err
	}

	var glossary model.GlossaryData
	result, err := binding.teamService.Sync(request.Force, func(raw []byte) (int, error) {
		if err := json.Unmarshal(raw, &glossary); err != nil {
			return 0, fmt.Errorf("invalid remote glossary payload: %w", err)
		}
		return binding.mergeRemoteGlossary(glossary)
	})
	if err != nil {
		return "", fmt.Errorf("team sync: %w", err)
	}
	if !result.Changed {
		return encodeTeamResponse("team sync", map[string]any{
			"status": "up-to-date", "version": result.Version, "changed": false,
		})
	}

	binding.glossaryStore.WriteSyncBackup(result.Raw)
	return encodeTeamResponse("team sync", map[string]any{
		"status":       "synced",
		"version":      result.Version,
		"changed":      true,
		"entries":      len(glossary.Entries),
		"appellations": len(glossary.Appellations),
		"grammar":      len(glossary.Grammar),
		"removed":      result.Removed,
	})
}

// TeamCreateProposal submits the same proposal object accepted by client.ts.
func TeamCreateProposal(requestJSON string) (string, error) {
	var request teamCreateProposalRequest
	if err := decodeTeamRequest("team create proposal", requestJSON, &request); err != nil {
		return "", err
	}
	if len(request.Payload) == 0 {
		return "", fmt.Errorf("team create proposal: payload is required")
	}
	return proxyTeamJSON("team create proposal", http.MethodPost, "/api/proposals", request)
}

func TeamMyProposals(requestJSON string) (string, error) {
	if err := decodeEmptyTeamRequest("team my proposals", requestJSON); err != nil {
		return "", err
	}
	return proxyTeamJSON("team my proposals", http.MethodGet, "/api/proposals/mine", nil)
}

func TeamWithdrawProposal(requestJSON string) (string, error) {
	var request teamIDRequest
	if err := decodeTeamRequest("team withdraw proposal", requestJSON, &request); err != nil {
		return "", err
	}
	path, err := teamResourcePath("team withdraw proposal", "/api/proposals/", request.ID, "")
	if err != nil {
		return "", err
	}
	return proxyTeamJSON("team withdraw proposal", http.MethodDelete, path, nil)
}

func TeamPendingProposals(requestJSON string) (string, error) {
	var request teamPendingProposalsRequest
	if err := decodeTeamRequest("team pending proposals", requestJSON, &request); err != nil {
		return "", err
	}
	path := "/api/proposals"
	if request.Category != "" {
		path += "?category=" + url.QueryEscape(request.Category)
	}
	return proxyTeamJSON("team pending proposals", http.MethodGet, path, nil)
}

func TeamApproveProposal(requestJSON string) (string, error) {
	var request teamReviewProposalRequest
	if err := decodeTeamRequest("team approve proposal", requestJSON, &request); err != nil {
		return "", err
	}
	path, err := teamResourcePath("team approve proposal", "/api/proposals/", request.ID, "/approve")
	if err != nil {
		return "", err
	}
	return proxyTeamJSON("team approve proposal", http.MethodPost, path, map[string]string{"note": request.Note})
}

func TeamRejectProposal(requestJSON string) (string, error) {
	var request teamReviewProposalRequest
	if err := decodeTeamRequest("team reject proposal", requestJSON, &request); err != nil {
		return "", err
	}
	path, err := teamResourcePath("team reject proposal", "/api/proposals/", request.ID, "/reject")
	if err != nil {
		return "", err
	}
	return proxyTeamJSON("team reject proposal", http.MethodPost, path, map[string]string{"note": request.Note})
}

func TeamSetReviewer(requestJSON string) (string, error) {
	var request teamSetReviewerRequest
	if err := decodeTeamRequest("team set reviewer", requestJSON, &request); err != nil {
		return "", err
	}
	return proxyTeamJSON("team set reviewer", http.MethodPost, "/api/admin/reviewers", request)
}

func TeamListUsers(requestJSON string) (string, error) {
	if err := decodeEmptyTeamRequest("team list users", requestJSON); err != nil {
		return "", err
	}
	return proxyTeamJSON("team list users", http.MethodGet, "/api/admin/users", nil)
}

func TeamChangePassword(requestJSON string) (string, error) {
	var request teamChangePasswordRequest
	if err := decodeTeamRequest("team change password", requestJSON, &request); err != nil {
		return "", err
	}
	return proxyTeamJSON("team change password", http.MethodPost, "/api/auth/password", request)
}

func TeamUpdateProfile(requestJSON string) (string, error) {
	var request teamUpdateProfileRequest
	if err := decodeTeamRequest("team update profile", requestJSON, &request); err != nil {
		return "", err
	}
	return proxyTeamJSON("team update profile", http.MethodPost, "/api/me", request)
}

func TeamAccountUsers(requestJSON string) (string, error) {
	if err := decodeEmptyTeamRequest("team account users", requestJSON); err != nil {
		return "", err
	}
	return proxyTeamJSON("team account users", http.MethodGet, "/api/users", nil)
}

func TeamCreateUser(requestJSON string) (string, error) {
	var request teamCreateUserRequest
	if err := decodeTeamRequest("team create user", requestJSON, &request); err != nil {
		return "", err
	}
	return proxyTeamJSON("team create user", http.MethodPost, "/api/admin/users", request)
}

func TeamSetUserRole(requestJSON string) (string, error) {
	var request teamSetUserRoleRequest
	if err := decodeTeamRequest("team set user role", requestJSON, &request); err != nil {
		return "", err
	}
	path, err := teamResourcePath("team set user role", "/api/admin/users/", request.ID, "/role")
	if err != nil {
		return "", err
	}
	return proxyTeamJSON("team set user role", http.MethodPost, path, map[string]string{"role": request.Role})
}

func TeamSetUserStatus(requestJSON string) (string, error) {
	var request teamSetUserStatusRequest
	if err := decodeTeamRequest("team set user status", requestJSON, &request); err != nil {
		return "", err
	}
	path, err := teamResourcePath("team set user status", "/api/admin/users/", request.ID, "/status")
	if err != nil {
		return "", err
	}
	return proxyTeamJSON("team set user status", http.MethodPost, path, map[string]string{"status": request.Status})
}

func TeamResetUserPassword(requestJSON string) (string, error) {
	var request teamResetUserPasswordRequest
	if err := decodeTeamRequest("team reset user password", requestJSON, &request); err != nil {
		return "", err
	}
	path, err := teamResourcePath("team reset user password", "/api/admin/users/", request.ID, "/reset-password")
	if err != nil {
		return "", err
	}
	return proxyTeamJSON("team reset user password", http.MethodPost, path, map[string]string{"newPassword": request.NewPassword})
}

func TeamDeleteUser(requestJSON string) (string, error) {
	var request teamIDRequest
	if err := decodeTeamRequest("team delete user", requestJSON, &request); err != nil {
		return "", err
	}
	path, err := teamResourcePath("team delete user", "/api/admin/users/", request.ID, "")
	if err != nil {
		return "", err
	}
	return proxyTeamJSON("team delete user", http.MethodDelete, path, nil)
}

func TeamGlossaryReplace(requestJSON string) (string, error) {
	var request teamGlossaryPayload
	if err := decodeTeamRequest("team glossary replace", requestJSON, &request); err != nil {
		return "", err
	}
	return proxyTeamJSON("team glossary replace", http.MethodPost, "/api/admin/glossary/replace", request)
}

func TeamBulkImport(requestJSON string) (string, error) {
	var request teamGlossaryPayload
	if err := decodeTeamRequest("team bulk import", requestJSON, &request); err != nil {
		return "", err
	}
	return proxyTeamJSON("team bulk import", http.MethodPost, "/api/admin/glossary/bulk-import", request)
}

func teamResourcePath(operation, prefix, id, suffix string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("%s: id is required", operation)
	}
	if teamIDHasDotSegment(id) {
		return "", fmt.Errorf("%s: id must not contain dot path segments", operation)
	}
	return prefix + url.PathEscape(id) + suffix, nil
}

func teamIDHasDotSegment(id string) bool {
	candidate := id
	// Check both literal and encoded forms. PathEscape prevents a single decode
	// from becoming traversal, but rejecting pre-encoded dot segments also keeps
	// the request safe behind intermediaries that normalize more than once.
	for {
		normalized := strings.ReplaceAll(candidate, `\`, "/")
		for _, segment := range strings.Split(normalized, "/") {
			if segment == "." || segment == ".." {
				return true
			}
		}
		decoded, err := url.PathUnescape(candidate)
		if err != nil || decoded == candidate {
			return false
		}
		candidate = decoded
	}
}

func proxyTeamJSON(operation, method, path string, payload any) (string, error) {
	teamService, err := currentTeamService()
	if err != nil {
		return "", err
	}
	body, status, err := teamService.Proxy(method, path, payload)
	if err != nil {
		return "", fmt.Errorf("%s: %w", operation, err)
	}

	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return "", teamRemoteStatusError(operation, status)
	}
	value, decodeErr := decodeAndSanitizeTeamResponse(body)
	if decodeErr != nil {
		return "", fmt.Errorf("%s: invalid remote JSON response: %w", operation, decodeErr)
	}
	return encodeTeamResponse(operation, value)
}

func decodeAndSanitizeTeamResponse(body []byte) (any, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return map[string]any{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	return sanitizeTeamResponse(value), nil
}

func sanitizeTeamResponse(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		clean := make(map[string]any, len(typed))
		for key, child := range typed {
			if sensitiveTeamResponseKey(key) {
				continue
			}
			clean[key] = sanitizeTeamResponse(child)
		}
		return clean
	case []any:
		clean := make([]any, len(typed))
		for index, child := range typed {
			clean[index] = sanitizeTeamResponse(child)
		}
		return clean
	default:
		return value
	}
}

func sensitiveTeamResponseKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(key))
	return normalized == "authorization" ||
		normalized == "password" ||
		normalized == "oldpassword" ||
		normalized == "newpassword" ||
		normalized == "secret" ||
		strings.HasSuffix(normalized, "token")
}

func teamRemoteStatusError(operation string, status int) error {
	return fmt.Errorf("%s failed (HTTP %d)", operation, status)
}
