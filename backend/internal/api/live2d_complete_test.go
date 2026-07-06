package api

import (
	"os"
	"path/filepath"
	"testing"
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
		write(base+".2048/texture_00.png", "PNGDATA")
		write(base+".physics3", "PHYS")
	}

	setup()
	if !live2dModelComplete(dir) {
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
		if live2dModelComplete(dir) {
			t.Errorf("model missing %q must be reported incomplete (old bug: only moc3 was checked)", victim)
		}
	}
}
