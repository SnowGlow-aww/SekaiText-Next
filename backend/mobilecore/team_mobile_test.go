package mobilecore

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sekaitext/backend/internal/model"
	"sekaitext/backend/internal/service"
)

func resetMobileTeamTestState() {
	mobileTeamState.initializeMu.Lock()
	defer mobileTeamState.initializeMu.Unlock()
	mobileTeamBindingState.mu.Lock()
	mobileTeamState.mu.Lock()
	mobileTeamState.service = nil
	mobileTeamState.root = ""
	mobileTeamState.mu.Unlock()
	mobileTeamBindingState.generation++
	mobileTeamBindingState.mu.Unlock()
}

func resetMobileGlossaryTestState() {
	mobileGlossaryState.initializeMu.Lock()
	defer mobileGlossaryState.initializeMu.Unlock()
	mobileTeamBindingState.mu.Lock()
	mobileGlossaryState.mu.Lock()
	mobileGlossaryState.store = nil
	mobileGlossaryState.root = ""
	mobileGlossaryState.mu.Unlock()
	mobileTeamBindingState.generation++
	mobileTeamBindingState.mu.Unlock()
}

func mobileTeamTestRoots(t *testing.T, servers ...*httptest.Server) *x509.CertPool {
	t.Helper()
	roots := x509.NewCertPool()
	for _, server := range servers {
		roots.AddCert(server.Certificate())
	}
	return roots
}

func writeMobileTeamSession(t *testing.T, dataDir, serverURL, refreshToken string) {
	t.Helper()
	raw, err := json.Marshal(map[string]string{
		"serverUrl":    serverURL,
		"refreshToken": refreshToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "team-session.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func encodeTeamTestRequest(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode team test request: %v", err)
	}
	return string(encoded)
}

func decodeTeamTestResponse(t *testing.T, raw string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		t.Fatalf("decode team response %q: %v", raw, err)
	}
}

func assertNoTeamSecretKeys(t *testing.T, raw string) {
	t.Helper()
	var value any
	decodeTeamTestResponse(t, raw, &value)
	var visit func(any)
	visit = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			for key, child := range typed {
				normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(key))
				if normalized == "authorization" || normalized == "password" || normalized == "secret" || strings.HasSuffix(normalized, "token") {
					t.Fatalf("secret key %q crossed mobile JSON boundary in %s", key, raw)
				}
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(value)
}

func assertTeamJSONEqual(t *testing.T, got any, wantJSON string) {
	t.Helper()
	var want any
	if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
		t.Fatalf("decode expected JSON %q: %v", wantJSON, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON body = %#v, want %#v", got, want)
	}
}

func TestTeamMobileInitializationAndStringJSONBoundary(t *testing.T) {
	resetMobileTeamTestState()
	if _, err := TeamStatus(`{}`); err == nil || !strings.Contains(err.Error(), "InitializeTeam") {
		t.Fatalf("unexpected uninitialized status error: %v", err)
	}
	if err := InitializeTeam(" \t "); err == nil || !strings.Contains(err.Error(), "data directory is required") {
		t.Fatalf("unexpected empty-directory error: %v", err)
	}

	if err := InitializeTeam(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := TeamStatus(""); err == nil || !strings.Contains(err.Error(), "JSON payload is required") {
		t.Fatalf("unexpected empty status request error: %v", err)
	}
	if _, err := TeamStatus(`{} {}`); err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("unexpected trailing JSON error: %v", err)
	}
	if _, err := TeamStatus(`null`); err == nil || !strings.Contains(err.Error(), "JSON object is required") {
		t.Fatalf("unexpected non-object request error: %v", err)
	}
	if _, err := TeamConnect(`{"serverUrl":"http://example.com"}`); err == nil || !strings.Contains(err.Error(), "absolute HTTPS") {
		t.Fatalf("insecure team URL was not rejected: %v", err)
	}
	statusJSON, err := TeamStatus(`{}`)
	if err != nil {
		t.Fatal(err)
	}
	var status teamStatusResponse
	decodeTeamTestResponse(t, statusJSON, &status)
	if status.LoggedIn || status.Connected || status.Readonly || status.ServerURL != "" || status.User != nil {
		t.Fatalf("unexpected initial team status: %s", statusJSON)
	}

	functions := map[string]any{
		"TeamStatus":            TeamStatus,
		"TeamLogin":             TeamLogin,
		"TeamLogout":            TeamLogout,
		"TeamConnect":           TeamConnect,
		"TeamDisconnect":        TeamDisconnect,
		"TeamSync":              TeamSync,
		"TeamCreateProposal":    TeamCreateProposal,
		"TeamMyProposals":       TeamMyProposals,
		"TeamWithdrawProposal":  TeamWithdrawProposal,
		"TeamPendingProposals":  TeamPendingProposals,
		"TeamApproveProposal":   TeamApproveProposal,
		"TeamRejectProposal":    TeamRejectProposal,
		"TeamSetReviewer":       TeamSetReviewer,
		"TeamListUsers":         TeamListUsers,
		"TeamChangePassword":    TeamChangePassword,
		"TeamUpdateProfile":     TeamUpdateProfile,
		"TeamAccountUsers":      TeamAccountUsers,
		"TeamCreateUser":        TeamCreateUser,
		"TeamSetUserRole":       TeamSetUserRole,
		"TeamSetUserStatus":     TeamSetUserStatus,
		"TeamResetUserPassword": TeamResetUserPassword,
		"TeamDeleteUser":        TeamDeleteUser,
		"TeamGlossaryReplace":   TeamGlossaryReplace,
		"TeamBulkImport":        TeamBulkImport,
	}
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	for name, function := range functions {
		functionType := reflect.TypeOf(function)
		if functionType.NumIn() != 1 || functionType.In(0).Kind() != reflect.String {
			t.Fatalf("%s must accept exactly one JSON string, got %s", name, functionType)
		}
		if functionType.NumOut() != 2 || functionType.Out(0).Kind() != reflect.String || functionType.Out(1) != errorType {
			t.Fatalf("%s must return (string, error), got %s", name, functionType)
		}
	}

	initializeType := reflect.TypeOf(InitializeTeam)
	if initializeType.NumIn() != 1 || initializeType.In(0).Kind() != reflect.String || initializeType.NumOut() != 1 || initializeType.Out(0) != errorType {
		t.Fatalf("InitializeTeam has non-gomobile-safe signature: %s", initializeType)
	}
}

