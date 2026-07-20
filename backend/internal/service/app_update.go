package service

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"sekaitext/backend/internal/fsutil"
)

// DefaultAppUpdateURL is the built-in app-release manifest (same repo as the
// plugin market). Overridable via Settings.AppUpdateURL. The manifest is JSON:
//
//	{
//	  "version": "4.1.0",
//	  "notes":   "本次更新内容…",
//	  "pubDate": "2026-06-28",
//	  "downloads": { "darwin-aarch64": "https://github.com/SnowGlow-aww/SekaiText-Next/releases/download/v4.1.0/SekaiText.dmg" },
//	  "artifacts": { "darwin-aarch64": { "digest": "sha256:...", "size": 123 } }
//	}
//
// The download value points at the project's own OSS-backed edge CDN
// (sakimizuki.accr.cc), the same host that serves this manifest.
const DefaultAppUpdateURL = "https://sakimizuki.accr.cc/sekaitext-plugins/app-release.json"

const maxInstallerSize int64 = 512 << 20

const (
	appUpdateSignatureAlgorithm = "ed25519"
	appUpdateSignatureHeader    = "SekaiText-App-Release-Signature-V1\n"
)

// OfficialAppUpdatePublicKeysJSON is linked into release sidecars by
// scripts/build-go.mjs. An empty trust set disables self-update checks rather
// than accepting an unauthenticated release manifest.
var OfficialAppUpdatePublicKeysJSON string

// CurrentAppVersion is compiled into the backend so update authorization has an
// authoritative floor independent of the client-supplied current query value.
// Release version bumps must update this alongside the package version.
var CurrentAppVersion = "5.9.1"

// AppReleaseManifest is the remote release document (see DefaultAppUpdateURL).
type AppReleaseManifest struct {
	Version   string                        `json:"version"`
	Notes     string                        `json:"notes,omitempty"`
	PubDate   string                        `json:"pubDate,omitempty"`
	Downloads map[string]string             `json:"downloads"`
	Artifacts map[string]AppReleaseArtifact `json:"artifacts"`
	Signature AppReleaseSignature           `json:"signature"`
}

