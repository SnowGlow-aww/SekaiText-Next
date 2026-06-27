package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DefaultMarketURL is the built-in plugin marketplace index. Overridable via
// Settings.PluginMarketURL.
const DefaultMarketURL = "https://raw.githubusercontent.com/snowglow-aww/sekaitext-plugins/main/index.json"

// GitHubProxyPrefix accelerates plugin-market downloads on networks where direct
// github.com / raw.githubusercontent.com access is slow or flaky. GitHub URLs are
// tried through this mirror first and fall back to the original GitHub URL when the
// mirror errors or returns non-200 (see MarketService.fetch). Keep the trailing /.
const GitHubProxyPrefix = "https://ghfast.top/"

// githubHosts are the GitHub-owned hosts worth routing through the mirror. Other
// hosts (a self-hosted index/CDN via Settings.PluginMarketURL) are left untouched.
var githubHosts = map[string]bool{
	"github.com":                    true,
	"raw.githubusercontent.com":     true,
	"objects.githubusercontent.com": true,
	"codeload.github.com":           true,
	"gist.githubusercontent.com":    true,
}

// mirrorCandidates returns the URLs to try in order: the ghfast.top mirror first
// (GitHub hosts only), then the original. A non-GitHub or unparseable URL yields
// just itself, so a self-hosted index/CDN is unaffected.
func mirrorCandidates(rawurl string) []string {
	u, err := url.Parse(rawurl)
	if err != nil || !githubHosts[strings.ToLower(u.Hostname())] {
		return []string{rawurl}
	}
	// ghfast.top expects the full original URL (scheme included) appended.
	return []string{GitHubProxyPrefix + rawurl, rawurl}
}

// mirrorFetch GETs rawurl, trying each mirrorCandidates URL in order until one
// returns HTTP 200. firstClient (shorter timeout) is used for every candidate
// except the last so a dead mirror fails over fast; lastClient (full timeout) is
// used for the final official attempt. The response Body is the caller's to close.
func mirrorFetch(firstClient, lastClient *http.Client, rawurl string) (*http.Response, error) {
	candidates := mirrorCandidates(rawurl)
	var lastErr error
	for i, cand := range candidates {
		client := lastClient
		if i < len(candidates)-1 {
			client = firstClient // a fallback remains → don't wait the full timeout
		}
		req, err := http.NewRequest(http.MethodGet, cand, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "SekaiText-PluginMarket")
		resp, err := client.Do(req)
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
		lastErr = errors.New("no url candidates")
	}
	return nil, lastErr
}

// MarketEntry is one plugin listing in the remote index.
type MarketEntry struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Version        string `json:"version"`
	Description    string `json:"description,omitempty"`
	Author         string `json:"author,omitempty"`
	Icon           string `json:"icon,omitempty"`
	MinHostVersion string `json:"minHostVersion,omitempty"`
	Download       string `json:"download"`         // URL to the .sekplugin
	SHA256         string `json:"sha256,omitempty"` // required at install for integrity
	Homepage       string `json:"homepage,omitempty"`
}

// MarketIndex is the remote registry document.
type MarketIndex struct {
	Version int           `json:"version"`
	Plugins []MarketEntry `json:"plugins"`
}

// MarketListing augments a MarketEntry with local state relative to the
// installed plugins, for the frontend to render install/update/installed.
type MarketListing struct {
	MarketEntry
	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installedVersion,omitempty"`
	UpdateAvailable  bool   `json:"updateAvailable"`
}

// MarketService fetches the remote plugin index and installs plugins from it by
// downloading the .sekplugin to a temp file and delegating to PluginStore.Install.
type MarketService struct {
	client     *http.Client // full timeout — used for the official-source attempt
	fastClient *http.Client // shorter timeout — fail over fast when the mirror is dead
	store      *PluginStore
}

func NewMarketService(store *PluginStore) *MarketService {
	// TLS certs are verified (default transport): both the index and the downloaded
	// .sekplugin feed dynamically imported JS, so an unverified connection is a
	// MITM→RCE vector. Routing through GitHubProxyPrefix is a deliberate, accepted
	// trust in that mirror; the install path still verifies each plugin's sha256
	// (taken from the index) before it is ever loaded.
	return &MarketService{
		client:     &http.Client{Timeout: 60 * time.Second},
		fastClient: &http.Client{Timeout: 25 * time.Second},
		store:      store,
	}
}

// fetch GETs rawurl through the GitHub mirror (then official) — see mirrorFetch.
func (m *MarketService) fetch(rawurl string) (*http.Response, error) {
	return mirrorFetch(m.fastClient, m.client, rawurl)
}

