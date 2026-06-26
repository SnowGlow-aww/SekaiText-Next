package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultMarketURL is the built-in plugin marketplace index. Overridable via
// Settings.PluginMarketURL.
const DefaultMarketURL = "https://raw.githubusercontent.com/snowglow-aww/sekaitext-plugins/main/index.json"

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
	client *http.Client
	store  *PluginStore
}

func NewMarketService(store *PluginStore) *MarketService {
	return &MarketService{
		client: &http.Client{
			Timeout: 60 * time.Second,
			// TLS certs are verified (default transport): both the index and the
			// downloaded .sekplugin come over the network and feed dynamically
			// imported JS, so an unverified connection is a MITM→RCE vector.
		},
		store: store,
	}
}

// FetchIndex retrieves + parses the remote index. url empty → DefaultMarketURL.
func (m *MarketService) FetchIndex(url string) (MarketIndex, error) {
	var idx MarketIndex
	if strings.TrimSpace(url) == "" {
		url = DefaultMarketURL
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return idx, err
	}
	req.Header.Set("User-Agent", "SekaiText-PluginMarket")
	resp, err := m.client.Do(req)
	if err != nil {
		return idx, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return idx, fmt.Errorf("index fetch failed: HTTP %d", resp.StatusCode)
	}
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

func (m *MarketService) downloadToTemp(url string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "SekaiText-PluginMarket")
	resp, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
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
