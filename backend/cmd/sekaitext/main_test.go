package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeLifecycleShutdowner struct {
	called bool
	err    error
}

func (s *fakeLifecycleShutdowner) Shutdown(context.Context) error {
	s.called = true
	return s.err
}

func TestShutdownLifecycleAlwaysInvokesBackendCleanup(t *testing.T) {
	wantErr := errors.New("cleanup failed")
	backend := &fakeLifecycleShutdowner{err: wantErr}
	err := shutdownLifecycle(backend, nil, time.Second)
	if !backend.called {
		t.Fatal("backend cleanup was not invoked")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("shutdownLifecycle error = %v, want %v", err, wantErr)
	}
}

func TestAuthTokenForTransport(t *testing.T) {
	for _, tt := range []struct {
		name    string
		ipc     bool
		token   string
		want    string
		wantErr bool
	}{
		{name: "TCP rejects empty token", token: "", wantErr: true},
		{name: "TCP rejects whitespace token", token: "  ", wantErr: true},
		{name: "TCP keeps non-empty token", token: "secret-token", want: "secret-token"},
		{name: "IPC does not expose supplied token", ipc: true, token: "ignored", want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := authTokenForTransport(tt.ipc, tt.token)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("token = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTrustedLoopbackHostAllowsExpectedAuthorities(t *testing.T) {
	for _, authority := range []string{
		"localhost",
		"LOCALHOST",
		"localhost:9800",
		"LOCALHOST:9800",
		"127.0.0.1",
		"127.0.0.1:9800",
		"[::1]",
		"[::1]:9800",
		"[0:0:0:0:0:0:0:1]:9800",
	} {
		t.Run(authority, func(t *testing.T) {
			called := false
			handler := trustedLoopbackHost(9800)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
			req.Host = authority
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusNoContent || !called {
				t.Fatalf("authority %q: status=%d called=%v", authority, rr.Code, called)
			}
		})
	}
}

func TestTrustedLoopbackHostRejectsBeforeRepresentativeRoutes(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		path      string
		authority string
	}{
		{"debug read", http.MethodGet, "/api/v1/debug/logs", "attacker.invalid"},
		{"settings read", http.MethodGet, "/api/v1/settings", "attacker.invalid:9800"},
		{"team read", http.MethodGet, "/api/v1/team/status", "localhost.attacker.invalid"},
		{"recovery read", http.MethodGet, "/api/v1/recovery/load", "127.0.0.2:9800"},
		{"authenticated mutation", http.MethodPost, "/api/v1/translation/save", "[::2]:9800"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := trustedLoopbackHost(9800)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Host = tt.authority
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Sekai-Token", "valid-token")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusMisdirectedRequest || called {
				t.Fatalf("authority %q: status=%d called=%v", tt.authority, rr.Code, called)
			}
		})
	}
}

func TestTrustedLoopbackHostRejectsMalformedAuthorities(t *testing.T) {
	for _, authority := range []string{
		"",
		" localhost",
		"localhost ",
		"localhost:",
		"localhost:9801",
		"localhost:not-a-port",
		"localhost:9800:attacker",
		"localhost/path",
		"user@localhost:9800",
		"::1",
		"[::1",
		"[::1]attacker",
		"[::1]:",
		"[::1]:9800:attacker",
	} {
		t.Run(authority, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			req.Host = authority
			rr := httptest.NewRecorder()
			trustedLoopbackHost(9800)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("malformed authority reached route handler")
			})).ServeHTTP(rr, req)
			if rr.Code != http.StatusMisdirectedRequest {
				t.Fatalf("authority %q: status=%d", authority, rr.Code)
			}
		})
	}
}
