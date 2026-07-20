package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sekaitext/backend/internal/fsutil"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCheckUpdateCarriesArtifactIntegrity(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	platform := CurrentPlatform()
	sign, trustedKeys := newTestAppManifestSigner(t)
	manifest := sign(`{"version":"99.0.0","downloads":{"` + platform + `":"https://sakimizuki.accr.cc/sekaitext-releases/v99.0.0/SekaiText.dmg"},"artifacts":{"` + platform + `":{"digest":"` + digest + `","size":1234}}}`)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(manifest)),
			Header:     make(http.Header),
		}, nil
	})}
	updates := &AppUpdateService{fast: client, dl: client, trustedKeys: trustedKeys}

	info, err := updates.CheckUpdate("https://manifest.example/app-release.json", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if info.Digest != digest || info.Size != 1234 {
		t.Fatalf("integrity metadata = (%q, %d), want (%q, 1234)", info.Digest, info.Size, digest)
	}
}

func TestCheckUpdateRejectsReleaseWithoutUsableIntegrity(t *testing.T) {
	platform := CurrentPlatform()
	validDigest := "sha256:" + strings.Repeat("a", 64)
	tests := []struct {
		name     string
		artifact string
		url      string
	}{
		{name: "missing artifact", artifact: `{}`, url: "https://sakimizuki.accr.cc/sekaitext-releases/v99/SekaiText.dmg"},
		{name: "missing size", artifact: `{"digest":"` + validDigest + `"}`, url: "https://sakimizuki.accr.cc/sekaitext-releases/v99/SekaiText.dmg"},
		{name: "invalid digest", artifact: `{"digest":"sha512:bad","size":10}`, url: "https://sakimizuki.accr.cc/sekaitext-releases/v99/SekaiText.dmg"},
		{name: "unofficial URL", artifact: `{"digest":"` + validDigest + `","size":10}`, url: "https://evil.example/SekaiText.dmg"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sign, trustedKeys := newTestAppManifestSigner(t)
			manifest := sign(`{"version":"99.0.0","downloads":{"` + platform + `":"` + tc.url + `"},"artifacts":{"` + platform + `":` + tc.artifact + `}}`)
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(manifest)),
					Header:     make(http.Header),
				}, nil
			})}
			updates := &AppUpdateService{fast: client, dl: client, trustedKeys: trustedKeys}
			if _, err := updates.CheckUpdate("https://manifest.example/app-release.json", "1.0.0"); err == nil {
				t.Fatal("expected malformed release to be rejected")
			}
		})
	}
}

func TestCheckUpdateRejectsTamperedOrRolledBackManifest(t *testing.T) {
	sign, trustedKeys := newTestAppManifestSigner(t)
	platform := CurrentPlatform()
	manifestFor := func(version string) string {
		return sign(`{"version":"` + version + `","downloads":{"` + platform + `":"https://sakimizuki.accr.cc/sekaitext-releases/v` + version + `/SekaiText.dmg"},"artifacts":{"` + platform + `":{"digest":"sha256:` + strings.Repeat("a", 64) + `","size":1234}}}`)
	}
	manifest := manifestFor("99.0.0")
	currentBody := manifest
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(currentBody)), Header: make(http.Header)}, nil
	})}
	updates := &AppUpdateService{fast: client, dl: client, trustedKeys: trustedKeys}
	if _, err := updates.CheckUpdate("https://manifest.example/app-release.json", "1.0.0"); err != nil {
		t.Fatal(err)
	}

	var tampered map[string]any
	if err := json.Unmarshal([]byte(manifest), &tampered); err != nil {
		t.Fatal(err)
	}
	tampered["version"] = "100.0.0"
	tamperedBytes, _ := json.Marshal(tampered)
	currentBody = string(tamperedBytes)
	if _, err := updates.CheckUpdate("https://manifest.example/app-release.json", "1.0.0"); err == nil {
		t.Fatal("expected tampered manifest signature to be rejected")
	}

	currentBody = manifestFor("98.0.0")
	if _, err := updates.CheckUpdate("https://manifest.example/app-release.json", "1.0.0"); err == nil || !strings.Contains(err.Error(), "回滚") {
		t.Fatalf("rollback error = %v", err)
	}
}

