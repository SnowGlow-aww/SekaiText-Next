package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCapabilityTokenMutatingRoutes(t *testing.T) {
	const token = "test-capability"
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := capabilityToken(token)(next)

	tests := []struct {
		name        string
		method      string
		path        string
		header      string
		contentType string
		want        int
	}{
		{"engine write requires token", http.MethodPost, "/api/v1/engine/timing/start", "", "application/json", http.StatusForbidden},
		{"live2d import requires token", http.MethodPost, "/api/v1/live2d/import", "", "application/json", http.StatusForbidden},
		{"live2d sync requires token", http.MethodPost, "/api/v1/live2d/sync", "", "application/json", http.StatusForbidden},
		{"valid token and JSON accepted", http.MethodPost, "/api/v1/engine/timing/start", token, "application/json", http.StatusNoContent},
		{"valid token rejects text plain", http.MethodPost, "/api/v1/translation/save", token, "text/plain", http.StatusUnsupportedMediaType},
		{"read does not require token", http.MethodGet, "/api/v1/engine/status", "", "", http.StatusNoContent},
		{"recovery post requires token", http.MethodPost, "/api/v1/recovery/clear", "", "text/plain", http.StatusForbidden},
		{"recovery delete requires token", http.MethodDelete, "/api/v1/recovery/clear", "", "", http.StatusForbidden},
		{"similar recovery path requires token", http.MethodPost, "/api/v1/recovery/clear/extra", "", "application/json", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.header != "" {
				req.Header.Set("X-Sekai-Token", tt.header)
			}
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != tt.want {
				t.Fatalf("status = %d, want %d", rr.Code, tt.want)
			}
		})
	}
}

func TestCrossOriginSimpleFileMutationsCannotReachHandlers(t *testing.T) {
	const token = "test-capability"
	var hits int
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusNoContent)
	})
	handler := developmentCORS().Handler(capabilityToken(token)(next))

	for _, path := range []string{
		"/api/v1/translation/create",
		"/api/v1/translation/rename-file",
		"/api/v1/translation/save",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Origin", "https://attacker.invalid")
		req.Header.Set("Content-Type", "text/plain")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("POST %s status = %d, want %d", path, rr.Code, http.StatusForbidden)
		}
	}
	if hits != 0 {
		t.Fatalf("unauthenticated file mutation handler ran %d times", hits)
	}
}

func TestCapabilityTokenDisabledInPrivateIPCMode(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	rr := httptest.NewRecorder()
	capabilityToken("")(next).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/settings", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestDevelopmentCORSUsesExplicitOrigins(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := developmentCORS().Handler(next)

	for _, tt := range []struct {
		origin string
		want   string
	}{
		{"http://localhost:5173", "http://localhost:5173"},
		{"http://127.0.0.1:5173", "http://127.0.0.1:5173"},
		{"https://attacker.invalid", ""},
	} {
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/settings", nil)
		req.Header.Set("Origin", tt.origin)
		req.Header.Set("Access-Control-Request-Method", http.MethodPut)
		req.Header.Set("Access-Control-Request-Headers", "content-type,x-sekai-token")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if got := rr.Header().Get("Access-Control-Allow-Origin"); got != tt.want {
			t.Errorf("origin %q allowed as %q, want %q", tt.origin, got, tt.want)
		}
	}
}
