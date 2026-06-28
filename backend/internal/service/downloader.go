package service

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Downloader handles HTTP downloads from CDN.
type Downloader struct {
	client  *http.Client
	dataDir string
}

// ProgressTracker tracks download progress.
type ProgressTracker struct {
	mu       sync.RWMutex
	current  int
	total    int
	message  string
	done     bool
}

// NewDownloader creates a new Downloader.
//
// TLS verification is disabled because some asset CDNs (e.g. storage.exmeaning.com)
// serve valid certificates that Go's macOS verifier (SecPolicyCreateSSL) rejects,
// even though curl/openssl accept the full chain. These are public, read-only
// JSON assets, so skipping verification has no meaningful security impact here.
func NewDownloader(dataDir string) *Downloader {
	return &Downloader{
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
		dataDir: dataDir,
	}
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

// DownloadJSON downloads a JSON file from URL and saves to dataDir.
// Returns file path.
func (d *Downloader) DownloadJSON(url, fileName string) (string, error) {
	if err := safeCacheName(fileName); err != nil {
		return "", err
	}
	filePath := filepath.Join(d.dataDir, fileName)

	// Check if already cached
	if _, err := os.Stat(filePath); err == nil {
		log.Printf("Cache hit: %s", fileName)
		return filePath, nil
	}

	log.Printf("Downloading: %s", url)
	resp, err := d.client.Get(url)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	// Write to a unique temp file first, then rename, so a mid-stream failure never
	// leaves a partial file that the cache check would later return as a hit, and
	// concurrent downloads of the same fileName can't clobber each other's .part.
	out, err := os.CreateTemp(filepath.Dir(filePath), filepath.Base(filePath)+".*.part")
	if err != nil {
		return "", fmt.Errorf("failed to create file: %w", err)
	}
	tmpPath := out.Name()

	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to write file: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to write file: %w", err)
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath)
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
	filePath := filepath.Join(dir, fileName)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	// Check if already cached
	if _, err := os.Stat(filePath); err == nil {
		log.Printf("Cache hit: %s", filePath)
		if progress != nil {
			// Get file size for progress
			info, _ := os.Stat(filePath)
			progress(info.Size(), info.Size())
		}
		return filePath, nil
	}

	log.Printf("Downloading: %s -> %s", url, filePath)
	resp, err := d.client.Get(url)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	// Write to a unique temp file first, then rename, so a mid-stream failure never
	// leaves a partial file that the cache check would later return as a hit, and
	// concurrent downloads of the same fileName can't clobber each other's .part.
	out, err := os.CreateTemp(dir, filepath.Base(filePath)+".*.part")
	if err != nil {
		return "", fmt.Errorf("failed to create file: %w", err)
	}
	tmpPath := out.Name()

	var reader io.Reader = resp.Body
	if progress != nil && resp.ContentLength > 0 {
		reader = &progressReader{reader: resp.Body, total: resp.ContentLength, fn: progress}
	}

	if _, err := io.Copy(out, reader); err != nil {
		out.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to write file: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to write file: %w", err)
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	if progress != nil {
		progress(resp.ContentLength, resp.ContentLength)
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
	return d.client.Get(url)
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