type AppReleaseSignature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"keyId"`
	Value     string `json:"value"`
}

// AppReleaseArtifact augments the legacy downloads URL map without changing its
// shape, so older clients can continue to read the same manifest.
type AppReleaseArtifact struct {
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// AppUpdateInfo is the result of a self-update check.
type AppUpdateInfo struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Notes           string `json:"notes,omitempty"`
	PubDate         string `json:"pubDate,omitempty"`
	DownloadURL     string `json:"downloadUrl,omitempty"`
	Digest          string `json:"digest,omitempty"`
	Size            int64  `json:"size,omitempty"`
	Platform        string `json:"platform"`
}

// AppUpdateService checks the remote app-release manifest and downloads the new
// installer from the project's CDN or canonical GitHub release path.
type AppUpdateService struct {
	fast                  *http.Client // manifest check + fail-fast on a dead mirror
	dl                    *http.Client // installer download — header-timeout fail-fast, long body
	trustedKeys           map[string]ed25519.PublicKey
	keyConfigErr          error
	statePath             string
	stateMu               sync.Mutex
	stateLoaded           bool
	highestVersion        string
	highestManifestDigest string
	installedVersion      string
	writeStateFile        func(string, []byte, os.FileMode) error
	ctx                   context.Context
	cancel                context.CancelFunc
	tasksMu               sync.Mutex
	tasksClosing          bool
	downloadActive        bool
	taskWG                sync.WaitGroup
	publishMu             sync.Mutex // serializes final-path validation and atomic publication
}

func NewAppUpdateService(dataDir string) *AppUpdateService {
	keys, keyConfigErr := parseAppUpdatePublicKeys(OfficialAppUpdatePublicKeysJSON)
	ctx, cancel := context.WithCancel(context.Background())
	manifestClient := newSnapshotHTTPClient()
	manifestClient.Timeout = 25 * time.Second
	downloadClient := newSnapshotHTTPClient()
	downloadClient.Timeout = 30 * time.Minute
	if transport, ok := downloadClient.Transport.(*http.Transport); ok {
		transport.ResponseHeaderTimeout = 25 * time.Second
	}
	return &AppUpdateService{
		fast: manifestClient,
		// Generous total cap so a mirror that returns headers then stalls
		// mid-body cannot hang the download goroutine forever. The snapshot
		// transport also pins the validated public DNS answers at dial time.
		dl:               downloadClient,
		trustedKeys:      keys,
		keyConfigErr:     keyConfigErr,
		statePath:        filepath.Join(dataDir, "app-update-state.json"),
		installedVersion: CurrentAppVersion,
		ctx:              ctx,
		cancel:           cancel,
	}
}

// CurrentPlatform is the manifest download key for this build (e.g. darwin-aarch64).
func CurrentPlatform() string {
	a := runtime.GOARCH
	if a == "arm64" {
		a = "aarch64" // match the Tauri/DMG target-triple naming
	}
	return runtime.GOOS + "-" + a
}

// CheckUpdate fetches the manifest and compares it against the backend's compiled
// version. The client version argument is retained for API compatibility but is
// never used to authorize an update. manifestURL empty → DefaultAppUpdateURL.
func (u *AppUpdateService) CheckUpdate(manifestURL, _ string) (AppUpdateInfo, error) {
	info := AppUpdateInfo{Platform: CurrentPlatform()}
	currentVersion := strings.TrimSpace(u.installedVersion)
	if currentVersion == "" {
		currentVersion = strings.TrimSpace(CurrentAppVersion)
	}
	if comparison, err := compareAppVersions(currentVersion, "0.0.0"); err != nil || comparison <= 0 {
		return info, errors.New("当前应用版本无效，更新检查已禁用")
	}
	info.Current = currentVersion
	if strings.TrimSpace(manifestURL) == "" {
		manifestURL = DefaultAppUpdateURL
	}
	resp, err := u.fetchManifest(manifestURL)
	if err != nil {
		return info, fmt.Errorf("检查更新失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return info, err
	}
	var man AppReleaseManifest
	if err := json.Unmarshal(data, &man); err != nil {
		return info, errors.New("更新清单格式错误")
	}
	if err := u.verifyManifestSignature(&man); err != nil {
		return info, err
	}
	if err := validateAppReleaseManifest(&man); err != nil {
		return info, err
	}
	comparison, err := compareAppVersions(man.Version, currentVersion)
	if err != nil {
		return info, errors.New("当前应用版本或更新清单版本无效")
	}
	if comparison < 0 {
		return info, fmt.Errorf("更新清单版本 %s 低于当前应用版本 %s，已拒绝回滚", man.Version, currentVersion)
	}
	if err := u.observeSignedManifest(&man); err != nil {
		return info, err
	}
	info.Latest = man.Version
	info.Notes = man.Notes
	info.PubDate = man.PubDate
	info.DownloadURL = man.Downloads[info.Platform]
	artifact := man.Artifacts[info.Platform]
	info.Digest = artifact.Digest
	info.Size = artifact.Size
	if comparison > 0 && info.DownloadURL != "" {
		info.UpdateAvailable = true
	}
	return info, nil
}

func (u *AppUpdateService) fetchManifest(rawURL string) (*http.Response, error) {
	if !publicSnapshotURLAllowed(rawURL) {
		return nil, errors.New("更新清单地址必须是公网 HTTPS URL")
	}
	var lastErr error
	for _, candidate := range mirrorCandidates(rawURL) {
		if !publicSnapshotURLAllowed(candidate) {
			lastErr = errors.New("更新清单候选地址不是公网 HTTPS URL")
			continue
		}
		req, err := http.NewRequest(http.MethodGet, candidate, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "SekaiText-AppUpdate")
		resp, err := u.fast.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的更新清单地址")
	}
	return nil, lastErr
}

func parseAppUpdatePublicKeys(raw string) (map[string]ed25519.PublicKey, error) {
	keys := map[string]ed25519.PublicKey{}
	if strings.TrimSpace(raw) == "" {
		return keys, nil
	}
	var encoded map[string]string
	if err := json.Unmarshal([]byte(raw), &encoded); err != nil {
		return nil, errors.New("内置应用更新公钥配置无效")
	}
	for keyID, value := range encoded {
		decoded, err := decodeCanonicalBase64(value)
		if !validSigningKeyID(keyID) || err != nil || len(decoded) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("内置应用更新公钥 %q 无效", keyID)
		}
		keys[keyID] = ed25519.PublicKey(append([]byte(nil), decoded...))
	}
	return keys, nil
}

func (u *AppUpdateService) verifyManifestSignature(man *AppReleaseManifest) error {
	if u.keyConfigErr != nil {
		return u.keyConfigErr
	}
	if len(u.trustedKeys) == 0 {
		return errors.New("应用更新签名公钥未配置，更新检查已禁用")
	}
	if man.Signature.Algorithm != appUpdateSignatureAlgorithm || !validSigningKeyID(man.Signature.KeyID) {
		return errors.New("更新清单签名格式无效")
	}
	key := u.trustedKeys[man.Signature.KeyID]
	if len(key) != ed25519.PublicKeySize {
		return errors.New("更新清单使用了客户端不信任的签名密钥")
	}
	signature, err := base64.StdEncoding.DecodeString(man.Signature.Value)
	if err != nil || base64.StdEncoding.EncodeToString(signature) != man.Signature.Value || len(signature) != ed25519.SignatureSize {
		return errors.New("更新清单签名编码无效")
	}
	if !ed25519.Verify(key, canonicalAppReleasePayload(man), signature) {
		return errors.New("更新清单签名验证失败")
	}
	return nil
}

func canonicalAppReleasePayload(man *AppReleaseManifest) []byte {
	var b strings.Builder
	b.WriteString(appUpdateSignatureHeader)
	canonicalField(&b, "algorithm", man.Signature.Algorithm)
	canonicalField(&b, "keyId", man.Signature.KeyID)
	canonicalField(&b, "version", man.Version)
	canonicalField(&b, "notes", man.Notes)
	canonicalField(&b, "pubDate", man.PubDate)
	platforms := make([]string, 0, len(man.Downloads))
	for platform := range man.Downloads {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	canonicalField(&b, "platformCount", strconv.Itoa(len(platforms)))
	for _, platform := range platforms {
		artifact := man.Artifacts[platform]
		canonicalField(&b, "platform", platform)
		canonicalField(&b, "download", man.Downloads[platform])
		canonicalField(&b, "digest", artifact.Digest)
		canonicalField(&b, "size", strconv.FormatInt(artifact.Size, 10))
	}
	return []byte(b.String())
}

func validateAppReleaseManifest(man *AppReleaseManifest) error {
	if _, err := parseAppVersion(man.Version); err != nil {
		return errors.New("更新清单版本无效")
	}
	if len(man.Downloads) == 0 || len(man.Downloads) != len(man.Artifacts) {
		return errors.New("更新清单平台信息不完整")
	}
	for platform, downloadURL := range man.Downloads {
		artifact, ok := man.Artifacts[platform]
		if !ok {
			return errors.New("更新清单平台信息不完整")
		}
		if officialReleaseSource(downloadURL) == releaseSourceNone {
			return errors.New("更新清单包含非官方安装包地址")
		}
		if _, err := parseSHA256Digest(artifact.Digest); err != nil {
			return errors.New("更新清单缺少有效的 SHA-256 digest")
		}
		if artifact.Size <= 0 || artifact.Size > maxInstallerSize {
			return errors.New("更新清单缺少有效的安装包大小")
		}
	}
	return nil
}

type appUpdateState struct {
	HighestVersion        string `json:"highestVersion"`
	HighestManifestDigest string `json:"highestManifestDigest"`
}

func (u *AppUpdateService) observeSignedManifest(manifest *AppReleaseManifest) error {
	version := manifest.Version
	payloadDigest := sha256.Sum256(canonicalAppReleasePayload(manifest))
	manifestDigest := hex.EncodeToString(payloadDigest[:])
	u.stateMu.Lock()
	defer u.stateMu.Unlock()
	if !u.stateLoaded {
		if u.statePath != "" {
			data, err := os.ReadFile(u.statePath)
			if err == nil {
				var state appUpdateState
				if err := json.Unmarshal(data, &state); err != nil || state.HighestVersion == "" || len(state.HighestManifestDigest) != sha256.Size*2 {
					return errors.New("应用更新防回滚状态损坏")
				}
				if _, err := hex.DecodeString(state.HighestManifestDigest); err != nil {
					return errors.New("应用更新防回滚状态损坏")
				}
				if _, err := parseAppVersion(state.HighestVersion); err != nil {
					return errors.New("应用更新防回滚状态损坏")
				}
				u.highestVersion = state.HighestVersion
				u.highestManifestDigest = state.HighestManifestDigest
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("读取应用更新防回滚状态失败: %w", err)
			}
		}
		u.stateLoaded = true
	}
	if u.highestVersion != "" {
		comparison, _ := compareAppVersions(version, u.highestVersion)
		if comparison < 0 {
			return fmt.Errorf("更新清单版本 %s 低于已验证版本 %s，已拒绝回滚", version, u.highestVersion)
		}
		if comparison == 0 {
			if subtle.ConstantTimeCompare([]byte(manifestDigest), []byte(u.highestManifestDigest)) != 1 {
				return fmt.Errorf("更新清单版本 %s 与已验证的同版本清单不一致，已拒绝替换", version)
			}
			return nil
		}
	}
	if u.statePath != "" {
		data, _ := json.Marshal(appUpdateState{HighestVersion: version, HighestManifestDigest: manifestDigest})
		write := fsutil.WriteFileAtomic
		if u.writeStateFile != nil {
			write = u.writeStateFile
		}
		if err := write(u.statePath, append(data, '\n'), 0o600); err != nil {
			if fsutil.IsWriteCommitted(err) {
				u.highestVersion = version
				u.highestManifestDigest = manifestDigest
			}
			return fmt.Errorf("保存应用更新防回滚状态失败: %w", err)
		}
	}
	u.highestVersion = version
	u.highestManifestDigest = manifestDigest
	return nil
}

type appVersion struct {
	core       [3]uint64
	prerelease []string
}

func parseAppVersion(value string) (appVersion, error) {
	var result appVersion
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	coreAndPrerelease, build, hasBuild := strings.Cut(value, "+")
	if hasBuild {
		if build == "" {
			return result, errors.New("invalid version")
		}
		for _, identifier := range strings.Split(build, ".") {
			if !validAppVersionIdentifier(identifier) {
				return result, errors.New("invalid version")
			}
		}
	}
	value = coreAndPrerelease
	core, prerelease, hasPrerelease := strings.Cut(value, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return result, errors.New("invalid version")
	}
	for i, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return result, errors.New("invalid version")
		}
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return result, errors.New("invalid version")
		}
		result.core[i] = n
	}
	if hasPrerelease {
		result.prerelease = strings.Split(prerelease, ".")
		for _, identifier := range result.prerelease {
			if !validAppVersionIdentifier(identifier) {
				return result, errors.New("invalid version")
			}
			if len(identifier) > 1 && identifier[0] == '0' && isNumericVersionIdentifier(identifier) {
				return result, errors.New("invalid version")
			}
		}
	}
	return result, nil
}

func validAppVersionIdentifier(identifier string) bool {
	if identifier == "" {
		return false
	}
	for _, c := range identifier {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func compareAppVersions(a, b string) (int, error) {
	av, err := parseAppVersion(a)
	if err != nil {
		return 0, err
	}
	bv, err := parseAppVersion(b)
	if err != nil {
		return 0, err
	}
	for i := range av.core {
		if av.core[i] < bv.core[i] {
			return -1, nil
		}
		if av.core[i] > bv.core[i] {
			return 1, nil
		}
	}
	if len(av.prerelease) == 0 || len(bv.prerelease) == 0 {
		switch {
		case len(av.prerelease) == len(bv.prerelease):
			return 0, nil
		case len(av.prerelease) == 0:
			return 1, nil
		default:
			return -1, nil
		}
	}
	for i := 0; i < len(av.prerelease) && i < len(bv.prerelease); i++ {
		if av.prerelease[i] == bv.prerelease[i] {
			continue
		}
		aNumeric := isNumericVersionIdentifier(av.prerelease[i])
		bNumeric := isNumericVersionIdentifier(bv.prerelease[i])
		switch {
		case aNumeric && bNumeric:
			if len(av.prerelease[i]) < len(bv.prerelease[i]) ||
				len(av.prerelease[i]) == len(bv.prerelease[i]) && av.prerelease[i] < bv.prerelease[i] {
				return -1, nil
			}
			return 1, nil
		case aNumeric:
			return -1, nil
		case bNumeric:
			return 1, nil
		case av.prerelease[i] < bv.prerelease[i]:
			return -1, nil
		default:
			return 1, nil
		}
	}
	if len(av.prerelease) < len(bv.prerelease) {
		return -1, nil
	}
	if len(av.prerelease) > len(bv.prerelease) {
		return 1, nil
	}
	return 0, nil
}

func isNumericVersionIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// DownloadUpdate downloads downloadURL into destDir while streaming size and
// SHA-256 verification, reporting progress, and returns the published file path.
func (u *AppUpdateService) DownloadUpdate(downloadURL, digest string, size int64, destDir string, progress DownloadProgressFn) (string, error) {
	if strings.TrimSpace(downloadURL) == "" {
		return "", errors.New("缺少下载地址")
	}
	expectedDigest, err := parseSHA256Digest(digest)
	if err != nil {
		return "", err
	}
	if size <= 0 || size > maxInstallerSize {
		return "", errors.New("安装包大小无效")
	}
	// The downloaded file is later launched via `open`, so restrict the source to
	// this project's exact CDN/GitHub release paths. A tampered manifest cannot
	// point the self-updater at another repository on an otherwise trusted host.
	if officialReleaseSource(downloadURL) == releaseSourceNone {
		return "", errors.New("下载地址的主机不在信任列表内，已拒绝")
	}
	if err := u.beginDownload(); err != nil {
		return "", err
	}
	defer u.endDownload()
	ctx := u.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	resp, err := u.fetchOfficialRelease(ctx, downloadURL)
	if err != nil {
		return "", fmt.Errorf("下载更新失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.ContentLength >= 0 && resp.ContentLength != size {
		return "", fmt.Errorf("安装包大小不匹配: 得到 %d，期望 %d", resp.ContentLength, size)
	}

	if err := os.MkdirAll(destDir, 0700); err != nil {
		return "", err
	}
	dest := filepath.Join(destDir, verifiedUpdateFileName(downloadURL, expectedDigest))

	// Write to a unique temp file then rename so a mid-stream failure never leaves a
	// file the installer/open step would mistake for a complete download. A unique
	// name (not the fixed dest+".part") is required because AppUpdateDownload spawns
	// one goroutine per request with no single-flight guard, so two concurrent
	// downloads of the same URL would otherwise clobber each other's .part and
	// interleave into a corrupted installer (mirrors DownloadJSONToDir/downloader.go).
	f, err := os.CreateTemp(destDir, filepath.Base(dest)+".*.part")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	var reader io.Reader = resp.Body
	if progress != nil {
		reader = &progressReader{reader: resp.Body, total: size, fn: progress}
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(reader, size+1))
	if err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if n != size {
		f.Close()
		os.Remove(tmp)
		return "", fmt.Errorf("安装包大小不匹配: 得到 %d，期望 %d", n, size)
	}
	if subtle.ConstantTimeCompare(hash.Sum(nil), expectedDigest) != 1 {
		f.Close()
		os.Remove(tmp)
		return "", errors.New("安装包 SHA-256 校验失败")
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	// Digest-derived names keep concurrently downloaded releases with the same
	// upstream filename from replacing one another. Publication is serialized so
	// same-digest callers also behave consistently on platforms where Rename cannot
	// replace an existing file.
	u.publishMu.Lock()
	defer u.publishMu.Unlock()
	if _, err := os.Stat(dest); err == nil {
		if err := VerifyUpdateFile(dest, digest, size); err == nil {
			os.Remove(tmp)
			return dest, nil
		}
		if err := os.Remove(dest); err != nil {
			os.Remove(tmp)
			return "", err
		}
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return dest, nil
}

func (u *AppUpdateService) beginDownload() error {
	u.tasksMu.Lock()
	defer u.tasksMu.Unlock()
	if u.tasksClosing {
		return errors.New("应用正在退出，无法开始更新下载")
	}
	if u.downloadActive {
		return errors.New("已有应用更新正在下载")
	}
	u.downloadActive = true
	u.taskWG.Add(1)
	return nil
}

func (u *AppUpdateService) endDownload() {
	u.tasksMu.Lock()
	u.downloadActive = false
	u.tasksMu.Unlock()
	u.taskWG.Done()
}

func (u *AppUpdateService) Shutdown(ctx context.Context) error {
	u.tasksMu.Lock()
	u.tasksClosing = true
	if u.cancel != nil {
		u.cancel()
	}
	u.tasksMu.Unlock()
	done := make(chan struct{})
	go func() {
		u.taskWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// VerifyUpdateFile rechecks a completed artifact before it is handed to the OS
// installer launcher. It deliberately performs both checks again to catch a file
// that was replaced or truncated after the download task completed.
func VerifyUpdateFile(filePath, digest string, size int64) error {
	expectedDigest, err := parseSHA256Digest(digest)
	if err != nil {
		return err
	}
	if size <= 0 || size > maxInstallerSize {
		return errors.New("安装包大小无效")
	}
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return errors.New("安装包大小不匹配")
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(f, size+1))
	if err != nil {
		return err
	}
	if n != size {
		return errors.New("安装包大小不匹配")
	}
	if subtle.ConstantTimeCompare(hash.Sum(nil), expectedDigest) != 1 {
		return errors.New("安装包 SHA-256 校验失败")
	}
	return nil
}

func parseSHA256Digest(digest string) ([]byte, error) {
	digest = strings.TrimSpace(digest)
	if algorithm, value, ok := strings.Cut(digest, ":"); ok {
		if !strings.EqualFold(algorithm, "sha256") {
			return nil, errors.New("安装包 digest 算法无效")
		}
		digest = value
	}
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		return nil, errors.New("安装包 SHA-256 digest 无效")
	}
	return decoded, nil
}

// updateFileName derives a safe, separator-free local filename from the URL.
func updateFileName(rawurl string) string {
	name := "SekaiText-update"
	if u, err := url.Parse(rawurl); err == nil {
		// u.Path is percent-decoded, so a URL ending in "/.." (or "%2e%2e") yields
		// base=="..", which filepath.Join would resolve to the PARENT of destDir.
		// Reject ".." (and anything with a separator) and keep the default name.
		if base := filepath.Base(u.Path); base != "." && base != ".." && base != "/" && base != "" && !strings.ContainsAny(base, `/\`) {
			name = base
		}
	}
	return filepath.Base(name)
}

func verifiedUpdateFileName(rawurl string, digest []byte) string {
	name := updateFileName(rawurl)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	return stem + "-" + hex.EncodeToString(digest[:6]) + ext
}

type releaseSource uint8

const (
	releaseSourceNone releaseSource = iota
	releaseSourceCDN
	releaseSourceGitHub
)

const (
	cdnReleasePath    = "/sekaitext-releases/"
	githubReleasePath = "/snowglow-aww/sekaitext-next/releases/download/"
)

// officialReleaseSource accepts only immutable release locations controlled by
// this project. Generic GitHub URLs and GitHub's opaque asset hosts are not valid
// manifest targets; asset hosts are considered only after the redirect chain has
// passed through the project's canonical GitHub release URL.
func officialReleaseSource(rawurl string) releaseSource {
	u, err := url.Parse(rawurl)
	if err != nil || !validHTTPSURL(u) || u.Fragment != "" || pathpkg.Clean(u.Path) != u.Path {
		return releaseSourceNone
	}
	switch strings.ToLower(u.Hostname()) {
	case "sakimizuki.accr.cc":
		if hasReleaseAssetPath(u.Path, cdnReleasePath, false) {
			return releaseSourceCDN
		}
	case "github.com":
		if hasReleaseAssetPath(u.Path, githubReleasePath, true) {
			return releaseSourceGitHub
		}
	}
	return releaseSourceNone
}

func validHTTPSURL(u *url.URL) bool {
	return u != nil && strings.EqualFold(u.Scheme, "https") && u.User == nil &&
		u.Hostname() != "" && (u.Port() == "" || u.Port() == "443")
}

func hasReleaseAssetPath(value, prefix string, foldCase bool) bool {
	if foldCase {
		value = strings.ToLower(value)
	}
	rest, ok := strings.CutPrefix(value, prefix)
	if !ok {
		return false
	}
	parts := strings.Split(rest, "/")
	return len(parts) >= 2 && parts[0] != "" && parts[len(parts)-1] != ""
}

func isGitHubReleaseAssetURL(u *url.URL) bool {
	if !validHTTPSURL(u) || u.Fragment != "" {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return u.Path != ""
	default:
		return false
	}
}

var errUnsafeUpdateRedirect = errors.New("更新下载重定向不安全")

func releaseRedirectAllowed(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return fmt.Errorf("%w: 来源无效", errUnsafeUpdateRedirect)
	}
	if len(via) >= 10 {
		return fmt.Errorf("%w: 次数过多", errUnsafeUpdateRedirect)
	}
	if officialReleaseSource(req.URL.String()) != releaseSourceNone {
		return nil
	}
	if !isGitHubReleaseAssetURL(req.URL) {
		return fmt.Errorf("%w: 目标不是官方地址", errUnsafeUpdateRedirect)
	}
	seenGitHubRelease := false
	for _, previous := range via {
		source := officialReleaseSource(previous.URL.String())
		if source == releaseSourceGitHub {
			seenGitHubRelease = true
			continue
		}
		if source == releaseSourceNone && (!seenGitHubRelease || !isGitHubReleaseAssetURL(previous.URL)) {
			return fmt.Errorf("%w: 重定向链无效", errUnsafeUpdateRedirect)
		}
	}
	if !seenGitHubRelease {
		return fmt.Errorf("%w: GitHub asset 缺少官方 release 来源", errUnsafeUpdateRedirect)
	}
	return nil
}

func (u *AppUpdateService) fetchOfficialRelease(ctx context.Context, rawurl string) (*http.Response, error) {
	var lastErr error
	for _, candidate := range routeDownloadURL(rawurl) {
		if officialReleaseSource(candidate) == releaseSourceNone {
			lastErr = errors.New("更新下载候选地址无效")
			continue
		}
		client := *u.dl
		client.CheckRedirect = releaseRedirectAllowed
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, candidate, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "SekaiText-AppUpdate")
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(err, errUnsafeUpdateRedirect) {
				return nil, err
			}
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的官方更新下载地址")
	}
	return nil, lastErr
}
