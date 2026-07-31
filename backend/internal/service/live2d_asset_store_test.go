package service

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLive2DAssetStoreCachesAndRepairsJSON(t *testing.T) {
	store, err := NewLive2DAssetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	store.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`[{"modelBase":"miku"}]`)),
			Request:    req,
		}, nil
	})}
	const upstream = "https://storage.sekai.best/sekai-live2d-assets/live2d/model_list.json"

	first, err := store.Resolve(upstream)
	if err != nil {
		t.Fatal(err)
	}
	if first.CacheHit || first.MIME != "application/json" || first.Size == 0 {
		t.Fatalf("unexpected first result: %+v", first)
	}
	if !strings.HasPrefix(first.Path, store.root+string(filepath.Separator)) {
		t.Fatalf("cache escaped object root: %s", first.Path)
	}
	second, err := store.Resolve(upstream)
	if err != nil {
		t.Fatal(err)
	}
	if !second.CacheHit || requests.Load() != 1 {
		t.Fatalf("second resolve did not hit cache: result=%+v requests=%d", second, requests.Load())
	}

	if err := os.WriteFile(first.Path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	repaired, err := store.Resolve(upstream)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.CacheHit || requests.Load() != 2 {
		t.Fatalf("corrupt cache was not repaired: result=%+v requests=%d", repaired, requests.Load())
	}
}

func TestLive2DAssetStoreRejectsUnapprovedURLs(t *testing.T) {
	store, err := NewLive2DAssetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cases := []string{
		"http://storage.sekai.best/sekai-live2d-assets/live2d/model_list.json",
		"https://storage.sekai.best.attacker.invalid/sekai-live2d-assets/live2d/model_list.json",
		"https://storage.sekai.best:444/sekai-live2d-assets/live2d/model_list.json",
		"https://storage.sekai.best/sekai-live2d-assets/live2d/model_list.json?v=1",
		"https://storage.sekai.best/sekai-live2d-assets/live2d/%2fetc/passwd.json",
		"https://storage2.exmeaning.com/sekai-jp-assets/../../secret.json",
		"https://storage2.exmeaning.com/sekai-jp-assets/live2d/model/demo/file.exe",
		"https://storage2.exmeaning.com/unrelated/file.json",
	}
	for _, raw := range cases {
		if _, err := store.assetSpec(raw); err == nil {
			t.Errorf("assetSpec accepted unsafe URL %q", raw)
		}
	}
}

func TestLive2DAssetStoreAllowsRewrittenEventAndCardScenarios(t *testing.T) {
	store, err := NewLive2DAssetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"https://storage2.exmeaning.com/sekai-jp-assets/event_story/event_208/scenario/event_208_01.json",
		"https://storage2.exmeaning.com/sekai-jp-assets/character/member/res012_no043/012043_touya01.json",
	} {
		if _, err := store.assetSpec(raw); err != nil {
			t.Errorf("assetSpec rejected catalog scenario URL %q: %v", raw, err)
		}
	}
}

func TestLive2DAssetContentValidation(t *testing.T) {
	store, err := NewLive2DAssetStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		url   string
		valid []byte
		bad   []byte
	}{
		{
			url:   "https://storage2.exmeaning.com/sekai-jp-assets/live2d/model/v1/demo/model.moc3",
			valid: []byte("MOC3\x02\x00"),
			bad:   []byte("<html>"),
		},
		{
			url:   "https://storage2.exmeaning.com/sekai-jp-assets/live2d/model/v1/demo/texture.png",
			valid: append([]byte("\x89PNG\r\n\x1a\n"), 0),
			bad:   []byte("GIF89a"),
		},
		{
			url:   "https://storage2.exmeaning.com/sekai-jp-assets/scenario/background/bg/bg.webp",
			valid: []byte("RIFF\x04\x00\x00\x00WEBP"),
			bad:   []byte("RIFF\x04\x00\x00\x00NOPE"),
		},
		{
			url:   "https://storage2.exmeaning.com/sekai-jp-assets/sound/scenario/voice/demo/voice.mp3",
			valid: []byte("ID3\x04\x00"),
			bad:   []byte("{\"error\":true}"),
		},
	}
	for _, tc := range cases {
		spec, err := store.assetSpec(tc.url)
		if err != nil {
			t.Fatalf("assetSpec(%q): %v", tc.url, err)
		}
		if !spec.validate(tc.valid) {
			t.Errorf("validator rejected valid body for %s", tc.url)
		}
		if spec.validate(tc.bad) {
			t.Errorf("validator accepted invalid body for %s", tc.url)
		}
	}
}
