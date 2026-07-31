package service

import (
	"bytes"
	"crypto/sha256"
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
	"strings"
	"time"

	"sekaitext/backend/internal/fsutil"
)

const (
	maxLive2DJSONBytes   int64 = 64 << 20
	maxLive2DBinaryBytes int64 = 128 << 20
	live2DMutableTTL           = 6 * time.Hour
)

var live2DAssetHosts = map[string]struct{}{
	"storage.sekai.best":     {},
	"storage.exmeaning.com":  {},
	"storage2.exmeaning.com": {},
}

// Live2DAssetResult describes one validated object in the app-private cache.
type Live2DAssetResult struct {
	Path     string `json:"path"`
	MIME     string `json:"mime"`
	Size     int64  `json:"size"`
	CacheHit bool   `json:"cacheHit"`
}

type live2DAssetSpec struct {
	canonical string
	extension string
	mime      string
	maxBytes  int64
	mutable   bool
	validate  func([]byte) bool
}

// Live2DAssetStore downloads the fixed built-in player's HTTPS resources into
// an app-private cache. It is intentionally not a general-purpose URL proxy.
type Live2DAssetStore struct {
	root   string
	client *http.Client
}

func NewLive2DAssetStore(cacheRoot string) (*Live2DAssetStore, error) {
	if strings.TrimSpace(cacheRoot) == "" {
		return nil, errors.New("live2d cache directory is required")
	}
	root, err := filepath.Abs(cacheRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve live2d cache directory: %w", err)
	}
	root = filepath.Join(root, "objects")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create live2d cache directory: %w", err)
	}
	store := &Live2DAssetStore{root: root}
	client := newSnapshotHTTPClient()
	client.Timeout = 60 * time.Second
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if _, err := store.assetSpec(req.URL.String()); err != nil {
			return fmt.Errorf("unsafe live2d redirect: %w", err)
		}
		return nil
	}
	store.client = client
	return store, nil
}

// Resolve returns a validated cached file, downloading it atomically on a miss.
func (s *Live2DAssetStore) Resolve(rawURL string) (Live2DAssetResult, error) {
	spec, err := s.assetSpec(rawURL)
	if err != nil {
		return Live2DAssetResult{}, err
	}
	sum := sha256.Sum256([]byte(spec.canonical))
	filePath := filepath.Join(s.root, hex.EncodeToString(sum[:])+spec.extension)
	unlock, err := lockDownloadPath(filePath)
	if err != nil {
		return Live2DAssetResult{}, fmt.Errorf("lock live2d cache object: %w", err)
	}
	defer unlock()

	if size, modTime, valid := validLive2DAssetFile(filePath, spec); valid {
		if !spec.mutable || time.Since(modTime) < live2DMutableTTL {
			return Live2DAssetResult{Path: filePath, MIME: spec.mime, Size: size, CacheHit: true}, nil
		}
	}

	result, downloadErr := s.download(spec, filePath)
	if downloadErr == nil {
		return result, nil
	}
	// Mutable index files remain usable offline after their refresh TTL expires.
	if size, _, valid := validLive2DAssetFile(filePath, spec); valid {
		return Live2DAssetResult{Path: filePath, MIME: spec.mime, Size: size, CacheHit: true}, nil
	}
	return Live2DAssetResult{}, downloadErr
}

