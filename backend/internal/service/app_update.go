package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DefaultAppUpdateURL is the built-in app-release manifest (same repo as the
// plugin market). Overridable via Settings.AppUpdateURL. The manifest is JSON:
//
//	{
//	  "version": "4.1.0",
//	  "notes":   "本次更新内容…",
//	  "pubDate": "2026-06-28",
//	  "downloads": { "darwin-aarch64": "https://github.com/<o>/<r>/releases/download/v4.1.0/SekaiText.dmg" }
//	}
//
// The download value points at the project's own OSS-backed edge CDN
// (sakimizuki.accr.cc), the same host that serves this manifest.
const DefaultAppUpdateURL = "https://sakimizuki.accr.cc/sekaitext-plugins/app-release.json"

// AppReleaseManifest is the remote release document (see DefaultAppUpdateURL).
type AppReleaseManifest struct {
	Version   string            `json:"version"`
	Notes     string            `json:"notes,omitempty"`
	PubDate   string            `json:"pubDate,omitempty"`
	Downloads map[string]string `json:"downloads"`
}

// AppUpdateInfo is the result of a self-update check.
type AppUpdateInfo struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Notes           string `json:"notes,omitempty"`
	PubDate         string `json:"pubDate,omitempty"`
	DownloadURL     string `json:"downloadUrl,omitempty"`
	Platform        string `json:"platform"`
}

// AppUpdateService checks the remote app-release manifest and downloads the new
// installer from the project's edge CDN (mirrorFetch falls back to the origin URL).
type AppUpdateService struct {
	fast *http.Client // manifest check + fail-fast on a dead mirror
	dl   *http.Client // installer download — header-timeout fail-fast, long body
}

func NewAppUpdateService() *AppUpdateService {
	return &AppUpdateService{
		fast: &http.Client{Timeout: 25 * time.Second},
		dl: &http.Client{
			// Generous total cap so a mirror that returns headers then stalls
			// mid-body can't hang the download goroutine forever (installers are
			// ≤512 MiB, so 30 min covers slow links). ResponseHeaderTimeout still
			// fails a dead mirror fast, before any byte arrives.
			Timeout: 30 * time.Minute,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				ResponseHeaderTimeout: 25 * time.Second,
			},
		},
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

// CheckUpdate fetches the manifest and compares it against current. manifestURL
// empty → DefaultAppUpdateURL.
func (u *AppUpdateService) CheckUpdate(manifestURL, current string) (AppUpdateInfo, error) {
	info := AppUpdateInfo{Current: current, Platform: CurrentPlatform()}
	if strings.TrimSpace(manifestURL) == "" {
		manifestURL = DefaultAppUpdateURL
	}
	resp, err := mirrorFetch(u.fast, u.fast, manifestURL)
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
	info.Latest = man.Version
	info.Notes = man.Notes
	info.PubDate = man.PubDate
	info.DownloadURL = man.Downloads[info.Platform]
	info.UpdateAvailable = versionNewer(man.Version, current) && info.DownloadURL != ""
	return info, nil
}

// DownloadUpdate downloads downloadURL (mirror-aware) into destDir, reporting
// progress, and returns the saved file path (filename derived from the URL).
func (u *AppUpdateService) DownloadUpdate(downloadURL, destDir string, progress DownloadProgressFn) (string, error) {
	if strings.TrimSpace(downloadURL) == "" {
		return "", errors.New("缺少下载地址")
	}
	// The downloaded file is later launched via `open`, so restrict the source to
	// trusted release hosts (our edge CDN / GitHub): a tampered/attacker manifest
	// can't point the self-updater at arbitrary bytes.
	if !isTrustedReleaseHost(downloadURL) {
		return "", errors.New("下载地址的主机不在信任列表内，已拒绝")
	}
	resp, err := mirrorFetch(u.dl, u.dl, downloadURL)
	if err != nil {
		return "", fmt.Errorf("下载更新失败: %w", err)
	}
	defer resp.Body.Close()

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}
	dest := filepath.Join(destDir, updateFileName(downloadURL))

	// Write to .part then rename so a mid-stream failure never leaves a file the
	// installer/open step would mistake for a complete download.
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	var reader io.Reader = resp.Body
	if progress != nil {
		reader = &progressReader{reader: resp.Body, total: resp.ContentLength, fn: progress}
	}
	// Cap at 512 MiB — installers are far smaller; guards against a huge body.
	if _, err := io.Copy(f, io.LimitReader(reader, 512<<20)); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return dest, nil
}

// updateFileName derives a safe, separator-free local filename from the URL.
func updateFileName(rawurl string) string {
	name := "SekaiText-update"
	if u, err := url.Parse(rawurl); err == nil {
		if base := filepath.Base(u.Path); base != "." && base != "/" && base != "" {
			name = base
		}
	}
	return filepath.Base(name)
}

// isTrustedReleaseHost restricts installer downloads to the project's own edge CDN
// and GitHub release hosts so a tampered/attacker manifest can't point the
// self-updater at arbitrary bytes.
func isTrustedReleaseHost(rawurl string) bool {
	u, err := url.Parse(rawurl)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	switch h {
	case "sakimizuki.accr.cc",
		"github.com", "api.github.com", "codeload.github.com", "objects.githubusercontent.com", "raw.githubusercontent.com":
		return true
	}
	return strings.HasSuffix(h, ".githubusercontent.com")
}
