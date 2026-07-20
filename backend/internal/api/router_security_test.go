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
		name   string
		method string
		path   string
		header string
		want   int
	}{
		{"engine write requires token", http.MethodPost, "/api/v1/engine/timing/start", "", http.StatusForbidden},
		{"live2d import requires token", http.MethodPost, "/api/v1/live2d/import", "", http.StatusForbidden},
		{"live2d sync requires token", http.MethodPost, "/api/v1/live2d/sync", "", http.StatusForbidden},
		{"valid token accepted", http.MethodPost, "/api/v1/engine/timing/start", token, http.StatusNoContent},
		{"read does not require token", http.MethodGet, "/api/v1/engine/status", "", http.StatusNoContent},
		{"recovery beacon is narrow exception", http.MethodPost, "/api/v1/recovery/clear", "", http.StatusNoContent},
		{"recovery delete still requires token", http.MethodDelete, "/api/v1/recovery/clear", "", http.StatusForbidden},
		{"similar recovery path still requires token", http.MethodPost, "/api/v1/recovery/clear/extra", "", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.header != "" {
				req.Header.Set("X-Sekai-Token", tt.header)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != tt.want {
				t.Fatalf("status = %d, want %d", rr.Code, tt.want)
			}
		})
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
