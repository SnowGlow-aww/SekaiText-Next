package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"sekaitext/backend/internal/api"
	"sekaitext/backend/internal/config"
	"sekaitext/backend/internal/ipc"
)

func main() {
	port := flag.Int("port", 9800, "server port")
	host := flag.String("host", "127.0.0.1", "interface to bind; 127.0.0.1 keeps the sidecar local-only. Use 0.0.0.0 to deliberately expose it to the LAN.")
	authToken := flag.String("auth-token", "", "capability token required on mutating requests (X-Sekai-Token header); empty disables enforcement (dev)")
	dir := flag.String("dir", ".", "base directory for read-only resources (images)")
	dataDir := flag.String("data-dir", "", "base directory for writable data (catalog, settings); defaults to --dir")
	ipcMode := flag.Bool("ipc", false, "serve over stdio framing (Tauri sekai:// custom scheme) instead of binding TCP; release transport. No TCP port, no capability token.")
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
	// In ipc (stdio) mode the channel is process-private — no external page can
	// reach it — so the capability token is pointless and stays "" (the token
	// middleware then no-ops). TCP mode keeps the per-launch token.
	if !*ipcMode {
		cfg.AuthToken = *authToken
	}

	// Ensure writable directories exist
	ensureDir(cfg.CatalogDir)
	ensureDir(cfg.DataDir)
	ensureDir(cfg.ImagesChrDir)
	ensureDir(cfg.PluginsDir)

	router := api.NewRouter(cfg)
	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	if *ipcMode {
		// Stdio transport (Tauri sekai:// custom scheme): no TCP bind. Logs go to
		// stderr (set inside ipc.Serve) so they never corrupt the stdout frame
		// stream. Returns on stdin EOF when the Rust shell closes the pipe.
		log.Printf("SekaiText server starting in IPC (stdio) mode")
		log.Printf("Resource directory: %s", cfg.BaseDir)
		log.Printf("Data directory: %s", cfg.DataBaseDir)
		serveDone := make(chan error, 1)
		go func() { serveDone <- ipc.Serve(router) }()
		var serveErr error
		select {
		case serveErr = <-serveDone:
		case <-signalCtx.Done():
		}
		shutdownErr := shutdownLifecycle(router, nil, 8*time.Second)
		if shutdownErr != nil {
			log.Printf("IPC shutdown warning: %v", shutdownErr)
		}
		if serveErr != nil {
			log.Printf("IPC server failed: %v", serveErr)
		}
		return
	}

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
		_ = shutdownLifecycle(router, nil, 8*time.Second)
		log.Printf("Server failed to bind %s: %v", addr, err)
		return
	}
	httpServer := &http.Server{Handler: router}
	serveDone := make(chan error, 1)
	go func() { serveDone <- httpServer.Serve(ln) }()
	var serveErr error
	select {
	case serveErr = <-serveDone:
	case <-signalCtx.Done():
	}
	if err := shutdownLifecycle(router, httpServer, 8*time.Second); err != nil {
		log.Printf("Server shutdown warning: %v", err)
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		log.Printf("Server failed: %v", serveErr)
	}
}

type lifecycleShutdowner interface {
	Shutdown(context.Context) error
}

// shutdownLifecycle is the single exit path for TCP serve errors, OS signals,
// and IPC EOF. HTTP intake stops before process-wide engine cleanup begins.
func shutdownLifecycle(backend lifecycleShutdowner, server *http.Server, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var serverDone chan error
	if server != nil {
		serverDone = make(chan error, 1)
		go func() { serverDone <- server.Shutdown(ctx) }()
	}
	var err error
	if backend != nil {
		err = errors.Join(err, backend.Shutdown(ctx))
	}
	if serverDone != nil {
		select {
		case serverErr := <-serverDone:
			err = errors.Join(err, serverErr)
		case <-ctx.Done():
			err = errors.Join(err, ctx.Err())
		}
	}
	return err
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