// FetchIndex retrieves + parses the remote index. url empty → DefaultMarketURL.
func (m *MarketService) FetchIndex(url string) (MarketIndex, error) {
	var idx MarketIndex
	if strings.TrimSpace(url) == "" {
		url = DefaultMarketURL
	}
	resp, err := m.fetch(url)
	if err != nil {
		return idx, fmt.Errorf("index fetch failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return idx, err
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return idx, errors.New("invalid market index JSON")
	}
	return idx, nil
}

// Listings fetches the index and annotates each entry with installed/update state.
func (m *MarketService) Listings(url string) ([]MarketListing, error) {
	idx, err := m.FetchIndex(url)
	if err != nil {
		return nil, err
	}
	installed, _ := m.store.List()
	byID := map[string]PluginInfo{}
	for _, p := range installed {
		byID[p.ID] = p
	}
	out := make([]MarketListing, 0, len(idx.Plugins))
	for _, e := range idx.Plugins {
		l := MarketListing{MarketEntry: e}
		if p, ok := byID[e.ID]; ok {
			l.Installed = true
			l.InstalledVersion = p.Version
			l.UpdateAvailable = versionNewer(e.Version, p.Version)
		}
		out = append(out, l)
	}
	return out, nil
}

// Install downloads the entry's .sekplugin to a temp file, verifies sha256 (if
// provided), and installs it via PluginStore. hostVersion gates minHostVersion.
func (m *MarketService) Install(url, id, hostVersion string) (PluginManifest, error) {
	var zero PluginManifest
	idx, err := m.FetchIndex(url)
	if err != nil {
		return zero, err
	}
	var entry *MarketEntry
	for i := range idx.Plugins {
		if idx.Plugins[i].ID == id {
			entry = &idx.Plugins[i]
			break
		}
	}
	if entry == nil {
		return zero, errors.New("plugin not found in market: " + id)
	}
	return m.installEntry(entry, hostVersion)
}

// installEntry downloads the entry's .sekplugin (mirror-aware), verifies its
// mandatory sha256, and installs it via PluginStore. Shared by Install + AutoUpdate.
func (m *MarketService) installEntry(entry *MarketEntry, hostVersion string) (PluginManifest, error) {
	var zero PluginManifest
	if strings.TrimSpace(entry.Download) == "" {
		return zero, errors.New("market entry missing download url")
	}
	tmp, err := m.downloadToTemp(entry.Download)
	if err != nil {
		return zero, err
	}
	defer os.Remove(tmp)

	// sha256 is mandatory: an entry without it would be installed with zero
	// integrity verification, so refuse rather than trust the bytes blindly.
	if strings.TrimSpace(entry.SHA256) == "" {
		return zero, errors.New("市场条目缺少 sha256 校验值，拒绝安装")
	}
	sum, err := fileSHA256(tmp)
	if err != nil {
		return zero, err
	}
	if !strings.EqualFold(sum, entry.SHA256) {
		return zero, errors.New("下载校验失败（sha256 不匹配）")
	}
	return m.store.Install(tmp, hostVersion, entry.ID)
}

// PluginUpdateResult is one plugin's outcome in an AutoUpdate sweep.
type PluginUpdateResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	FromVersion string `json:"fromVersion,omitempty"`
	ToVersion   string `json:"toVersion,omitempty"`
	Error       string `json:"error,omitempty"`
}

// AutoUpdateSummary reports an AutoUpdate sweep: which installed plugins were
// upgraded and which failed (per-plugin failures are non-fatal).
type AutoUpdateSummary struct {
	Updated []PluginUpdateResult `json:"updated"`
	Failed  []PluginUpdateResult `json:"failed"`
}

// AutoUpdate fetches the index once and reinstalls every installed plugin that has
// a newer version available. The index fetch is the only hard error; per-plugin
// failures are collected rather than aborting the sweep.
func (m *MarketService) AutoUpdate(url, hostVersion string) (AutoUpdateSummary, error) {
	var sum AutoUpdateSummary
	idx, err := m.FetchIndex(url)
	if err != nil {
		return sum, err
	}
	installed, _ := m.store.List()
	have := map[string]string{}
	for _, p := range installed {
		have[p.ID] = p.Version
	}
	for i := range idx.Plugins {
		e := idx.Plugins[i]
		cur, ok := have[e.ID]
		if !ok || !versionNewer(e.Version, cur) {
			continue
		}
		if _, err := m.installEntry(&e, hostVersion); err != nil {
			sum.Failed = append(sum.Failed, PluginUpdateResult{ID: e.ID, Name: e.Name, FromVersion: cur, ToVersion: e.Version, Error: err.Error()})
			continue
		}
		sum.Updated = append(sum.Updated, PluginUpdateResult{ID: e.ID, Name: e.Name, FromVersion: cur, ToVersion: e.Version})
	}
	return sum, nil
}

func (m *MarketService) downloadToTemp(url string) (string, error) {
	resp, err := m.fetch(url)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	f, err := os.CreateTemp("", "sekplugin-*.sekplugin")
	if err != nil {
		return "", err
	}
	// Cap download at 128 MiB.
	_, err = io.Copy(f, io.LimitReader(resp.Body, 128<<20))
	f.Close()
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
