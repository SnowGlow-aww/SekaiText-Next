package api

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// mutatingLive2DWriter changes the cache after the handler has committed its
// response headers. A correct handler has already captured the validated body.
type mutatingLive2DWriter struct {
	recorder *httptest.ResponseRecorder
	mutate   func()
}

func (w *mutatingLive2DWriter) Header() http.Header { return w.recorder.Header() }

func (w *mutatingLive2DWriter) WriteHeader(status int) {
	if w.mutate != nil {
		w.mutate()
		w.mutate = nil
	}
	w.recorder.WriteHeader(status)
}

func (w *mutatingLive2DWriter) Write(body []byte) (int, error) {
	return w.recorder.Write(body)
}

func TestLive2DProxyServesTheValidatedLocalSnapshot(t *testing.T) {
	root := t.TempDir()
	rawURL := "https://storage.sekai.best/sekai-live2d-assets/live2d/model/atomic.webp"
	valid := live2dTestFixture(t, "static-vp8.webp")
	path := live2dLocalPath(root, rawURL)
	if path == "" {
		t.Fatal("test URL did not resolve to a local mirror path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, valid, 0o644); err != nil {
		t.Fatal(err)
	}

	calls := 0
	h := live2dTestHandler(t, root, live2dRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return live2dTestResponse(req, http.StatusBadGateway, nil, 0), nil
	}))
	rr := httptest.NewRecorder()
	mw := &mutatingLive2DWriter{
		recorder: rr,
		mutate: func() {
			if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, len(valid)), 0o644); err != nil {
				t.Fatalf("mutate cached file: %v", err)
			}
		},
	}
	h.Live2DProxy(mw, live2dProxyRequest(t, rawURL))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if !bytes.Equal(rr.Body.Bytes(), valid) {
		t.Fatal("proxy served bytes different from the validated local snapshot")
	}
	if calls != 0 {
		t.Fatalf("local snapshot contacted upstream %d time(s)", calls)
	}
}

func TestLive2DProxyRejectsSymlinkEscapes(t *testing.T) {
	for _, mode := range []string{"parent", "file"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			rawURL := "https://storage.sekai.best/sekai-live2d-assets/live2d/model/escape.webp"
			path := live2dLocalPath(root, rawURL)
			if path == "" {
				t.Fatal("test URL did not resolve to a local mirror path")
			}
			validOutside := live2dTestFixture(t, "static-vp8.webp")
			if mode == "parent" {
				if err := os.WriteFile(filepath.Join(outside, "escape.webp"), validOutside, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "model")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				outsidePath := filepath.Join(outside, "escape.webp")
				if err := os.WriteFile(outsidePath, validOutside, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outsidePath, path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}

			upstream := live2dTestFixture(t, "static-vp8l.webp")
			calls := 0
			h := live2dTestHandler(t, root, live2dRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				return live2dTestResponse(req, http.StatusOK, upstream, -1), nil
			}))
			rr := httptest.NewRecorder()
			h.Live2DProxy(rr, live2dProxyRequest(t, rawURL))
			if rr.Code != http.StatusOK || rr.Header().Get("X-Live2D-Source") != "cdn" {
				t.Fatalf("symlink escape result = status %d source %q; body=%s", rr.Code, rr.Header().Get("X-Live2D-Source"), rr.Body.String())
			}
			if !bytes.Equal(rr.Body.Bytes(), upstream) {
				t.Fatal("proxy served the outside-root symlink target")
			}
			if calls != 1 {
				t.Fatalf("symlink escape upstream calls = %d, want 1", calls)
			}
		})
	}
}

func TestLive2DMirrorReadIsBounded(t *testing.T) {
	root := t.TempDir()
	rawURL := "https://storage.sekai.best/sekai-live2d-assets/live2d/model/large.webp"
	path := live2dLocalPath(root, rawURL)
	if path == "" {
		t.Fatal("test URL did not resolve to a local mirror path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxLive2DAssetBytes + 1); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := live2dReadCachedFile(root, path, rawURL); !errors.Is(err, errLive2DAssetTooLarge) {
		t.Fatalf("bounded mirror read error = %v, want errLive2DAssetTooLarge", err)
	}
}

func TestLive2DProxyRedirectRevalidatesPortAndPathFamily(t *testing.T) {
	for _, tc := range []struct {
		name        string
		redirectURL string
	}{
		{name: "non-default port", redirectURL: "https://storage.sekai.best:444/sekai-live2d-assets/live2d/model/final.webp"},
		{name: "wrong path family", redirectURL: "https://storage.sekai.best/sekai-live2d-assets/not-live2d/final.webp"},
		{name: "unrelated allowed host path", redirectURL: "https://sakimizuki.accr.cc/sekaitext-plugins/index.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			rawURL := "https://storage.sekai.best/sekai-live2d-assets/live2d/model/redirect.webp"
			h := live2dTestHandler(t, "", live2dRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				resp := live2dTestResponse(req, http.StatusFound, nil, 0)
				resp.Header.Set("Location", tc.redirectURL)
				return resp, nil
			}))
			rr := httptest.NewRecorder()
			h.Live2DProxy(rr, live2dProxyRequest(t, rawURL))
			if rr.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
			}
			if calls != 1 {
				t.Fatalf("redirect to %s made %d requests, want 1", tc.redirectURL, calls)
			}
		})
	}
}