func (s *Live2DAssetStore) download(spec live2DAssetSpec, filePath string) (Live2DAssetResult, error) {
	req, err := http.NewRequest(http.MethodGet, spec.canonical, nil)
	if err != nil {
		return Live2DAssetResult{}, err
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "SekaiText-Android/"+CurrentAppVersion)
	resp, err := s.client.Do(req)
	if err != nil {
		return Live2DAssetResult{}, fmt.Errorf("download live2d asset: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Live2DAssetResult{}, fmt.Errorf("download live2d asset returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > spec.maxBytes {
		return Live2DAssetResult{}, fmt.Errorf("live2d asset exceeds %d byte limit", spec.maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, spec.maxBytes+1))
	if err != nil {
		return Live2DAssetResult{}, fmt.Errorf("read live2d asset: %w", err)
	}
	if int64(len(data)) == 0 || int64(len(data)) > spec.maxBytes {
		return Live2DAssetResult{}, fmt.Errorf("live2d asset is empty or exceeds %d byte limit", spec.maxBytes)
	}
	if !spec.validate(data) {
		return Live2DAssetResult{}, errors.New("live2d asset content does not match its allowed type")
	}
	if err := fsutil.WriteFileAtomic(filePath, data, 0o644); err != nil {
		return Live2DAssetResult{}, fmt.Errorf("write live2d cache object: %w", err)
	}
	return Live2DAssetResult{Path: filePath, MIME: spec.mime, Size: int64(len(data)), CacheHit: false}, nil
}

func validLive2DAssetFile(filePath string, spec live2DAssetSpec) (int64, time.Time, bool) {
	info, err := os.Stat(filePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > spec.maxBytes {
		return 0, time.Time{}, false
	}
	data, err := os.ReadFile(filePath)
	if err != nil || !spec.validate(data) {
		return 0, time.Time{}, false
	}
	return info.Size(), info.ModTime(), true
}

func (s *Live2DAssetStore) assetSpec(rawURL string) (live2DAssetSpec, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return live2DAssetSpec{}, errors.New("live2d asset URL must be absolute HTTPS")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return live2DAssetSpec{}, errors.New("live2d asset URL must not contain credentials, query, or fragment")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if _, allowed := live2DAssetHosts[host]; !allowed {
		return live2DAssetSpec{}, fmt.Errorf("live2d asset host not allowed: %s", host)
	}
	if port := u.Port(); port != "" && port != "443" {
		return live2DAssetSpec{}, errors.New("live2d asset URL must use port 443")
	}
	if strings.Contains(u.Path, "\\") || strings.Contains(strings.ToLower(u.EscapedPath()), "%2f") || strings.Contains(strings.ToLower(u.EscapedPath()), "%5c") {
		return live2DAssetSpec{}, errors.New("live2d asset URL contains an unsafe path encoding")
	}
	cleanPath := pathpkg.Clean(u.Path)
	if cleanPath != u.Path || !strings.HasPrefix(cleanPath, "/") || strings.Contains(cleanPath, "\x00") {
		return live2DAssetSpec{}, errors.New("live2d asset URL contains an unsafe path")
	}
	if !allowedLive2DPath(host, cleanPath) {
		return live2DAssetSpec{}, fmt.Errorf("live2d asset path not allowed: %s", cleanPath)
	}

	u.Scheme = "https"
	u.Host = host
	u.RawPath = ""
	canonical := u.String()
	lowerPath := strings.ToLower(cleanPath)
	spec := live2DAssetSpec{canonical: canonical}
	switch {
	case strings.HasSuffix(lowerPath, ".json"), strings.HasSuffix(lowerPath, ".model3"), strings.HasSuffix(lowerPath, ".physics3"):
		spec.extension = filepath.Ext(lowerPath)
		spec.mime = "application/json"
		spec.maxBytes = maxLive2DJSONBytes
		spec.validate = json.Valid
		spec.mutable = strings.HasSuffix(lowerPath, "/model_list.json") || strings.HasSuffix(lowerPath, "/buildmotiondata.json")
	case strings.HasSuffix(lowerPath, ".moc3"):
		spec.extension = ".moc3"
		spec.mime = "application/octet-stream"
		spec.maxBytes = maxLive2DBinaryBytes
		spec.validate = func(data []byte) bool { return len(data) >= 4 && bytes.Equal(data[:4], []byte("MOC3")) }
	case strings.HasSuffix(lowerPath, ".png"):
		spec.extension = ".png"
		spec.mime = "image/png"
		spec.maxBytes = maxLive2DBinaryBytes
		spec.validate = func(data []byte) bool { return len(data) >= 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) }
	case strings.HasSuffix(lowerPath, ".webp"):
		spec.extension = ".webp"
		spec.mime = "image/webp"
		spec.maxBytes = maxLive2DBinaryBytes
		spec.validate = func(data []byte) bool {
			return len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP"))
		}
	case strings.HasSuffix(lowerPath, ".mp3"):
		spec.extension = ".mp3"
		spec.mime = "audio/mpeg"
		spec.maxBytes = maxLive2DBinaryBytes
		spec.validate = func(data []byte) bool {
			return len(data) >= 3 && (bytes.Equal(data[:3], []byte("ID3")) || (data[0] == 0xff && data[1]&0xe0 == 0xe0))
		}
	default:
		return live2DAssetSpec{}, fmt.Errorf("live2d asset type not allowed: %s", cleanPath)
	}
	return spec, nil
}

func allowedLive2DPath(host, path string) bool {
	switch host {
	case "storage.sekai.best":
		return strings.HasPrefix(path, "/sekai-live2d-assets/live2d/")
	case "storage.exmeaning.com", "storage2.exmeaning.com":
		for _, prefix := range []string{
			"/sekai-jp-assets/live2d/model/",
			"/sekai-jp-assets/scenario/",
			// The built-in player rewrites event and card story URLs from the
			// catalog onto the complete exmeaning mirror before native resolution.
			"/sekai-jp-assets/event_story/",
			"/sekai-jp-assets/character/member/",
			"/sekai-jp-assets/sound/scenario/",
			"/sekai-jp-assets/sound/card_scenario/",
		} {
			if strings.HasPrefix(path, prefix) {
				return true
			}
		}
	}
	return false
}