func TestCheckUpdateUsesServerFloorAndRejectsFreshReplay(t *testing.T) {
	sign, trustedKeys := newTestAppManifestSigner(t)
	platform := CurrentPlatform()
	manifestFor := func(version string) string {
		return sign(`{"version":"` + version + `","downloads":{"` + platform + `":"https://sakimizuki.accr.cc/sekaitext-releases/v` + version + `/SekaiText.dmg"},"artifacts":{"` + platform + `":{"digest":"sha256:` + strings.Repeat("a", 64) + `","size":1234}}}`)
	}
	body := manifestFor("5.8.0")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	updates := &AppUpdateService{
		fast: client, dl: client, trustedKeys: trustedKeys,
		installedVersion: "5.9.0", statePath: filepath.Join(t.TempDir(), "app-update-state.json"),
	}
	if _, err := updates.CheckUpdate("https://manifest.example/app-release.json", "0.0.0"); err == nil || !strings.Contains(err.Error(), "回滚") {
		t.Fatalf("fresh replay below the server floor was accepted: %v", err)
	}

	body = manifestFor("6.0.0")
	info, err := updates.CheckUpdate("https://manifest.example/app-release.json", "0.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if info.Current != "5.9.0" || !info.UpdateAvailable {
		t.Fatalf("server-authorized update = %+v", info)
	}
	body = manifestFor("6.1.0")
	info, err = updates.CheckUpdate("https://manifest.example/app-release.json", "999.0.0")
	if err != nil || info.Current != "5.9.0" || !info.UpdateAvailable {
		t.Fatalf("client version influenced authorization: info=%+v err=%v", info, err)
	}
	body = manifestFor("5.9.0")
	if _, err := updates.CheckUpdate("https://manifest.example/app-release.json", "0.0.0"); err == nil || !strings.Contains(err.Error(), "回滚") {
		t.Fatalf("replay below observed high-water mark was accepted: %v", err)
	}
}

