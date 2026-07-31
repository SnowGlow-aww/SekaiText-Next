package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"sekaitext/backend/internal/api"
	"sekaitext/backend/internal/config"
	"sekaitext/backend/internal/ipc"
)

func main() {
	port := flag.Int("port", 9800, "server port")
	host := flag.String("host", "127.0.0.1", "loopback interface to bind in development TCP mode (localhost, 127.0.0.1, or ::1)")
	authToken := flag.String("auth-token", "", "capability token required in TCP mode on mutating requests (X-Sekai-Token header)")
	dir := flag.String("dir", ".", "base directory for read-only resources (images)")
	dataDir := flag.String("data-dir", "", "base directory for writable data (catalog, settings); defaults to --dir")
	ipcMode := flag.Bool("ipc", false, "serve over stdio framing (Tauri sekai:// custom scheme) instead of binding TCP; release transport. No TCP port, no capability token.")
	flag.Parse()
	validatedAuthToken, err := authTokenForTransport(*ipcMode, *authToken)
	if err != nil {
		log.Printf("Refusing to start: %v", err)
		return
	}
	validatedHost, err := hostForTransport(*ipcMode, *host)
	if err != nil {
		log.Printf("Refusing to start: %v", err)
		return
	}

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
	// IPC is process-private and uses no capability; TCP is fail-closed above.
	cfg.AuthToken = validatedAuthToken

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

	addr := net.JoinHostPort(validatedHost, strconv.Itoa(*port))
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
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		_ = shutdownLifecycle(router, nil, 8*time.Second)
		log.Printf("Server listener has unexpected address type %T", ln.Addr())
		return
	}
	// Keep Host validation outside the router so hostile authorities are rejected
	// before CORS, authentication, logging, or any route. IPC does not use this
	// handler because its authority is synthesized inside the private frame transport.
	httpServer := newTCPHTTPServer(trustedLoopbackHost(tcpAddr.Port)(router))
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

func newTCPHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}

func authTokenForTransport(ipcMode bool, raw string) (string, error) {
	if ipcMode {
		return "", nil
	}
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("TCP mode requires a non-empty --auth-token")
	}
	return raw, nil
}

// hostForTransport keeps the development HTTP server process-local. Host-header
// validation alone cannot prove the peer is local when a listener is bound to a
// LAN/wildcard address because a remote client can send Host: localhost. Release
// IPC ignores --host entirely and does not create a network listener.
func hostForTransport(ipcMode bool, raw string) (string, error) {
	if ipcMode {
		return "", nil
	}
	host := strings.TrimSpace(raw)
	if strings.EqualFold(host, "localhost") {
		return "localhost", nil
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || addr.Zone() != "" {
		return "", errors.New("TCP mode requires a loopback --host (localhost, 127.0.0.1, or ::1)")
	}
	addr = addr.Unmap()
	if addr != netip.MustParseAddr("127.0.0.1") && addr != netip.IPv6Loopback() {
		return "", errors.New("TCP mode requires a loopback --host (localhost, 127.0.0.1, or ::1)")
	}
	return addr.String(), nil
}

func trustedLoopbackHost(expectedPort int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !trustedLoopbackAuthority(r.Host, expectedPort) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusMisdirectedRequest)
				_, _ = w.Write([]byte(`{"error":"untrusted request authority"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func trustedLoopbackAuthority(authority string, expectedPort int) bool {
	if authority == "" || expectedPort < 1 || expectedPort > 65535 {
		return false
	}

	host, port, hasPort := authority, "", false
	if strings.HasPrefix(authority, "[") {
		closing := strings.IndexByte(authority, ']')
		if closing < 0 {
			return false
		}
		host = authority[1:closing]
		remainder := authority[closing+1:]
		if remainder != "" {
			if len(remainder) < 2 || remainder[0] != ':' {
				return false
			}
			port = remainder[1:]
			hasPort = true
		}
	} else {
		switch strings.Count(authority, ":") {
		case 0:
		case 1:
			var err error
			host, port, err = net.SplitHostPort(authority)
			if err != nil {
				return false
			}
			hasPort = true
		default:
			// IPv6 authorities must be bracketed.
			return false
		}
	}

	if hasPort {
		if port == "" {
			return false
		}
		parsed, err := strconv.ParseUint(port, 10, 16)
		if err != nil || int(parsed) != expectedPort {
			return false
		}
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && (addr == netip.MustParseAddr("127.0.0.1") || addr == netip.IPv6Loopback())
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