func TestTeamMobileLoginUsesTrustedTLSAndPersistsWithoutExposingTokens(t *testing.T) {
	resetMobileTeamTestState()
	var loginHits atomic.Int32
	var refreshHits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			loginHits.Add(1)
			_, _ = io.WriteString(w, `{"accessToken":"access-secret","refreshToken":"refresh-secret","token":"must-not-leak","user":{"id":"u1","username":"amia","displayName":"Amia","role":"member","status":"active","avatarColor":"#123456"}}`)
		case "/api/auth/refresh":
			refreshHits.Add(1)
			_, _ = io.WriteString(w, `{"accessToken":"renewed-access","refreshToken":"renewed-refresh","user":{"id":"u1","username":"amia","displayName":"Amia Renewed","role":"member","status":"active"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	dataDir := t.TempDir()
	if err := InitializeTeam(dataDir); err != nil {
		t.Fatal(err)
	}
	loginRequest := encodeTeamTestRequest(t, map[string]string{
		"serverUrl": server.URL,
		"username":  "amia",
		"password":  "login-password",
	})
	_, err := TeamLogin(loginRequest)
	var unknownAuthority x509.UnknownAuthorityError
	if !errors.As(err, &unknownAuthority) {
		t.Fatalf("untrusted TeamLogin error = %v, want x509.UnknownAuthorityError", err)
	}
	if loginHits.Load() != 0 {
		t.Fatalf("untrusted server received %d credential requests", loginHits.Load())
	}

	roots := mobileTeamTestRoots(t, server)
	if err := initializeTeamWithRootCAs(dataDir, roots); err != nil {
		t.Fatal(err)
	}
	loginJSON, err := TeamLogin(loginRequest)
	if err != nil {
		t.Fatal(err)
	}
	assertNoTeamSecretKeys(t, loginJSON)
	var login struct {
		LoggedIn bool             `json:"loggedIn"`
		User     service.TeamUser `json:"user"`
	}
	decodeTeamTestResponse(t, loginJSON, &login)
	if !login.LoggedIn || login.User.Username != "amia" || login.User.DisplayName != "Amia" {
		t.Fatalf("unexpected login response: %s", loginJSON)
	}

	sessionPath := filepath.Join(dataDir, "team-session.json")
	session, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(session), `"refreshToken": "refresh-secret"`) {
		t.Fatalf("refresh token was not persisted by TeamService: %s", session)
	}
	if strings.Contains(string(session), "access-secret") || strings.Contains(string(session), "accessToken") {
		t.Fatalf("access token was persisted unexpectedly: %s", session)
	}
	info, err := os.Stat(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("team session mode = %o, want 600", got)
	}

	// Reinitializing against the same app-private directory must restore via the
	// refresh token without returning either token to the WebView boundary.
	if err := initializeTeamWithRootCAs(dataDir, roots); err != nil {
		t.Fatal(err)
	}
	if refreshHits.Load() != 0 {
		t.Fatalf("InitializeTeam performed a blocking refresh: hits=%d", refreshHits.Load())
	}
	statusJSON, err := TeamStatus(`{}`)
	if err != nil {
		t.Fatal(err)
	}
	if refreshHits.Load() != 1 {
		t.Fatalf("explicit TeamStatus refresh hits = %d, want 1", refreshHits.Load())
	}
	assertNoTeamSecretKeys(t, statusJSON)
	var restored teamStatusResponse
	decodeTeamTestResponse(t, statusJSON, &restored)
	if !restored.LoggedIn || !restored.Connected || restored.Readonly || restored.User == nil || restored.User.DisplayName != "Amia Renewed" {
		t.Fatalf("session was not restored: %s", statusJSON)
	}

	if _, err := TeamLogout(`{}`); err != nil {
		t.Fatal(err)
	}
	statusJSON, err = TeamStatus(`{}`)
	if err != nil {
		t.Fatal(err)
	}
	decodeTeamTestResponse(t, statusJSON, &restored)
	if restored.LoggedIn || !restored.Connected || !restored.Readonly || restored.User != nil {
		t.Fatalf("logout did not retain readonly connection: %s", statusJSON)
	}
	if err := initializeTeamWithRootCAs(dataDir, roots); err != nil {
		t.Fatal(err)
	}
	if refreshHits.Load() != 1 {
		t.Fatalf("empty refresh token triggered another refresh: %d", refreshHits.Load())
	}

	if _, err := TeamDisconnect(`{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("disconnect did not remove persisted session: %v", err)
	}
	statusJSON, err = TeamStatus(`{}`)
	if err != nil {
		t.Fatal(err)
	}
	decodeTeamTestResponse(t, statusJSON, &restored)
	if restored.Connected || restored.LoggedIn || restored.Readonly || restored.ServerURL != "" {
		t.Fatalf("disconnect did not clear status: %s", statusJSON)
	}
}

func TestTeamMobileInitializeDefersBlockedSessionRefresh(t *testing.T) {
	resetMobileTeamTestState()
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var refreshHits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/refresh" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		refreshHits.Add(1)
		close(refreshStarted)
		<-releaseRefresh
		_, _ = io.WriteString(w, `{"accessToken":"restored-access","refreshToken":"restored-refresh","user":{"id":"u1","username":"amia","displayName":"Restored"}}`)
	}))
	defer server.Close()

	dataDir := t.TempDir()
	writeMobileTeamSession(t, dataDir, server.URL, "saved-refresh")
	roots := mobileTeamTestRoots(t, server)
	initDone := make(chan error, 1)
	go func() {
		initDone <- initializeTeamWithRootCAs(dataDir, roots)
	}()
	select {
	case err := <-initDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
		close(releaseRefresh)
		t.Fatal("InitializeTeam blocked Android cold start on session refresh")
	}
	if refreshHits.Load() != 0 {
		close(releaseRefresh)
		t.Fatalf("InitializeTeam contacted refresh endpoint: hits=%d", refreshHits.Load())
	}

	type statusResult struct {
		raw string
		err error
	}
	statusDone := make(chan statusResult, 1)
	go func() {
		raw, err := TeamStatus(`{}`)
		statusDone <- statusResult{raw: raw, err: err}
	}()
	select {
	case <-refreshStarted:
	case <-time.After(time.Second):
		close(releaseRefresh)
		t.Fatal("explicit TeamStatus did not trigger deferred refresh")
	}

	// Activity recreation commonly repeats InitializeTeam for the same app data
	// directory while an explicit status refresh is still running. It must reuse
	// the current service instead of waiting on that network request.
	reinitializeDone := make(chan error, 1)
	go func() { reinitializeDone <- InitializeTeam(dataDir) }()
	select {
	case err := <-reinitializeDone:
		if err != nil {
			close(releaseRefresh)
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
		close(releaseRefresh)
		t.Fatal("repeated InitializeTeam blocked on an in-flight refresh")
	}
	close(releaseRefresh)
	result := <-statusDone
	if result.err != nil {
		t.Fatal(result.err)
	}
	var status teamStatusResponse
	decodeTeamTestResponse(t, result.raw, &status)
	if !status.LoggedIn || status.User == nil || status.User.DisplayName != "Restored" {
		t.Fatalf("deferred refresh did not restore session: %s", result.raw)
	}
	assertNoTeamSecretKeys(t, result.raw)
}

