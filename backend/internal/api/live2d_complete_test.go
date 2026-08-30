package api

import (
	"bytes"
	"context"
	"embed"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sekaitext/backend/internal/config"
	"sekaitext/backend/internal/model"
)

//go:embed testdata/live2d/*
var live2dTestdata embed.FS

// TestLive2DModelComplete is a regression test for the sync completeness check:
// deleting ANY body file (not just the moc3) must make a model count as incomplete
// so the next sync repairs it. Previously only the moc3 was checked, so a deleted
// texture/physics went unnoticed and the sync falsely reported "最新最全 / done".
func TestLive2DModelComplete(t *testing.T) {
	dir := t.TempDir()
	const base = "testmodel"
	model3 := `{"FileReferences":{"Moc":"testmodel.moc3","Textures":["testmodel.2048/texture_00.png"],"Physics":"testmodel.physics3.json"}}`

	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	setup := func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		write(base+".model3", model3)
		write("buildmodeldata.json", `{"Moc3FileName":"testmodel.moc3.bytes"}`)
		write(base+".moc3", "MOC3")
		write(base+".2048/texture_00.png", "\x89PNG\r\n\x1a\npayload")
		write(base+".physics3", `{}`)
	}

	setup()
	if !live2dModelComplete(dir, base+".model3.json") {
		t.Fatal("fully-populated model should be reported complete")
	}

	// Deleting any single body file must flip it to incomplete.
	for _, victim := range []string{
		base + ".2048/texture_00.png", // the reported case: a deleted texture
		base + ".moc3",
		base + ".physics3",
		"buildmodeldata.json",
		base + ".model3",
	} {
		setup()
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(victim))); err != nil {
			t.Fatal(err)
		}
		if live2dModelComplete(dir, base+".model3.json") {
			t.Errorf("model missing %q must be reported incomplete (old bug: only moc3 was checked)", victim)
		}
	}
}

func TestLive2DModelCompleteSelectsExpectedModel(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("buildmodeldata.json", `{"Moc3FileName":"expected.moc3.bytes"}`)
	// A complete stale model must not mask the selected model's missing model3.
	write("stale.model3", `{"FileReferences":{"Moc":"stale.moc3","Textures":[]}}`)
	write("stale.moc3", "MOC3stale")
	if live2dModelComplete(dir, "expected.model3.json") {
		t.Fatal("stale model assets satisfied completeness for the selected model")
	}
}

func TestValidateLive2DModelList(t *testing.T) {
	valid := live2dModelListEntry{
		ModelName: "model", ModelBase: "01ichika_normal",
		ModelPath: "v1/main/01_ichika/01ichika_normal", ModelFile: "01ichika.model3.json",
	}
	if refs, err := validateLive2DModelList([]live2dModelListEntry{valid, valid}); err != nil || len(refs) != 1 {
		t.Fatalf("valid list: refs=%v err=%v", refs, err)
	}
	for _, entries := range [][]live2dModelListEntry{
		nil,
		{{ModelName: "bad", ModelBase: "base", ModelPath: "../escape", ModelFile: "bad.model3.json"}},
		{{ModelName: "bad", ModelBase: "base", ModelPath: "v1/model", ModelFile: "not-a-model"}},
	} {
		if _, err := validateLive2DModelList(entries); err == nil {
			t.Fatalf("invalid model list accepted: %+v", entries)
		}
	}
}

