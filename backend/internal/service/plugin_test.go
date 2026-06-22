package service

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writePlugin(t *testing.T, dir, id, version string) {
	t.Helper()
	pdir := filepath.Join(dir, id)
	if err := os.MkdirAll(pdir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"` + id + `","name":"` + id + `","version":"` + version + `","entry":"entry.js"}`
	if err := os.WriteFile(filepath.Join(pdir, "manifest.json"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "entry.js"), []byte("export function setup(){}"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPluginStoreListAndState(t *testing.T) {
	dir := t.TempDir()
	s := NewPluginStore(dir)

	// Empty store lists nothing.
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}

	writePlugin(t, dir, "alpha", "1.0.0")
	writePlugin(t, dir, "beta", "2.1.0")

	list, _ = s.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(list))
	}
	// Sorted by id; default enabled.
	if list[0].ID != "alpha" || !list[0].Enabled {
		t.Fatalf("alpha should be first and enabled: %+v", list[0])
	}

	// Disable beta, verify it persists.
	if err := s.SetEnabled("beta", false); err != nil {
		t.Fatal(err)
	}
	list, _ = s.List()
	for _, p := range list {
		if p.ID == "beta" && p.Enabled {
			t.Fatal("beta should be disabled")
		}
	}
}

func TestPluginStoreUninstall(t *testing.T) {
	dir := t.TempDir()
	s := NewPluginStore(dir)
	writePlugin(t, dir, "alpha", "1.0.0")

	// Disable it first, then uninstall removes the dir + state entry.
	if err := s.SetEnabled("alpha", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Uninstall("alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "alpha")); !os.IsNotExist(err) {
		t.Fatal("alpha dir should be gone")
	}
	list, _ := s.List()
	if len(list) != 0 {
		t.Fatalf("expected empty list after uninstall, got %d", len(list))
	}
}

func TestVersionNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.0.1", "1.0.0", true},
		{"1.1.0", "1.0.9", true},
		{"2.0.0", "1.9.9", true},
		{"1.0.0", "1.0.0", false},
		{"1.0.0", "1.0.1", false},
		{"1.0.0-alpha", "1.0.0", false}, // pre-release suffix ignored
	}
	for _, c := range cases {
		if got := versionNewer(c.a, c.b); got != c.want {
			t.Errorf("versionNewer(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

// makeSekplugin writes a .sekplugin (zip) with the given manifest JSON + an
// entry.js, returning its path.
func makeSekplugin(t *testing.T, manifestJSON string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pkg.sekplugin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	mw, _ := zw.Create("manifest.json")
	mw.Write([]byte(manifestJSON))
	ew, _ := zw.Create("entry.js")
	ew.Write([]byte("export function setup(){}"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInstallHappyPath(t *testing.T) {
	dir := t.TempDir()
	s := NewPluginStore(dir)
	pkg := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"1.0.0","minHostVersion":"2.0.0","entry":"entry.js"}`)

	m, err := s.Install(pkg, "3.0.0", "")
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if m.ID != "demo" {
		t.Fatalf("expected id demo, got %s", m.ID)
	}
	if _, err := os.Stat(filepath.Join(dir, "demo", "entry.js")); err != nil {
		t.Fatal("entry.js not extracted")
	}
	list, _ := s.List()
	if len(list) != 1 || !list[0].Enabled {
		t.Fatalf("expected 1 enabled plugin, got %+v", list)
	}
}

func TestInstallIncompatibleVersion(t *testing.T) {
	s := NewPluginStore(t.TempDir())
	pkg := makeSekplugin(t, `{"id":"demo","version":"1.0.0","minHostVersion":"2.0.0","entry":"entry.js"}`)
	m, err := s.Install(pkg, "1.0.0", "")
	if err != ErrIncompatible {
		t.Fatalf("expected ErrIncompatible, got %v", err)
	}
	if m.MinHostVersion != "2.0.0" {
		t.Fatalf("manifest should be returned for messaging, got %+v", m)
	}
}

func TestInstallRejectsBadId(t *testing.T) {
	s := NewPluginStore(t.TempDir())
	pkg := makeSekplugin(t, `{"id":"../evil","version":"1.0.0","entry":"entry.js"}`)
	if _, err := s.Install(pkg, "3.0.0", ""); err == nil {
		t.Fatal("expected error for path-escaping id")
	}
}

func TestInstallExpectIDMismatch(t *testing.T) {
	dir := t.TempDir()
	s := NewPluginStore(dir)
	// Package advertises id "hello" but the market entry expected "newplug".
	pkg := makeSekplugin(t, `{"id":"hello","version":"1.0.0","entry":"entry.js"}`)
	_, err := s.Install(pkg, "3.0.0", "newplug")
	if !errors.Is(err, ErrIDMismatch) {
		t.Fatalf("expected ErrIDMismatch, got %v", err)
	}
	// Nothing should have been written for either id.
	if _, e := os.Stat(filepath.Join(dir, "hello")); !os.IsNotExist(e) {
		t.Fatal("mismatched package must not be extracted")
	}
}
