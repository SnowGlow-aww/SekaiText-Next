package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"sekaitext/backend/internal/api"
	"sekaitext/backend/internal/config"
)

func main() {
	port := flag.Int("port", 9800, "server port")
	host := flag.String("host", "127.0.0.1", "interface to bind; 127.0.0.1 keeps the sidecar local-only. Use 0.0.0.0 to deliberately expose it to the LAN.")
	authToken := flag.String("auth-token", "", "capability token required on mutating requests (X-Sekai-Token header); empty disables enforcement (dev)")
	dir := flag.String("dir", ".", "base directory for read-only resources (images)")
	dataDir := flag.String("data-dir", "", "base directory for writable data (catalog, settings); defaults to --dir")
	flag.Parse()

	// Resolve base directory:
	// - If --dir explicitly provided, use it as-is (relative to CWD).
	// - If default "." and resources aren't found, fall back to the executable's
	//   directory (for Tauri sidecar deployment).
	baseDir := *dir
	if baseDir == "." {
		// Check if CWD has resources/ directory
		if _, err := os.Stat(filepath.Join(".", "resources", "catalog")); os.IsNotExist(err) {
			// Fall back to executable directory for sidecar deployment
			exe, err := os.Executable()
			if err == nil {
				baseDir = filepath.Dir(exe)
			}
		}
	}

	cfg := config.NewAppConfig(baseDir, *dataDir)
	cfg.AuthToken = *authToken

	// Ensure writable directories exist
	ensureDir(cfg.CatalogDir)
	ensureDir(cfg.DataDir)
	ensureDir(cfg.ImagesChrDir)
	ensureDir(cfg.PluginsDir)

	router := api.NewRouter(cfg)

	addr := fmt.Sprintf("%s:%d", *host, *port)
	log.Printf("SekaiText server starting on %s", addr)
	log.Printf("Resource directory: %s", cfg.BaseDir)
	log.Printf("Data directory: %s", cfg.DataBaseDir)

	// Retry the bind briefly: during an in-place upgrade the new sidecar may start
	// while a just-killed old instance still holds the port for a moment. Without
	// this the new sidecar would Fatal and the frontend would fall back to the stale
	// old backend (which lacks newer routes → 404s).
	ln, err := listenWithRetry(addr, 25, 200*time.Millisecond)
	if err != nil {
		log.Fatalf("Server failed to bind %s: %v", addr, err)
	}
	if err := http.Serve(ln, router); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// listenWithRetry binds addr, retrying briefly so a port momentarily held by a
// just-killed previous sidecar doesn't fail the launch.
func listenWithRetry(addr string, attempts int, delay time.Duration) (net.Listener, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		lastErr = err
		time.Sleep(delay)
	}
	return nil, lastErr
}

func ensureDir(path string) {
	if err := os.MkdirAll(path, 0755); err != nil {
		log.Printf("Warning: could not create directory %s: %v", path, err)
	}
}