func TestCompleteLive2DSyncPreservesCancellation(t *testing.T) {
	progress := &model.Live2DSyncProgress{Status: "canceled", Error: "user canceled"}
	completeLive2DSync(context.Background(), progress)
	if progress.Status != "canceled" || progress.Error != "user canceled" {
		t.Fatalf("cancellation became terminal success: %+v", progress)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	progress = &model.Live2DSyncProgress{Status: "downloading"}
	completeLive2DSync(ctx, progress)
	if progress.Status != "canceled" {
		t.Fatalf("canceled context became %q", progress.Status)
	}
}

func TestLive2DRootLockCanonicalizesSymlinkAliases(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	unlock, err := live2dLockPath(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if secondUnlock, err := live2dLockPath(ctx, alias); err == nil {
		secondUnlock()
		t.Fatal("symlink alias bypassed the root lock")
	}
}

// TestLive2DSekaiFallback checks the exmeaning/CDN → sekai.best URL remap used when
// a body file (e.g. a texture) is missing from exmeaning but present on sekai.best.
func TestLive2DSekaiFallback(t *testing.T) {
	rel := "/live2d/model/v1/main/17_kanade/17kanade_black/17kanade_black_t01.2048/texture_00.png"
	got := live2dSekaiFallback(live2dExmeaning + rel)
	if want := live2dSekaiBest + rel; got != want {
		t.Errorf("fallback = %q, want %q", got, want)
	}
	// A URL that isn't an exmeaning/CDN body URL (model_list/motion) yields no fallback.
	if got := live2dSekaiFallback(live2dSekaiBest + "/live2d/model_list.json"); got != "" {
		t.Errorf("expected no fallback for a sekai.best URL, got %q", got)
	}
}

func TestLive2DHostPolicyRequiresExactOriginAndPathFamily(t *testing.T) {
	for _, raw := range []string{
		"https://storage.sekai.best/sekai-live2d-assets/live2d/model_list.json",
		"https://storage.sekai.best:443/sekai-live2d-assets/live2d/motion/demo/motion.motion3.json",
		"https://storage2.exmeaning.com/sekai-jp-assets/live2d/model/v1/demo/model.model3",
		"https://storage2.exmeaning.com/sekai-jp-assets/scenario/background/bg/bg.webp",
		"https://storage2.exmeaning.com/sekai-jp-assets/sound/scenario/bgm/bgm/bgm.mp3",
		"https://storage.exmeaning.com/sekai-jp-assets/sound/scenario/voice/event/voice.mp3",
		"https://storage2.exmeaning.com/sekai-jp-assets/sound/card_scenario/voice/card/voice.mp3",
		"https://storage2.exmeaning.com/sekai-jp-assets/event_story/event_208/scenario/event_208_01.json",
		"https://storage2.exmeaning.com/sekai-jp-assets/character/member/res012_no043/012043_touya01.json",
		"https://assets.unipjsk.com/startapp/scenario/unitstory/leo/episode.json",
		"https://sekai-assets-bdf29c81.seiunx.net/jp-assets/ondemand/event_story/event/scenario/episode.json",
	} {
		if !live2dHostAllowed(raw) {
			t.Errorf("live2dHostAllowed(%q) = false", raw)
		}
	}

	for _, raw := range []string{
		"http://storage.sekai.best/sekai-live2d-assets/live2d/model_list.json",
		"https://storage.sekai.best:444/sekai-live2d-assets/live2d/model_list.json",
		"https://storage.sekai.best.attacker.invalid/sekai-live2d-assets/live2d/model_list.json",
		"https://storage.sekai.best@attacker.invalid/sekai-live2d-assets/live2d/model_list.json",
		"https://attacker@storage.sekai.best/sekai-live2d-assets/live2d/model_list.json",
		"https://127.0.0.1/sekai-live2d-assets/live2d/model_list.json",
		"https://storage.sekai.best/sekai-live2d-assets/private/secrets.json",
		"https://storage.sekai.best/sekai-live2d-assets/live2d/model/demo/file.exe",
		"https://storage.sekai.best/sekai-live2d-assets/live2d/model/../private.webp",
		"https://storage.sekai.best/sekai-live2d-assets/live2d/model/demo.webp?cache=1",
		"https://storage.sekai.best/sekai-live2d-assets/live2d/model/demo.webp#fragment",
		"https://storage.sekai.best/sekai-live2d-assets/live2d/%2fetc/passwd.json",
		"https://storage2.exmeaning.com/unrelated/background.webp",
		"https://storage2.exmeaning.com/sekai-jp-assets/sound/unrelated/file.mp3",
		"https://sakimizuki.accr.cc/sekaitext-plugins/index.json",
	} {
		if live2dHostAllowed(raw) {
			t.Errorf("live2dHostAllowed(%q) = true", raw)
		}
	}
}

func TestReadLive2DBoundedBodyRejectsUnknownLengthOverflow(t *testing.T) {
	_, err := readLive2DBoundedBody(bytes.NewReader([]byte("12345")), -1, 4)
	if err == nil {
		t.Fatal("expected unknown-length overflow to be rejected")
	}
}

func TestLive2DAssetBodyValidation(t *testing.T) {
	validPNG := append([]byte("\x89PNG\r\n\x1a\n"), []byte("payload")...)
	validVP8 := live2dTestFixture(t, "static-vp8.webp")
	validVP8L := live2dTestFixture(t, "static-vp8l.webp")
	webpShell := live2dTestWebPShell()
	vp8HeaderShell := make([]byte, 32)
	copy(vp8HeaderShell[0:4], "RIFF")
	binary.LittleEndian.PutUint32(vp8HeaderShell[4:8], uint32(len(vp8HeaderShell)-8))
	copy(vp8HeaderShell[8:12], "WEBP")
	copy(vp8HeaderShell[12:16], "VP8 ")
	binary.LittleEndian.PutUint32(vp8HeaderShell[16:20], 12)
	vp8HeaderShell[20] = 0x40 // key frame with a declared two-byte first partition.
	copy(vp8HeaderShell[23:26], "\x9d\x01\x2a")
	vp8HeaderShell[26] = 0x01
	vp8HeaderShell[28] = 0x01
	vp8lHeaderShell := make([]byte, 26)
	copy(vp8lHeaderShell[0:4], "RIFF")
	binary.LittleEndian.PutUint32(vp8lHeaderShell[4:8], uint32(len(vp8lHeaderShell)-8))
	copy(vp8lHeaderShell[8:12], "WEBP")
	copy(vp8lHeaderShell[12:16], "VP8L")
	binary.LittleEndian.PutUint32(vp8lHeaderShell[16:20], 6)
	vp8lHeaderShell[20] = 0x2f
	truncatedWebP := append([]byte(nil), validVP8[:len(validVP8)-1]...)
	binary.LittleEndian.PutUint32(truncatedWebP[4:8], uint32(len(truncatedWebP)-8))
	malformedWebP := append([]byte(nil), validVP8...)
	copy(malformedWebP[8:12], "NOPE")
	badWebPSize := append([]byte(nil), validVP8...)
	binary.LittleEndian.PutUint32(badWebPSize[4:8], uint32(len(validVP8)))

	validMP3 := live2dTestFixture(t, "tone-id3v2.mp3")
	id3End, ok := live2dID3v2End(validMP3)
	if !ok {
		t.Fatal("real MP3 fixture has no valid ID3v2 tag")
	}
	id3Only := append([]byte(nil), validMP3[:id3End]...)
	validMPEG := live2dTestMPEGFrame()
	truncatedMPEG := append([]byte(nil), validMPEG[:len(validMPEG)-1]...)
	headerOnlyMPEG := append([]byte(nil), validMPEG[:4]...)
	malformedMPEG := append([]byte(nil), validMPEG...)
	malformedMPEG[2] &= 0x0f // free/reserved bitrate index is not bounded.

	for _, tc := range []struct {
		name string
		url  string
		body []byte
		want bool
	}{
		{name: "json", url: "https://storage.sekai.best/model.model3", body: []byte(`{"FileReferences":{}}`), want: true},
		{name: "invalid json", url: "https://storage.sekai.best/model.model3", body: []byte("<html>error</html>"), want: false},
		{name: "png", url: "https://storage.sekai.best/texture.png", body: validPNG, want: true},
		{name: "truncated png", url: "https://storage.sekai.best/texture.png", body: []byte("truncated"), want: false},
		{name: "moc3", url: "https://storage.sekai.best/model.moc3", body: []byte("MOC3data"), want: true},
		{name: "real static vp8 webp", url: "https://storage.sekai.best/texture.webp", body: validVP8, want: true},
		{name: "real static vp8l webp", url: "https://storage.sekai.best/texture.webp", body: validVP8L, want: true},
		{name: "vp8x shell", url: "https://storage.sekai.best/texture.webp", body: webpShell, want: false},
		{name: "vp8 header shell", url: "https://storage.sekai.best/texture.webp", body: vp8HeaderShell, want: false},
		{name: "vp8l header shell", url: "https://storage.sekai.best/texture.webp", body: vp8lHeaderShell, want: false},
		{name: "truncated vp8 chunk", url: "https://storage.sekai.best/texture.webp", body: truncatedWebP, want: false},
		{name: "malformed webp", url: "https://storage.sekai.best/texture.webp", body: malformedWebP, want: false},
		{name: "webp size mismatch", url: "https://storage.sekai.best/texture.webp", body: badWebPSize, want: false},
		{name: "real id3v2 mp3", url: "https://storage.sekai.best/voice.mp3", body: validMP3, want: true},
		{name: "complete real mpeg frame", url: "https://storage.sekai.best/voice.mp3", body: validMPEG, want: true},
		{name: "id3 only", url: "https://storage.sekai.best/voice.mp3", body: id3Only, want: false},
		{name: "mpeg header only", url: "https://storage.sekai.best/voice.mp3", body: headerOnlyMPEG, want: false},
		{name: "truncated mpeg frame", url: "https://storage.sekai.best/voice.mp3", body: truncatedMPEG, want: false},
		{name: "malformed mpeg frame", url: "https://storage.sekai.best/voice.mp3", body: malformedMPEG, want: false},
		{name: "extension alone is not enough", url: "https://storage.sekai.best/asset.webp", body: []byte("arbitrary"), want: false},
		{name: "untyped asset", url: "https://storage.sekai.best/untyped.bin", body: []byte("arbitrary"), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := live2dAssetBodyValid(tc.url, tc.body); got != tc.want {
				t.Errorf("live2dAssetBodyValid(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

func TestLive2DContentTypes(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
	}{
		{path: "https://storage.sekai.best/texture.png?cache=1", want: "image/png"},
		{path: "https://storage.sekai.best/texture.WEBP?cache=1", want: "image/webp"},
		{path: "/local/voice.MP3", want: "audio/mpeg"},
		{path: "/local/model.model3", want: "application/json"},
		{path: "/local/model.moc3", want: "application/octet-stream"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if got := live2dContentType(tc.path); got != tc.want {
				t.Errorf("live2dContentType(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

type live2dRoundTripFunc func(*http.Request) (*http.Response, error)

func (f live2dRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func live2dTestResponse(req *http.Request, status int, body []byte, contentLength int64) *http.Response {
	if contentLength < 0 {
		contentLength = int64(len(body))
	}
	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Header:        make(http.Header),
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: contentLength,
		Request:       req,
	}
}

func live2dTestHandler(t *testing.T, root string, transport http.RoundTripper) *Handler {
	t.Helper()
	oldClient := live2dSyncHTTP
	live2dSyncHTTP = &http.Client{Transport: transport, CheckRedirect: oldClient.CheckRedirect}
	t.Cleanup(func() { live2dSyncHTTP = oldClient })
	return &Handler{cfg: &config.AppConfig{Live2DLocalDir: root}}
}

func live2dProxyRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()
	return httptest.NewRequest(http.MethodGet, "/api/v1/live2d/proxy?url="+url.QueryEscape(rawURL), nil)
}

func live2dTestFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := live2dTestdata.ReadFile("testdata/live2d/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

func live2dTestFixtureNoT(name string) []byte {
	body, err := live2dTestdata.ReadFile("testdata/live2d/" + name)
	if err != nil {
		panic(err)
	}
	return body
}

func live2dTestWebP() []byte {
	return live2dTestFixtureNoT("static-vp8.webp")
}

func live2dTestWebPShell() []byte {
	body := make([]byte, 30)
	copy(body[0:4], "RIFF")
	binary.LittleEndian.PutUint32(body[4:8], 22)
	copy(body[8:12], "WEBP")
	copy(body[12:16], "VP8X")
	binary.LittleEndian.PutUint32(body[16:20], 10)
	return body
}

func live2dTestID3() []byte {
	return live2dTestFixtureNoT("tone-id3v2.mp3")
}

func live2dTestMPEGFrame() []byte {
	body := live2dTestFixtureNoT("tone-id3v2.mp3")
	offset, ok := live2dID3v2End(body)
	if !ok {
		panic("real MP3 fixture has invalid ID3v2 header")
	}
	frameLength, ok := live2dMPEGFrameLength(body[offset:])
	if !ok || offset+frameLength > len(body) {
		panic("real MP3 fixture has no complete MPEG frame")
	}
	return append([]byte(nil), body[offset:offset+frameLength]...)
}

func TestLive2DProxyServesValidatedWebPAndMP3(t *testing.T) {
	vp8 := live2dTestFixture(t, "static-vp8.webp")
	vp8l := live2dTestFixture(t, "static-vp8l.webp")
	mp3 := live2dTestFixture(t, "tone-id3v2.mp3")
	for _, tc := range []struct {
		name       string
		rawURL     string
		body       []byte
		local      bool
		wantType   string
		wantSource string
	}{
		{name: "cached real vp8 webp", rawURL: "https://storage.sekai.best/sekai-live2d-assets/live2d/model/background.webp", body: vp8, local: true, wantType: "image/webp", wantSource: "local"},
		{name: "cached real vp8l webp", rawURL: "https://storage.sekai.best/sekai-live2d-assets/live2d/model/background-vp8l.webp", body: vp8l, local: true, wantType: "image/webp", wantSource: "local"},
		{name: "upstream real vp8 webp", rawURL: "https://storage2.exmeaning.com/sekai-jp-assets/scenario/background/bg/bg.webp", body: vp8, wantType: "image/webp", wantSource: "cdn"},
		{name: "upstream real vp8l webp", rawURL: "https://storage2.exmeaning.com/sekai-jp-assets/scenario/background/bg-vp8l/bg-vp8l.webp", body: vp8l, wantType: "image/webp", wantSource: "cdn"},
		{name: "upstream real id3v2 mp3", rawURL: "https://storage2.exmeaning.com/sekai-jp-assets/sound/scenario/bgm/bgm/bgm.mp3", body: mp3, wantType: "audio/mpeg", wantSource: "cdn"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			calls := 0
			h := live2dTestHandler(t, root, live2dRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				return live2dTestResponse(req, http.StatusOK, tc.body, -1), nil
			}))
			if tc.local {
				path := live2dLocalPath(root, tc.rawURL)
				if path == "" {
					t.Fatal("test URL did not resolve to a local mirror path")
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, tc.body, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			rr := httptest.NewRecorder()
			h.Live2DProxy(rr, live2dProxyRequest(t, tc.rawURL))
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
			}
			if got := rr.Header().Get("Content-Type"); got != tc.wantType {
				t.Errorf("Content-Type = %q, want %q", got, tc.wantType)
			}
			if got := rr.Header().Get("X-Live2D-Source"); got != tc.wantSource {
				t.Errorf("X-Live2D-Source = %q, want %q", got, tc.wantSource)
			}
			if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if !bytes.Equal(rr.Body.Bytes(), tc.body) {
				t.Errorf("proxy body changed: got %d bytes, want %d", rr.Body.Len(), len(tc.body))
			}
			if tc.local && calls != 0 {
				t.Errorf("cached asset contacted upstream %d time(s)", calls)
			}
			if !tc.local && calls != 1 {
				t.Errorf("upstream call count = %d, want 1", calls)
			}
		})
	}
}

func TestLive2DProxyRejectsInvalidBodiesForCachedAndUpstreamAssets(t *testing.T) {
	mp3 := live2dTestFixture(t, "tone-id3v2.mp3")
	id3End, ok := live2dID3v2End(mp3)
	if !ok {
		t.Fatal("real MP3 fixture has no valid ID3v2 tag")
	}
	frame := live2dTestMPEGFrame()
	for _, tc := range []struct {
		name   string
		rawURL string
		body   []byte
		local  bool
	}{
		{name: "invalid cached webp", rawURL: "https://storage.sekai.best/sekai-live2d-assets/live2d/model/background.webp", body: live2dTestWebPShell(), local: true},
		{name: "invalid upstream webp shell", rawURL: "https://storage2.exmeaning.com/sekai-jp-assets/scenario/background/bg/bg.webp", body: live2dTestWebPShell()},
		{name: "id3-only upstream mp3", rawURL: "https://storage2.exmeaning.com/sekai-jp-assets/sound/scenario/bgm/bgm/bgm.mp3", body: append([]byte(nil), mp3[:id3End]...)},
		{name: "header-only upstream mp3", rawURL: "https://storage2.exmeaning.com/sekai-jp-assets/sound/scenario/voice/event/voice.mp3", body: append([]byte(nil), frame[:4]...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			calls := 0
			h := live2dTestHandler(t, root, live2dRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				return live2dTestResponse(req, http.StatusOK, tc.body, -1), nil
			}))
			if tc.local {
				path := live2dLocalPath(root, tc.rawURL)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, tc.body, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			rr := httptest.NewRecorder()
			h.Live2DProxy(rr, live2dProxyRequest(t, tc.rawURL))
			if rr.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
			}
			if calls != 1 {
				t.Errorf("invalid asset upstream calls = %d, want 1", calls)
			}
		})
	}
}

func TestLive2DProxyFallsBackFromInvalidCachedAsset(t *testing.T) {
	root := t.TempDir()
	rawURL := "https://storage.sekai.best/sekai-live2d-assets/live2d/model/background.webp"
	path := live2dLocalPath(root, rawURL)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a webp"), 0o644); err != nil {
		t.Fatal(err)
	}
	valid := live2dTestWebP()
	calls := 0
	h := live2dTestHandler(t, root, live2dRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return live2dTestResponse(req, http.StatusOK, valid, -1), nil
	}))

	rr := httptest.NewRecorder()
	h.Live2DProxy(rr, live2dProxyRequest(t, rawURL))
	if rr.Code != http.StatusOK || rr.Header().Get("X-Live2D-Source") != "cdn" {
		t.Fatalf("invalid cached asset result = status %d source %q; body=%s", rr.Code, rr.Header().Get("X-Live2D-Source"), rr.Body.String())
	}
	if !bytes.Equal(rr.Body.Bytes(), valid) {
		t.Fatal("fallback body did not match validated upstream body")
	}
	if calls != 1 {
		t.Fatalf("fallback upstream calls = %d, want 1", calls)
	}
}

func TestLive2DProxyEnforcesSizeLimit(t *testing.T) {
	for _, tc := range []struct {
		name       string
		local      bool
		wantStatus int
		wantCalls  int
	}{
		{name: "cached asset", local: true, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "upstream asset", wantStatus: http.StatusBadGateway, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			calls := 0
			rawURL := "https://storage.sekai.best/sekai-live2d-assets/live2d/model/background.webp"
			h := live2dTestHandler(t, root, live2dRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				return live2dTestResponse(req, http.StatusOK, nil, maxLive2DAssetBytes+1), nil
			}))
			if tc.local {
				path := live2dLocalPath(root, rawURL)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.Truncate(maxLive2DAssetBytes + 1); err != nil {
					f.Close()
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}

			rr := httptest.NewRecorder()
			h.Live2DProxy(rr, live2dProxyRequest(t, rawURL))
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if calls != tc.wantCalls {
				t.Errorf("upstream calls = %d, want %d", calls, tc.wantCalls)
			}
		})
	}
}

func TestLive2DProxyHostAndPathRejection(t *testing.T) {
	calls := 0
	h := live2dTestHandler(t, "", live2dRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return live2dTestResponse(req, http.StatusOK, live2dTestWebP(), -1), nil
	}))
	for _, rawURL := range []string{
		"http://storage.sekai.best/sekai-live2d-assets/live2d/model/background.webp",
		"https://storage.sekai.best.attacker.invalid/sekai-live2d-assets/live2d/model/background.webp",
		"https://storage.sekai.best@127.0.0.1/sekai-live2d-assets/live2d/model/background.webp",
		"https://127.0.0.1/sekai-live2d-assets/live2d/model/background.webp",
	} {
		t.Run(rawURL, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.Live2DProxy(rr, live2dProxyRequest(t, rawURL))
			if rr.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rr.Code)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("rejected hosts contacted upstream %d time(s)", calls)
	}

	root := t.TempDir()
	for _, tc := range []struct {
		name string
		url  string
		want bool
	}{
		{name: "missing live2d segment", url: "https://storage.sekai.best/sekai-live2d-assets/model/background.webp", want: false},
		{name: "path traversal", url: "https://storage.sekai.best/sekai-live2d-assets/live2d/model/../background.webp", want: false},
		{name: "unsupported live2d category", url: "https://storage.sekai.best/sekai-live2d-assets/live2d/sound/background.mp3", want: false},
		{name: "model path", url: "https://storage.sekai.best/sekai-live2d-assets/live2d/model/background.webp", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := live2dLocalPath(root, tc.url) != ""; got != tc.want {
				t.Errorf("live2dLocalPath(%q) present = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

func TestLive2DProxyRedirectPolicy(t *testing.T) {
	for _, tc := range []struct {
		name        string
		redirectURL string
		wantStatus  int
		wantCalls   int
		wantSource  string
	}{
		{name: "allowed redirect", redirectURL: "https://storage.sekai.best/sekai-live2d-assets/live2d/model/final.webp", wantStatus: http.StatusOK, wantCalls: 2, wantSource: "cdn"},
		{name: "disallowed redirect", redirectURL: "https://127.0.0.1/internal", wantStatus: http.StatusBadGateway, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			rawURL := "https://storage.sekai.best/sekai-live2d-assets/live2d/model/redirect.webp"
			h := live2dTestHandler(t, "", live2dRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					resp := live2dTestResponse(req, http.StatusFound, nil, 0)
					resp.Header.Set("Location", tc.redirectURL)
					return resp, nil
				}
				return live2dTestResponse(req, http.StatusOK, live2dTestWebP(), -1), nil
			}))

			rr := httptest.NewRecorder()
			h.Live2DProxy(rr, live2dProxyRequest(t, rawURL))
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if calls != tc.wantCalls {
				t.Errorf("upstream calls = %d, want %d", calls, tc.wantCalls)
			}
			if got := rr.Header().Get("X-Live2D-Source"); got != tc.wantSource {
				t.Errorf("X-Live2D-Source = %q, want %q", got, tc.wantSource)
			}
		})
	}
}

func TestLive2DCDNUpstream(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rawURL string
		want   string
	}{
		{
			name:   "exmeaning2 sound rewrites to storage.exmeaning.com",
			rawURL: "https://storage2.exmeaning.com/sekai-jp-assets/sound/scenario/bgm/bgm01/bgm01.mp3",
			want:   "https://storage.exmeaning.com/sekai-jp-assets/sound/scenario/bgm/bgm01/bgm01.mp3",
		},
		{
			name:   "storage sound remains on storage.exmeaning.com",
			rawURL: "https://storage.exmeaning.com/sekai-jp-assets/sound/scenario/voice/sid/vid.mp3",
			want:   "https://storage.exmeaning.com/sekai-jp-assets/sound/scenario/voice/sid/vid.mp3",
		},
		{
			name:   "exmeaning2 model rewrites to edge CDN",
			rawURL: "https://storage2.exmeaning.com/sekai-jp-assets/live2d/model/v2/demo/buildmodeldata.json",
			want:   "https://sakimizuki.accr.cc/sekai-jp-assets/live2d/model/v2/demo/buildmodeldata.json",
		},
		{
			name:   "storage model rewrites to edge CDN",
			rawURL: "https://storage.exmeaning.com/sekai-jp-assets/live2d/model/v2/demo/buildmodeldata.json",
			want:   "https://sakimizuki.accr.cc/sekai-jp-assets/live2d/model/v2/demo/buildmodeldata.json",
		},
		{
			name:   "exmeaning2 background rewrites to edge CDN",
			rawURL: "https://storage2.exmeaning.com/sekai-jp-assets/scenario/background/bg/bg.webp",
			want:   "https://sakimizuki.accr.cc/sekai-jp-assets/scenario/background/bg/bg.webp",
		},
		{
			name:   "sekai.best motion untouched",
			rawURL: "https://storage.sekai.best/sekai-live2d-assets/live2d/motion/demo/motion.motion3.json",
			want:   "https://storage.sekai.best/sekai-live2d-assets/live2d/motion/demo/motion.motion3.json",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := live2dCDNUpstream(tc.rawURL); got != tc.want {
				t.Errorf("live2dCDNUpstream(%q) = %q, want %q", tc.rawURL, got, tc.want)
			}
		})
	}
}
