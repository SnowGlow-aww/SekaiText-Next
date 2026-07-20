package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"sekaitext/backend/internal/fsutil"
)

const maxStoryJSONBytes int64 = 64 << 20

var storyDownloadHosts = map[string]struct{}{
	"storage.sekai.best": {},
	"assets.unipjsk.com": {},
	"production-sekai-assets.neo.bot.haruki.seiunx.com": {},
	"storage.exmeaning.com":                             {},
}

// Downloader handles HTTP downloads from CDN.
type Downloader struct {
	client           *http.Client
	dataDir          string
	allowedHosts     map[string]struct{}
	maxResponseBytes int64
}

// ProgressTracker tracks download progress.
type ProgressTracker struct {
	mu      sync.RWMutex
	current int
	total   int
	message string
	done    bool
}

// NewDownloader creates a new Downloader.
func NewDownloader(dataDir string) *Downloader {
	d := &Downloader{
		dataDir:          dataDir,
		allowedHosts:     storyDownloadHosts,
		maxResponseBytes: maxStoryJSONBytes,
	}
	client := newSnapshotHTTPClient()
	client.Timeout = 30 * time.Second
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if _, err := d.validateURL(req.URL.String()); err != nil {
			return fmt.Errorf("unsafe redirect: %w", err)
		}
		return nil
	}
	d.client = client
	return d
}

// NewProgressTracker creates a new ProgressTracker.
func NewProgressTracker() *ProgressTracker {
	return &ProgressTracker{}
}

// SetTotal sets the total number of steps.
func (pt *ProgressTracker) SetTotal(total int) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.total = total
	pt.current = 0
	pt.done = false
}

// Advance increments the progress counter.
func (pt *ProgressTracker) Advance(msg string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.current++
	pt.message = msg
}

// Done marks the progress as complete.
func (pt *ProgressTracker) Done() {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.done = true
	pt.message = "完成"
}

// Status returns the current progress status.
func (pt *ProgressTracker) Status() (int, int, string, bool) {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return pt.current, pt.total, pt.message, pt.done
}

// safeCacheName validates a CDN-derived cache file name. The names come from
// remote master data (event chapter AssetName / special story FileName, etc.)
// and are always flat basenames, so any path separator or ".." segment is
// rejected to keep the write inside the cache directory.
func safeCacheName(fileName string) error {
	if fileName == "" || fileName != filepath.Base(fileName) || strings.Contains(fileName, "..") {
		return fmt.Errorf("invalid file name: %q", fileName)
	}
	return nil
}

// canonicalHTTPSURL produces a stable cache-key input while rejecting URL forms
// that should never be sent by the downloader.
func canonicalHTTPSURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", fmt.Errorf("download URL must be an absolute HTTPS URL")
	}
	if u.User != nil {
		return "", fmt.Errorf("download URL must not contain credentials")
	}
	host, port := strings.ToLower(u.Hostname()), u.Port()
	if host == "" {
		return "", fmt.Errorf("download URL has no host")
	}
	u.Scheme = "https"
	u.Host = host
	if port != "" && port != "443" {
		u.Host = net.JoinHostPort(host, port)
	}
	u.Fragment = ""
	u.RawQuery = u.Query().Encode()
	if u.Path == "" {
		u.Path = "/"
	} else {
		u.Path = pathpkg.Clean(u.Path)
		if !strings.HasPrefix(u.Path, "/") {
			u.Path = "/" + u.Path
		}
	}
	u.RawPath = ""
	return u.String(), nil
}

func (d *Downloader) validateURL(raw string) (string, error) {
	canonical, err := canonicalHTTPSURL(raw)
	if err != nil {
		return "", err
	}
	u, _ := url.Parse(canonical)
	if _, ok := d.allowedHosts[u.Hostname()]; !ok {
		return "", fmt.Errorf("download host not allowed: %s", u.Hostname())
	}
	return canonical, nil
}

func (d *Downloader) cachePath(rawURL, fileName string) (string, error) {
	canonical, err := d.validateURL(rawURL)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return filepath.Join(d.dataDir, "story-cache", hex.EncodeToString(sum[:]), fileName), nil
}