func TestAppUpdateSignatureSharedNodeFixture(t *testing.T) {
	data, err := os.ReadFile("../../../scripts/fixtures/app-release-signature-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		PublicKey        string             `json:"publicKey"`
		CanonicalPayload string             `json:"canonicalPayload"`
		Manifest         AppReleaseManifest `json:"manifest"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	publicKey, err := base64.StdEncoding.DecodeString(fixture.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	updates := &AppUpdateService{trustedKeys: map[string]ed25519.PublicKey{fixture.Manifest.Signature.KeyID: publicKey}}
	if err := updates.verifyManifestSignature(&fixture.Manifest); err != nil {
		t.Fatal(err)
	}
	if got := base64.StdEncoding.EncodeToString(canonicalAppReleasePayload(&fixture.Manifest)); got != fixture.CanonicalPayload {
		t.Fatalf("canonical payload differs from Node fixture")
	}
}

func TestAppUpdateRollbackStatePersistsAcrossServiceRestart(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "app-update-state.json")
	first := &AppUpdateService{statePath: statePath}
	if err := first.observeSignedManifest(&AppReleaseManifest{Version: "7.2.0"}); err != nil {
		t.Fatal(err)
	}
	second := &AppUpdateService{statePath: statePath}
	if err := second.observeSignedManifest(&AppReleaseManifest{Version: "7.1.9"}); err == nil || !strings.Contains(err.Error(), "回滚") {
		t.Fatalf("persisted rollback error = %v", err)
	}
}

func TestAppUpdatePostCommitErrorAdvancesInMemoryHighWater(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "app-update-state.json")
	wantErr := errors.New("directory sync failed")
	updates := &AppUpdateService{
		statePath: statePath,
		writeStateFile: func(path string, data []byte, perm os.FileMode) error {
			if err := os.WriteFile(path, data, perm); err != nil {
				return err
			}
			return &fsutil.PostCommitError{Err: wantErr}
		},
	}
	if err := updates.observeSignedManifest(&AppReleaseManifest{Version: "7.2.0"}); !errors.Is(err, wantErr) {
		t.Fatalf("post-commit error = %v, want %v", err, wantErr)
	}
	if updates.highestVersion != "7.2.0" {
		t.Fatalf("in-memory high-water = %q, want 7.2.0", updates.highestVersion)
	}
	if err := updates.observeSignedManifest(&AppReleaseManifest{Version: "7.1.9"}); err == nil || !strings.Contains(err.Error(), "回滚") {
		t.Fatalf("lower manifest after committed warning was accepted: %v", err)
	}
	data, err := os.ReadFile(statePath)
	if err != nil || !strings.Contains(string(data), `"highestVersion":"7.2.0"`) {
		t.Fatalf("committed state = %q, %v", data, err)
	}
}

func TestAppUpdateRejectsDifferentManifestAtSameVersion(t *testing.T) {
	updates := &AppUpdateService{}
	first := &AppReleaseManifest{Version: "7.2.0", Notes: "first"}
	if err := updates.observeSignedManifest(first); err != nil {
		t.Fatal(err)
	}
	if err := updates.observeSignedManifest(first); err != nil {
		t.Fatalf("identical manifest was rejected: %v", err)
	}
	changed := &AppReleaseManifest{Version: "7.2.0", Notes: "changed"}
	if err := updates.observeSignedManifest(changed); err == nil || !strings.Contains(err.Error(), "同版本") {
		t.Fatalf("same-version replacement error = %v", err)
	}
}

func TestParseAppVersionRejectsInvalidBuildMetadata(t *testing.T) {
	for _, version := range []string{"1.2.3+", "1.2.3+bad!", "1.2.3+one+two", "1.2.3+one..two"} {
		if _, err := parseAppVersion(version); err == nil {
			t.Errorf("parseAppVersion(%q) accepted invalid build metadata", version)
		}
	}
	if _, err := parseAppVersion("1.2.3-beta.1+build.007"); err != nil {
		t.Fatalf("valid SemVer rejected: %v", err)
	}
}

func newTestAppManifestSigner(t *testing.T) (func(string) string, map[string]ed25519.PublicKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const keyID = "test-app-update"
	return func(raw string) string {
		t.Helper()
		var manifest AppReleaseManifest
		if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
			t.Fatal(err)
		}
		manifest.Signature = AppReleaseSignature{Algorithm: appUpdateSignatureAlgorithm, KeyID: keyID}
		manifest.Signature.Value = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, canonicalAppReleasePayload(&manifest)))
		encoded, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}, map[string]ed25519.PublicKey{keyID: publicKey}
}

func TestDownloadUpdateVerifiesAndPublishesInstaller(t *testing.T) {
	payload := []byte("verified installer")
	sum := sha256.Sum256(payload)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Body:          io.NopCloser(strings.NewReader(string(payload))),
			Header:        make(http.Header),
			ContentLength: int64(len(payload)),
		}, nil
	})}
	updates := &AppUpdateService{fast: client, dl: client}

	got, err := updates.DownloadUpdate(
		"https://sakimizuki.accr.cc/sekaitext-releases/v99.0.0/SekaiText.dmg",
		digest,
		int64(len(payload)),
		t.TempDir(),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(payload) {
		t.Fatalf("downloaded installer = %q, want %q", data, payload)
	}
}

func TestDownloadUpdateRejectsInvalidOrMismatchedIntegrity(t *testing.T) {
	payload := []byte("installer bytes")
	sum := sha256.Sum256(payload)
	validDigest := "sha256:" + hex.EncodeToString(sum[:])
	tests := []struct {
		name   string
		digest string
		size   int64
	}{
		{name: "missing digest", size: int64(len(payload))},
		{name: "invalid digest", digest: "sha512:" + strings.Repeat("a", 64), size: int64(len(payload))},
		{name: "missing size", digest: validDigest},
		{name: "size mismatch", digest: validDigest, size: int64(len(payload) + 1)},
		{name: "sha mismatch", digest: "sha256:" + strings.Repeat("0", 64), size: int64(len(payload))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode:    http.StatusOK,
					Body:          io.NopCloser(strings.NewReader(string(payload))),
					Header:        make(http.Header),
					ContentLength: int64(len(payload)),
				}, nil
			})}
			updates := &AppUpdateService{fast: client, dl: client}
			_, err := updates.DownloadUpdate(
				"https://sakimizuki.accr.cc/sekaitext-releases/v99.0.0/SekaiText.dmg",
				tc.digest,
				tc.size,
				dir,
				nil,
			)
			if err == nil {
				t.Fatal("expected integrity error")
			}
			entries, readErr := os.ReadDir(dir)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("failed download left files behind: %v", entries)
			}
		})
	}
}

func TestDownloadUpdateRejectsUnofficialInitialURLs(t *testing.T) {
	payload := []byte("installer")
	sum := sha256.Sum256(payload)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	urls := []string{
		"http://sakimizuki.accr.cc/sekaitext-releases/v1/SekaiText.dmg",
		"https://sakimizuki.accr.cc/sekaitext-plugins/SekaiText.dmg",
		"https://github.com/attacker/SekaiText-Next/releases/download/v1/SekaiText.dmg",
		"https://github.com/SnowGlow-aww/SekaiText-Next/archive/v1.zip",
		"https://release-assets.githubusercontent.com/github-production-release-asset/opaque",
	}
	for _, downloadURL := range urls {
		t.Run(downloadURL, func(t *testing.T) {
			requests := 0
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				requests++
				return &http.Response{
					StatusCode:    http.StatusOK,
					Body:          io.NopCloser(strings.NewReader(string(payload))),
					Header:        make(http.Header),
					ContentLength: int64(len(payload)),
				}, nil
			})}
			updates := &AppUpdateService{fast: client, dl: client}
			if _, err := updates.DownloadUpdate(downloadURL, digest, int64(len(payload)), t.TempDir(), nil); err == nil {
				t.Fatal("expected URL policy error")
			}
			if requests != 0 {
				t.Fatalf("unofficial URL reached the network %d time(s)", requests)
			}
		})
	}
}

func TestDownloadUpdateAllowsGitHubReleaseAssetRedirect(t *testing.T) {
	previousMode := DownloadMirrorMode()
	SetDownloadMirror("github")
	t.Cleanup(func() { SetDownloadMirror(previousMode) })

	payload := []byte("github release installer")
	sum := sha256.Sum256(payload)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Hostname() {
		case "github.com":
			return redirectResponse("https://release-assets.githubusercontent.com/github-production-release-asset/123/installer.dmg?sp=r"), nil
		case "release-assets.githubusercontent.com":
			return &http.Response{
				StatusCode:    http.StatusOK,
				Body:          io.NopCloser(strings.NewReader(string(payload))),
				Header:        make(http.Header),
				ContentLength: int64(len(payload)),
			}, nil
		default:
			t.Fatalf("unexpected request to %s", req.URL)
			return nil, nil
		}
	})}
	updates := &AppUpdateService{fast: client, dl: client}
	if _, err := updates.DownloadUpdate(
		"https://github.com/SnowGlow-aww/SekaiText-Next/releases/download/v99.0.0/SekaiText.dmg",
		digest,
		int64(len(payload)),
		t.TempDir(),
		nil,
	); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadUpdateRevalidatesEveryRedirect(t *testing.T) {
	payload := []byte("redirected installer")
	sum := sha256.Sum256(payload)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	tests := []struct {
		name string
		url  string
		mode string
		next map[string]string
	}{
		{
			name: "CDN cannot leave official hosts",
			url:  "https://sakimizuki.accr.cc/sekaitext-releases/v1/SekaiText.dmg",
			mode: "cdn",
			next: map[string]string{"sakimizuki.accr.cc": "https://evil.example/installer.dmg"},
		},
		{
			name: "GitHub asset chain cannot leave asset hosts",
			url:  "https://github.com/SnowGlow-aww/SekaiText-Next/releases/download/v1/SekaiText.dmg",
			mode: "github",
			next: map[string]string{
				"github.com":                           "https://release-assets.githubusercontent.com/github-production-release-asset/123/installer.dmg",
				"release-assets.githubusercontent.com": "https://evil.example/installer.dmg",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			previousMode := DownloadMirrorMode()
			SetDownloadMirror(tc.mode)
			t.Cleanup(func() { SetDownloadMirror(previousMode) })
			reachedEvil := false
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Hostname() == "evil.example" {
					reachedEvil = true
				}
				if location := tc.next[req.URL.Hostname()]; location != "" {
					return redirectResponse(location), nil
				}
				return &http.Response{
					StatusCode:    http.StatusOK,
					Body:          io.NopCloser(strings.NewReader(string(payload))),
					Header:        make(http.Header),
					ContentLength: int64(len(payload)),
				}, nil
			})}
			updates := &AppUpdateService{fast: client, dl: client}
			if _, err := updates.DownloadUpdate(tc.url, digest, int64(len(payload)), t.TempDir(), nil); err == nil {
				t.Fatal("expected redirect policy error")
			}
			if reachedEvil {
				t.Fatal("redirect policy allowed a request to the untrusted host")
			}
		})
	}
}

func redirectResponse(location string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusFound,
		Body:       io.NopCloser(strings.NewReader("redirect")),
		Header:     http.Header{"Location": []string{location}},
	}
}

func TestConcurrentDownloadsAreSingleFlight(t *testing.T) {
	payload := []byte("installer release A")
	started := make(chan struct{})
	release := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		close(started)
		<-release
		return &http.Response{
			StatusCode:    http.StatusOK,
			Body:          io.NopCloser(strings.NewReader(string(payload))),
			Header:        make(http.Header),
			ContentLength: int64(len(payload)),
		}, nil
	})}
	updates := &AppUpdateService{fast: client, dl: client}
	dir := t.TempDir()
	sum := sha256.Sum256(payload)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	result := make(chan error, 1)
	go func() {
		_, err := updates.DownloadUpdate(
			"https://sakimizuki.accr.cc/sekaitext-releases/v1/SekaiText.dmg",
			digest, int64(len(payload)), dir, nil,
		)
		result <- err
	}()
	<-started
	if _, err := updates.DownloadUpdate(
		"https://sakimizuki.accr.cc/sekaitext-releases/v1/SekaiText.dmg",
		digest, int64(len(payload)), dir, nil,
	); err == nil || !strings.Contains(err.Error(), "正在下载") {
		t.Fatalf("concurrent download error = %v", err)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".part") {
			t.Fatalf("temporary file remains after download: %s", entry.Name())
		}
	}
}

func TestAppUpdateShutdownCancelsActiveDownload(t *testing.T) {
	payload := []byte("installer")
	sum := sha256.Sum256(payload)
	started := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		close(started)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	ctx, cancel := context.WithCancel(context.Background())
	updates := &AppUpdateService{fast: client, dl: client, ctx: ctx, cancel: cancel}
	dir := t.TempDir()
	done := make(chan error, 1)
	go func() {
		_, err := updates.DownloadUpdate(
			"https://sakimizuki.accr.cc/sekaitext-releases/v1/SekaiText.dmg",
			"sha256:"+hex.EncodeToString(sum[:]), int64(len(payload)), dir, nil,
		)
		done <- err
	}()
	<-started
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()
	if err := updates.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("download error after shutdown = %v", err)
	}
}