func TestTeamMobileTransientRefreshCanRetryOnStatusAndAuthenticatedRequest(t *testing.T) {
	resetMobileTeamTestState()
	var refreshHits atomic.Int32
	var proposalHits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/refresh":
			if refreshHits.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, `{"accessToken":"retry-access","refreshToken":"retry-refresh","user":{"id":"u1","username":"amia","displayName":"Retried"}}`)
		case "/api/proposals/mine":
			proposalHits.Add(1)
			if r.Header.Get("Authorization") != "Bearer retry-access" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `[]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	dataDir := t.TempDir()
	writeMobileTeamSession(t, dataDir, server.URL, "saved-refresh")
	if err := initializeTeamWithRootCAs(dataDir, mobileTeamTestRoots(t, server)); err != nil {
		t.Fatal(err)
	}
	statusJSON, err := TeamStatus(`{}`)
	if err != nil {
		t.Fatal(err)
	}
	var pending teamStatusResponse
	decodeTeamTestResponse(t, statusJSON, &pending)
	if pending.LoggedIn || !pending.Connected || !pending.Readonly || refreshHits.Load() != 1 {
		t.Fatalf("transient refresh should retain retryable readonly state: %s hits=%d", statusJSON, refreshHits.Load())
	}

	restoredJSON, err := TeamStatus(`{}`)
	if err != nil {
		t.Fatal(err)
	}
	var restored teamStatusResponse
	decodeTeamTestResponse(t, restoredJSON, &restored)
	if !restored.LoggedIn || restored.User == nil || restored.User.DisplayName != "Retried" || refreshHits.Load() != 2 {
		t.Fatalf("explicit status did not retry transient refresh: %s hits=%d", restoredJSON, refreshHits.Load())
	}
	assertNoTeamSecretKeys(t, restoredJSON)

	mineJSON, err := TeamMyProposals(`{}`)
	if err != nil {
		t.Fatal(err)
	}
	if mineJSON != `[]` || refreshHits.Load() != 2 || proposalHits.Load() != 1 {
		t.Fatalf("authenticated request result=%s refreshHits=%d proposalHits=%d", mineJSON, refreshHits.Load(), proposalHits.Load())
	}
	session, err := os.ReadFile(filepath.Join(dataDir, "team-session.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(session), "retry-refresh") || strings.Contains(string(session), "retry-access") {
		t.Fatalf("retry did not persist only the rotated refresh token: %s", session)
	}
}

func TestTeamMobileAuthAndSyncErrorsDoNotExposeRemoteText(t *testing.T) {
	const sensitive = "access-secret refresh-secret server-stack-secret"
	assertSafeError := func(t *testing.T, err error, want string) {
		t.Helper()
		if err == nil || err.Error() != want || strings.Contains(err.Error(), "access-secret") || strings.Contains(err.Error(), "refresh-secret") || strings.Contains(err.Error(), "server-stack-secret") {
			t.Fatalf("error = %v, want %q without remote text", err, want)
		}
	}

	t.Run("login rejection", func(t *testing.T) {
		resetMobileTeamTestState()
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/auth/login" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"`+sensitive+`"}`)
		}))
		defer server.Close()

		if err := initializeTeamWithRootCAs(t.TempDir(), mobileTeamTestRoots(t, server)); err != nil {
			t.Fatal(err)
		}
		_, err := TeamLogin(encodeTeamTestRequest(t, map[string]string{
			"serverUrl": server.URL,
			"username":  "amia",
			"password":  "password",
		}))
		assertSafeError(t, err, "team login: login failed (HTTP 401)")
	})

	t.Run("refresh failure", func(t *testing.T) {
		resetMobileTeamTestState()
		var proposalHits atomic.Int32
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/auth/refresh":
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, `{"error":"`+sensitive+`"}`)
			case "/api/proposals/mine":
				proposalHits.Add(1)
				_, _ = io.WriteString(w, `[]`)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer server.Close()

		dataDir := t.TempDir()
		writeMobileTeamSession(t, dataDir, server.URL, "saved-refresh")
		if err := initializeTeamWithRootCAs(dataDir, mobileTeamTestRoots(t, server)); err != nil {
			t.Fatal(err)
		}
		_, err := TeamMyProposals(`{}`)
		assertSafeError(t, err, "team my proposals: refresh failed (HTTP 503)")
		if proposalHits.Load() != 0 {
			t.Fatalf("authenticated endpoint received %d requests after failed refresh", proposalHits.Load())
		}
	})

	t.Run("sync status failure", func(t *testing.T) {
		resetMobileTeamTestState()
		resetMobileGlossaryTestState()
		var versionHits atomic.Int32
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/config":
				w.WriteHeader(http.StatusNotFound)
			case "/api/glossary/version":
				if versionHits.Add(1) == 1 {
					_, _ = io.WriteString(w, `{"version":1}`)
					return
				}
				w.WriteHeader(http.StatusBadGateway)
				_, _ = io.WriteString(w, `{"error":"`+sensitive+`"}`)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer server.Close()

		dataDir := t.TempDir()
		if err := InitializeGlossary(dataDir); err != nil {
			t.Fatal(err)
		}
		if err := initializeTeamWithRootCAs(dataDir, mobileTeamTestRoots(t, server)); err != nil {
			t.Fatal(err)
		}
		if _, err := TeamConnect(encodeTeamTestRequest(t, map[string]string{"serverUrl": server.URL})); err != nil {
			t.Fatal(err)
		}
		_, err := TeamSync(`{}`)
		assertSafeError(t, err, "team sync: remote glossary version failed (HTTP 502)")
	})
}