func (d *Downloader) sourceHash(rawURL string) (string, error) {
	canonical, err := d.validateURL(rawURL)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:]), nil
}

func validJSONFile(filePath string, maxBytes int64) bool {
	info, err := os.Stat(filePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxBytes {
		return false
	}
	data, err := os.ReadFile(filePath)
	return err == nil && json.Valid(data)
}

type downloadCacheMarker struct {
	Version       int    `json:"version"`
	Source        string `json:"source"`
	ContentSHA256 string `json:"contentSha256"`
}

func validJSONFileDigest(filePath string, maxBytes int64) (string, int64, bool, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, false, nil
		}
		return "", 0, false, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxBytes {
		return "", info.Size(), false, nil
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", 0, false, err
	}
	if !json.Valid(data) {
		return "", int64(len(data)), false, nil
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), int64(len(data)), true, nil
}

func (d *Downloader) get(rawURL string) (*http.Response, error) {
	canonical, err := d.validateURL(rawURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, canonical, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.ContentLength > d.maxResponseBytes {
		resp.Body.Close()
		return nil, fmt.Errorf("response exceeds %d byte limit", d.maxResponseBytes)
	}
	return resp, nil
}

func copyJSONWithLimit(dst io.Writer, src io.Reader, maxBytes int64) (int64, error) {
	n, err := io.Copy(dst, io.LimitReader(src, maxBytes+1))
	if err != nil {
		return n, err
	}
	if n > maxBytes {
		return n, fmt.Errorf("response exceeds %d byte limit", maxBytes)
	}
	return n, nil
}

// DownloadJSON downloads a JSON file from URL and saves to dataDir.
// Returns file path.
func (d *Downloader) DownloadJSON(url, fileName string) (string, error) {
	if err := safeCacheName(fileName); err != nil {
		return "", err
	}
	filePath, err := d.cachePath(url, fileName)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}
	unlock, err := lockDownloadPath(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to canonicalize cache path: %w", err)
	}
	defer unlock()

	// Filename-only legacy entries are intentionally ignored. The URL hash keeps
	// identical asset names from different story sources isolated.
	if validJSONFile(filePath, d.maxResponseBytes) {
		log.Printf("Cache hit: %s", fileName)
		return filePath, nil
	}

	log.Printf("Downloading: %s", url)
	resp, err := d.get(url)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	data, _, err := readJSONWithLimit(resp.Body, d.maxResponseBytes)
	if err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}
	if err := fsutil.WriteFileAtomic(filePath, data, 0o644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	log.Printf("Downloaded: %s", fileName)
	return filePath, nil
}

// DownloadProgressFn is called with (bytesRead, totalBytes) during download.
type DownloadProgressFn func(read, total int64)

