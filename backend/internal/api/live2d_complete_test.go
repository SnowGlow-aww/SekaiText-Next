package api

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sekaitext/backend/internal/model"
)

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

func TestLive2DHostPolicyRequiresExactHTTPSHost(t *testing.T) {
	for _, raw := range []string{
		"http://storage.sekai.best/sekai-live2d-assets/live2d/model_list.json",
		"https://storage.sekai.best.attacker.invalid/live2d/model_list.json",
		"https://storage.sekai.best@attacker.invalid/live2d/model_list.json",
		"https://127.0.0.1/live2d/model_list.json",
	} {
		if live2dHostAllowed(raw) {
			t.Errorf("live2dHostAllowed(%q) = true", raw)
		}
	}
	if raw := "https://storage.sekai.best/sekai-live2d-assets/live2d/model_list.json"; !live2dHostAllowed(raw) {
		t.Errorf("live2dHostAllowed(%q) = false", raw)
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
	for _, tc := range []struct {
		url  string
		body []byte
		want bool
	}{
		{"https://storage.sekai.best/model.model3", []byte(`{"FileReferences":{}}`), true},
		{"https://storage.sekai.best/model.model3", []byte("<html>error</html>"), false},
		{"https://storage.sekai.best/texture.png", validPNG, true},
		{"https://storage.sekai.best/texture.png", []byte("truncated"), false},
		{"https://storage.sekai.best/model.moc3", []byte("MOC3data"), true},
		{"https://storage.sekai.best/untyped.bin", []byte("arbitrary"), false},
	} {
		if got := live2dAssetBodyValid(tc.url, tc.body); got != tc.want {
			t.Errorf("live2dAssetBodyValid(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}