func TestTeamMobileSyncRejectsReinitializedBinding(t *testing.T) {
	resetMobileTeamTestState()
	resetMobileGlossaryTestState()
	exportStarted := make(chan struct{})
	releaseExport := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/config":
			w.WriteHeader(http.StatusNotFound)
		case "/api/glossary/version":
			_, _ = io.WriteString(w, `{"version":1}`)
		case "/api/glossary/export":
			close(exportStarted)
			<-releaseExport
			_, _ = io.WriteString(w, `{"entries":[{"source":"stale","translation":"stale","category":"remote"}],"appellations":[],"grammar":[]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	oldDir := t.TempDir()
	if err := InitializeGlossary(oldDir); err != nil {
		t.Fatal(err)
	}
	if err := initializeTeamWithRootCAs(oldDir, mobileTeamTestRoots(t, server)); err != nil {
		t.Fatal(err)
	}
	if _, err := TeamConnect(encodeTeamTestRequest(t, map[string]string{"serverUrl": server.URL})); err != nil {
		t.Fatal(err)
	}

	syncDone := make(chan error, 1)
	go func() {
		_, err := TeamSync(`{}`)
		syncDone <- err
	}()
	select {
	case <-exportStarted:
	case <-time.After(time.Second):
		t.Fatal("team sync did not reach blocked export")
	}

	newDir := t.TempDir()
	if err := InitializeGlossary(newDir); err != nil {
		t.Fatal(err)
	}
	close(releaseExport)
	if err := <-syncDone; !errors.Is(err, errStaleMobileTeamBinding) {
		t.Fatalf("stale TeamSync error = %v, want binding generation error", err)
	}
	if err := InitializeTeam(newDir); err != nil {
		t.Fatal(err)
	}

	exportJSON, err := GlossaryExport()
	if err != nil {
		t.Fatal(err)
	}
	var current model.GlossaryData
	decodeTeamTestResponse(t, exportJSON, &current)
	if len(current.Entries) != 0 || len(current.Appellations) != 0 || len(current.Grammar) != 0 {
		t.Fatalf("old TeamService wrote into replacement glossary: %s", exportJSON)
	}
}

func TestTeamMobileSyncUsesExistingServicesAndPersistsReadonlyConnection(t *testing.T) {
	resetMobileTeamTestState()
	resetMobileGlossaryTestState()
	var versionHits atomic.Int32
	var exportHits atomic.Int32
	var remoteVersion atomic.Int32
	var emptySnapshot atomic.Bool
	remoteVersion.Store(7)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/config":
			w.WriteHeader(http.StatusNotFound)
		case "/api/glossary/version":
			versionHits.Add(1)
			_, _ = io.WriteString(w, fmt.Sprintf(`{"version":%d}`, remoteVersion.Load()))
		case "/api/glossary/export":
			exportHits.Add(1)
			if emptySnapshot.Load() {
				_, _ = io.WriteString(w, `{"entries":[],"appellations":[],"grammar":[]}`)
				return
			}
			_, _ = io.WriteString(w, `{
				"entries":[{"id":"server-id","source":"セカイ","translation":"世界","category":"专有名词表","origin":"remote"}],
				"appellations":[{"speaker":"初音ミク","target":"镜音铃","jp":"リン","cn":"铃"}],
				"grammar":[{"id":"server-grammar","item":"〜ながら","example":"歌いながら踊る"}]
			}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	dataDir := t.TempDir()
	if err := InitializeGlossary(dataDir); err != nil {
		t.Fatal(err)
	}
	if _, err := GlossaryAddEntry(`{"source":"本地词","translation":"local","category":"自定义"}`); err != nil {
		t.Fatal(err)
	}
	roots := mobileTeamTestRoots(t, server)
	if err := initializeTeamWithRootCAs(dataDir, roots); err != nil {
		t.Fatal(err)
	}
	connectJSON, err := TeamConnect(encodeTeamTestRequest(t, map[string]string{"serverUrl": server.URL}))
	if err != nil {
		t.Fatal(err)
	}
	var connected map[string]bool
	decodeTeamTestResponse(t, connectJSON, &connected)
	if !connected["connected"] || !connected["readonly"] {
		t.Fatalf("unexpected connect response: %s", connectJSON)
	}

	syncJSON, err := TeamSync(`{"force":false}`)
	if err != nil {
		t.Fatal(err)
	}
	var synced struct {
		Status       string `json:"status"`
		Version      int    `json:"version"`
		Changed      bool   `json:"changed"`
		Entries      int    `json:"entries"`
		Appellations int    `json:"appellations"`
		Grammar      int    `json:"grammar"`
	}
	decodeTeamTestResponse(t, syncJSON, &synced)
	if synced.Status != "synced" || synced.Version != 7 || !synced.Changed || synced.Entries != 1 || synced.Appellations != 1 || synced.Grammar != 1 {
		t.Fatalf("unexpected sync response: %s", syncJSON)
	}

	exportJSON, err := GlossaryExport()
	if err != nil {
		t.Fatal(err)
	}
	var glossary model.GlossaryData
	decodeTeamTestResponse(t, exportJSON, &glossary)
	if len(glossary.Entries) != 2 {
		t.Fatalf("sync did not merge with local glossary: %s", exportJSON)
	}
	origins := map[string]string{}
	for _, entry := range glossary.Entries {
		origins[entry.Source] = entry.Origin
	}
	if origins["本地词"] != model.OriginUser || origins["セカイ"] != model.OriginRemote {
		t.Fatalf("unexpected merged origins: %#v", origins)
	}
	if len(glossary.Appellations) != 1 || len(glossary.Grammar) != 1 {
		t.Fatalf("sync did not merge all glossary sections: %s", exportJSON)
	}

	upToDateJSON, err := TeamSync(`{}`)
	if err != nil {
		t.Fatal(err)
	}
	var upToDate struct {
		Status  string `json:"status"`
		Version int    `json:"version"`
		Changed bool   `json:"changed"`
	}
	decodeTeamTestResponse(t, upToDateJSON, &upToDate)
	if upToDate.Status != "up-to-date" || upToDate.Version != 7 || upToDate.Changed {
		t.Fatalf("unexpected second sync response: %s", upToDateJSON)
	}
	// A later full authoritative export may be genuinely empty. It must clear
	// every old remote section without deleting the local user entry.
	remoteVersion.Store(8)
	emptySnapshot.Store(true)
	clearedJSON, err := TeamSync(`{}`)
	if err != nil {
		t.Fatal(err)
	}
	var cleared struct {
		Status  string `json:"status"`
		Version int    `json:"version"`
		Changed bool   `json:"changed"`
		Removed int    `json:"removed"`
	}
	decodeTeamTestResponse(t, clearedJSON, &cleared)
	if cleared.Status != "synced" || cleared.Version != 8 || !cleared.Changed || cleared.Removed != 1 {
		t.Fatalf("unexpected authoritative clear response: %s", clearedJSON)
	}
	exportJSON, err = GlossaryExport()
	if err != nil {
		t.Fatal(err)
	}
	var clearedGlossary model.GlossaryData
	decodeTeamTestResponse(t, exportJSON, &clearedGlossary)
	if len(clearedGlossary.Entries) != 1 || clearedGlossary.Entries[0].Source != "本地词" || clearedGlossary.Entries[0].Origin != model.OriginUser || len(clearedGlossary.Appellations) != 0 || len(clearedGlossary.Grammar) != 0 {
		t.Fatalf("authoritative empty snapshot damaged local data or retained remote data: %s", exportJSON)
	}

	if exportHits.Load() != 2 {
		t.Fatalf("export requests = %d, want 2", exportHits.Load())
	}
	if versionHits.Load() != 4 { // connect + three sync polls
		t.Fatalf("version requests = %d, want 4", versionHits.Load())
	}

	backups, err := os.ReadDir(filepath.Join(dataDir, "resources", "glossary", "backups"))
	if err != nil || len(backups) == 0 {
		t.Fatalf("sync backup was not written: entries=%v err=%v", backups, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "team-session.json")); err != nil {
		t.Fatalf("readonly connection was not persisted: %v", err)
	}

	if err := initializeTeamWithRootCAs(dataDir, roots); err != nil {
		t.Fatal(err)
	}
	statusJSON, err := TeamStatus(`{}`)
	if err != nil {
		t.Fatal(err)
	}
	var status teamStatusResponse
	decodeTeamTestResponse(t, statusJSON, &status)
	if !status.Connected || !status.Readonly || status.LoggedIn || status.ServerURL != server.URL {
		t.Fatalf("readonly connection did not survive restart: %s", statusJSON)
	}
}

func TestTeamMobileSyncRequiresInitializedGlossary(t *testing.T) {
	resetMobileTeamTestState()
	resetMobileGlossaryTestState()
	if err := InitializeTeam(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := TeamSync(`{}`); err == nil || !strings.Contains(err.Error(), "InitializeGlossary") {
		t.Fatalf("unexpected missing glossary error: %v", err)
	}
}

type recordedMobileTeamRequest struct {
	Method        string
	EscapedPath   string
	Category      string
	Authorization string
	Body          any
}

func TestTeamMobileProposalAccountAndAdminFacade(t *testing.T) {
	resetMobileTeamTestState()
	var recordsMu sync.Mutex
	var records []recordedMobileTeamRequest
	var failMine atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/login" {
			_, _ = io.WriteString(w, `{"accessToken":"access-secret","refreshToken":"refresh-secret","user":{"id":"admin","username":"admin","displayName":"Admin","role":"superadmin","status":"active"}}`)
			return
		}
		var body any
		raw, _ := io.ReadAll(r.Body)
		if len(strings.TrimSpace(string(raw))) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("decode upstream request body %q: %v", raw, err)
			}
		}
		recordsMu.Lock()
		records = append(records, recordedMobileTeamRequest{
			Method:        r.Method,
			EscapedPath:   r.URL.EscapedPath(),
			Category:      r.URL.Query().Get("category"),
			Authorization: r.Header.Get("Authorization"),
			Body:          body,
		})
		recordsMu.Unlock()

		if failMine.Load() && r.URL.Path == "/api/proposals/mine" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":"forbidden access-secret refresh-secret must-not-leak","accessToken":"nested-secret"}`)
			return
		}
		switch {
		case r.URL.Path == "/api/users":
			_, _ = io.WriteString(w, `[{"id":"u1","username":"member","displayName":"Member","role":"member","status":"active","accessToken":"must-not-leak","nested":{"refresh_token":"must-not-leak","ok":true}}]`)
		case r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `[]`)
		default:
			_, _ = io.WriteString(w, `{"ok":true}`)
		}
	}))
	defer server.Close()

	if err := initializeTeamWithRootCAs(t.TempDir(), mobileTeamTestRoots(t, server)); err != nil {
		t.Fatal(err)
	}
	if _, err := TeamLogin(encodeTeamTestRequest(t, map[string]string{
		"serverUrl": server.URL,
		"username":  "admin",
		"password":  "password",
	})); err != nil {
		t.Fatal(err)
	}

	type facadeCall struct {
		name         string
		call         func(string) (string, error)
		request      string
		method       string
		escapedPath  string
		category     string
		expectedBody string
	}
	calls := []facadeCall{
		{
			name: "create proposal", call: TeamCreateProposal,
			request:      `{"kind":"edit","targetType":"entry","targetId":"entry-1","category":"人名","payload":{"source":"ミク","translation":"未来"},"baseVersion":3}`,
			method:       http.MethodPost,
			escapedPath:  "/api/proposals",
			expectedBody: `{"kind":"edit","targetType":"entry","targetId":"entry-1","category":"人名","payload":{"source":"ミク","translation":"未来"},"baseVersion":3}`,
		},
		{name: "my proposals", call: TeamMyProposals, request: `{}`, method: http.MethodGet, escapedPath: "/api/proposals/mine"},
		{name: "withdraw proposal", call: TeamWithdrawProposal, request: `{"id":" p/1 "}`, method: http.MethodDelete, escapedPath: "/api/proposals/p%2F1"},
		{name: "pending proposals", call: TeamPendingProposals, request: `{"category":"人名&scope=all"}`, method: http.MethodGet, escapedPath: "/api/proposals", category: "人名&scope=all"},
		{name: "approve proposal", call: TeamApproveProposal, request: `{"id":"p/1","note":"ok"}`, method: http.MethodPost, escapedPath: "/api/proposals/p%2F1/approve", expectedBody: `{"note":"ok"}`},
		{name: "reject proposal", call: TeamRejectProposal, request: `{"id":"p/1","note":"needs work"}`, method: http.MethodPost, escapedPath: "/api/proposals/p%2F1/reject", expectedBody: `{"note":"needs work"}`},
		{name: "set reviewer", call: TeamSetReviewer, request: `{"userId":"u1","categories":["人名","地名"]}`, method: http.MethodPost, escapedPath: "/api/admin/reviewers", expectedBody: `{"userId":"u1","categories":["人名","地名"]}`},
		{name: "list admin users", call: TeamListUsers, request: `{}`, method: http.MethodGet, escapedPath: "/api/admin/users"},
		{name: "change password", call: TeamChangePassword, request: `{"oldPassword":"old","newPassword":"new-secret"}`, method: http.MethodPost, escapedPath: "/api/auth/password", expectedBody: `{"oldPassword":"old","newPassword":"new-secret"}`},
		{name: "update profile", call: TeamUpdateProfile, request: `{"displayName":"New Name","avatarColor":"#abcdef"}`, method: http.MethodPost, escapedPath: "/api/me", expectedBody: `{"displayName":"New Name","avatarColor":"#abcdef"}`},
		{name: "account users", call: TeamAccountUsers, request: `{}`, method: http.MethodGet, escapedPath: "/api/users"},
		{name: "create user", call: TeamCreateUser, request: `{"username":"new-user","password":"password","role":"member","displayName":"New User"}`, method: http.MethodPost, escapedPath: "/api/admin/users", expectedBody: `{"username":"new-user","password":"password","role":"member","displayName":"New User"}`},
		{name: "set role", call: TeamSetUserRole, request: `{"id":"u/1","role":"reviewer"}`, method: http.MethodPost, escapedPath: "/api/admin/users/u%2F1/role", expectedBody: `{"role":"reviewer"}`},
		{name: "set status", call: TeamSetUserStatus, request: `{"id":"u/1","status":"disabled"}`, method: http.MethodPost, escapedPath: "/api/admin/users/u%2F1/status", expectedBody: `{"status":"disabled"}`},
		{name: "reset password", call: TeamResetUserPassword, request: `{"id":"u/1","newPassword":"reset-secret"}`, method: http.MethodPost, escapedPath: "/api/admin/users/u%2F1/reset-password", expectedBody: `{"newPassword":"reset-secret"}`},
		{name: "delete user", call: TeamDeleteUser, request: `{"id":"u/1"}`, method: http.MethodDelete, escapedPath: "/api/admin/users/u%2F1"},
		{
			name: "replace glossary", call: TeamGlossaryReplace,
			request:      `{"entries":[{"id":"e1","source":"ミク","translation":"未来","category":"人名","origin":"user"}],"appellations":[],"grammar":[]}`,
			method:       http.MethodPost,
			escapedPath:  "/api/admin/glossary/replace",
			expectedBody: `{"entries":[{"id":"e1","source":"ミク","translation":"未来","category":"人名","origin":"user"}],"appellations":[],"grammar":[]}`,
		},
		{
			name: "bulk import", call: TeamBulkImport,
			request:      `{"entries":[{"id":"e1","source":"ミク","translation":"未来","category":"人名","origin":"user"}],"appellations":[],"grammar":[]}`,
			method:       http.MethodPost,
			escapedPath:  "/api/admin/glossary/bulk-import",
			expectedBody: `{"entries":[{"id":"e1","source":"ミク","translation":"未来","category":"人名","origin":"user"}],"appellations":[],"grammar":[]}`,
		},
	}

	var accountUsersJSON string
	for _, call := range calls {
		response, err := call.call(call.request)
		if err != nil {
			t.Fatalf("%s: %v", call.name, err)
		}
		var responseValue any
		decodeTeamTestResponse(t, response, &responseValue)
		if call.name == "account users" {
			accountUsersJSON = response
		}
	}
	assertNoTeamSecretKeys(t, accountUsersJSON)
	var accountUsers []map[string]any
	decodeTeamTestResponse(t, accountUsersJSON, &accountUsers)
	if len(accountUsers) != 1 || accountUsers[0]["username"] != "member" {
		t.Fatalf("token scrubber damaged account response: %s", accountUsersJSON)
	}
	nested, ok := accountUsers[0]["nested"].(map[string]any)
	if !ok || nested["ok"] != true {
		t.Fatalf("recursive token scrubber damaged safe nested data: %s", accountUsersJSON)
	}

	recordsMu.Lock()
	gotRecords := append([]recordedMobileTeamRequest(nil), records...)
	recordsMu.Unlock()
	if len(gotRecords) != len(calls) {
		t.Fatalf("upstream request count = %d, want %d", len(gotRecords), len(calls))
	}
	for index, call := range calls {
		record := gotRecords[index]
		if record.Method != call.method || record.EscapedPath != call.escapedPath || record.Category != call.category {
			t.Errorf("%s upstream target = %s %s category=%q, want %s %s category=%q", call.name, record.Method, record.EscapedPath, record.Category, call.method, call.escapedPath, call.category)
		}
		if record.Authorization != "Bearer access-secret" {
			t.Errorf("%s authorization = %q", call.name, record.Authorization)
		}
		if call.expectedBody == "" {
			if record.Body != nil {
				t.Errorf("%s body = %#v, want empty", call.name, record.Body)
			}
		} else {
			assertTeamJSONEqual(t, record.Body, call.expectedBody)
		}
	}

	beforeInvalid := len(gotRecords)
	if _, err := TeamDeleteUser(`{"id":"  "}`); err == nil || !strings.Contains(err.Error(), "id is required") {
		t.Fatalf("unexpected empty-id error: %v", err)
	}
	for _, id := range []string{".", "..", "proposal/../admin", "%2e%2e", `%252e%252e`, `proposal\..\admin`} {
		request := encodeTeamTestRequest(t, map[string]string{"id": id})
		if _, err := TeamDeleteUser(request); err == nil || !strings.Contains(err.Error(), "dot path segments") {
			t.Errorf("dot-segment id %q error = %v", id, err)
		}
	}
	if _, err := TeamCreateProposal(`{"kind":`); err == nil || !strings.Contains(err.Error(), "decode team create proposal request") {
		t.Fatalf("unexpected malformed proposal error: %v", err)
	}
	recordsMu.Lock()
	afterInvalid := len(records)
	recordsMu.Unlock()
	if afterInvalid != beforeInvalid {
		t.Fatalf("invalid requests reached remote server: before=%d after=%d", beforeInvalid, afterInvalid)
	}

	failMine.Store(true)
	if _, err := TeamMyProposals(`{}`); err == nil || err.Error() != "team my proposals failed (HTTP 403)" || strings.Contains(err.Error(), "access-secret") || strings.Contains(err.Error(), "refresh-secret") || strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("unexpected sanitized remote error: %v", err)
	}

	if _, err := TeamLogout(`{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := TeamMyProposals(`{}`); !errors.Is(err, service.ErrNotLoggedIn) {
		t.Fatalf("logged-out proxy error = %v, want ErrNotLoggedIn", err)
	}
}