// DownloadJSONToDir downloads a JSON file from URL to a specific directory.
// If progress is non-nil, it is called periodically with bytes read and total bytes.
func (d *Downloader) DownloadJSONToDir(url, dir, fileName string, progress DownloadProgressFn) (string, error) {
	if err := safeCacheName(fileName); err != nil {
		return "", err
	}
	sourceHash, err := d.sourceHash(url)
	if err != nil {
		return "", err
	}
	filePath := filepath.Join(dir, fileName)
	markerPath := filepath.Join(dir, ".sekaitext-cache", fileName+".source")

	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}
	unlock, err := lockDownloadPath(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to canonicalize output path: %w", err)
	}
	defer unlock()

	markerData, markerErr := os.ReadFile(markerPath)
	markerExists := markerErr == nil
	if markerErr != nil && !os.IsNotExist(markerErr) {
		return "", fmt.Errorf("read cache marker: %w", markerErr)
	}
	if markerExists {
		var marker downloadCacheMarker
		if json.Unmarshal(markerData, &marker) == nil && marker.Version == 1 && marker.Source == sourceHash {
			digest, size, valid, err := validJSONFileDigest(filePath, d.maxResponseBytes)
			if err != nil {
				return "", fmt.Errorf("validate cached output: %w", err)
			}
			if valid && digest == marker.ContentSHA256 {
				log.Printf("Cache hit: %s", filePath)
				if progress != nil {
					progress(size, size)
				}
				return filePath, nil
			}
		}
	}
	if markerExists {
		if err := os.Remove(markerPath); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("remove stale cache marker: %w", err)
		}
	}

	log.Printf("Downloading: %s -> %s", url, filePath)
	resp, err := d.get(url)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	var reader io.Reader = resp.Body
	if progress != nil && resp.ContentLength > 0 {
		reader = &progressReader{reader: resp.Body, total: resp.ContentLength, fn: progress}
	}

	data, n, err := readJSONWithLimit(reader, d.maxResponseBytes)
	if err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}
	if err := fsutil.WriteFileAtomic(filePath, data, 0o644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}
	contentSum := sha256.Sum256(data)
	marker := downloadCacheMarker{
		Version:       1,
		Source:        sourceHash,
		ContentSHA256: hex.EncodeToString(contentSum[:]),
	}
	markerData, err = json.Marshal(marker)
	if err != nil {
		return "", fmt.Errorf("encode cache marker: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(markerPath), 0755); err != nil {
		return "", fmt.Errorf("create cache marker directory: %w", err)
	}
	if err := fsutil.WriteFileAtomic(markerPath, markerData, 0o644); err != nil {
		return "", fmt.Errorf("write cache marker: %w", err)
	}

	if progress != nil {
		progress(n, n)
	}

	log.Printf("Downloaded: %s", filePath)
	return filePath, nil
}

type progressReader struct {
	reader io.Reader
	total  int64
	read   int64
	fn     DownloadProgressFn
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.reader.Read(b)
	p.read += int64(n)
	p.fn(p.read, p.total)
	return n, err
}

// Get performs a streaming GET against url and returns the live response. The
// caller owns resp.Body and must close it. Used by the Live2D asset proxy so the
// frontend (which cannot reach some CDNs directly due to CORS/sandbox network
// rules) can fetch model/texture/motion files through the local backend.
func (d *Downloader) Get(url string) (*http.Response, error) {
	resp, err := d.get(url)
	if err != nil {
		return nil, err
	}
	resp.Body = &limitedReadCloser{
		Reader: io.LimitReader(resp.Body, d.maxResponseBytes),
		Closer: resp.Body,
	}
	return resp, nil
}

type limitedReadCloser struct {
	io.Reader
	io.Closer
}

type downloadPathLock struct {
	mu   sync.Mutex
	refs int
}

var downloadPathLocks = struct {
	sync.Mutex
	items map[string]*downloadPathLock
}{items: make(map[string]*downloadPathLock)}

func lockDownloadPath(path string) (func(), error) {
	key, err := canonicalDownloadPath(path)
	if err != nil {
		return nil, err
	}
	downloadPathLocks.Lock()
	entry := downloadPathLocks.items[key]
	if entry == nil {
		entry = &downloadPathLock{}
		downloadPathLocks.items[key] = entry
	}
	entry.refs++
	downloadPathLocks.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		downloadPathLocks.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(downloadPathLocks.items, key)
		}
		downloadPathLocks.Unlock()
	}, nil
}

func canonicalDownloadPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

func readJSONWithLimit(r io.Reader, max int64) ([]byte, int64, error) {
	var buffer bytes.Buffer
	n, err := copyJSONWithLimit(&buffer, r, max)
	if err != nil {
		return nil, n, err
	}
	data := buffer.Bytes()
	if len(data) == 0 || !json.Valid(data) {
		return nil, n, fmt.Errorf("download returned empty or invalid JSON")
	}
	return append([]byte(nil), data...), n, nil
}

// UpdateAll performs a full metadata update from CDN. It single-flights: a second
// concurrent call returns immediately, so two background refreshes can't race-append
// the shared metadata slices (which can panic and crash the sidecar).
func (lm *ListManager) UpdateAll(catalogDir string, pt *ProgressTracker) {
	if !lm.updateMu.TryLock() {
		return
	}
	defer lm.updateMu.Unlock()
	lm.UpdateAllFromCDN(catalogDir, pt)
}
