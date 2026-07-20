package service

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func testPluginProvenance(version string) PluginProvenance {
	return PluginProvenance{
		Source:       "verified-market",
		Publisher:    OfficialPluginPublisher,
		KeyID:        "test-2026-01",
		SHA256:       strings.Repeat("a", 64),
		IndexVersion: 2,
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
	// Unprovenanced legacy payloads are sorted but fail closed until approved.
	if list[0].ID != "alpha" || list[0].Enabled {
		t.Fatalf("alpha should be first and approval-gated: %+v", list[0])
	}
	if err := s.SetEnabled("alpha", true, true); err != nil {
		t.Fatal(err)
	}

	// Disable beta, verify it persists.
	if err := s.SetEnabled("beta", false, false); err != nil {
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
	if err := s.SetEnabled("alpha", false, false); err != nil {
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
		{"1.0.0-alpha", "1.0.0", false},
		{"1.0.0", "1.0.0-rc.1", true},
		{"1.0.0-beta.2", "1.0.0-beta.1", true},
		{"01.0.0", "1.0.0", false},
	}
	for _, c := range cases {
		if got := versionNewer(c.a, c.b); got != c.want {
			t.Errorf("versionNewer(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestUpdateVersionChannelHandling(t *testing.T) {
	if updateVersionAllowed("2.0.0-beta.1", "1.0.0") {
		t.Fatal("stable install must not auto-update onto a prerelease channel")
	}
	if !updateVersionAllowed("2.0.0", "2.0.0-rc.1") {
		t.Fatal("prerelease install should be allowed to advance to stable")
	}
	if updateVersionAllowed("1.0", "0.9.0") {
		t.Fatal("invalid semver must not be considered an update")
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

	m, err := s.installVerifiedMarket(pkg, "3.0.0", "demo", "1.0.0", testPluginProvenance("1.0.0"), nil)
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

func TestLocalInstallDefaultsDisabled(t *testing.T) {
	dir := t.TempDir()
	s := NewPluginStore(dir)
	pkg := makeSekplugin(t, `{"id":"local_demo","name":"Local Demo","version":"1.0.0","entry":"entry.js"}`)

	if _, err := s.InstallLocal(pkg, "3.0.0"); err != nil {
		t.Fatalf("local install failed: %v", err)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Enabled || !list[0].Local {
		t.Fatalf("local plugin should be marked local and disabled: %+v", list)
	}
}

func TestInstallRetainsBackupAndRollbackRestoresIt(t *testing.T) {
	dir := t.TempDir()
	s := NewPluginStore(dir)
	v1 := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"1.0.0","entry":"entry.js"}`)
	v2 := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"2.0.0","entry":"entry.js"}`)

	if _, err := s.installVerifiedMarket(v1, "3.0.0", "demo", "1.0.0", testPluginProvenance("1.0.0"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.installVerifiedMarket(v2, "3.0.0", "demo", "2.0.0", testPluginProvenance("2.0.0"), nil); err != nil {
		t.Fatal(err)
	}
	backup, err := readManifestAt(s.backupPath("demo"))
	if err != nil || backup.Version != "1.0.0" {
		t.Fatalf("expected retained v1 backup, got %+v, err=%v", backup, err)
	}
	active, err := s.readManifest("demo")
	if err != nil || active.Version != "2.0.0" {
		t.Fatalf("expected active v2, got %+v, err=%v", active, err)
	}

	restored, err := s.Rollback("demo")
	if err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	if restored.Version != "1.0.0" {
		t.Fatalf("expected rollback manifest v1, got %+v", restored)
	}
	active, err = s.readManifest("demo")
	if err != nil || active.Version != "1.0.0" {
		t.Fatalf("expected active v1 after rollback, got %+v, err=%v", active, err)
	}
	if _, err := os.Stat(s.backupPath("demo")); !os.IsNotExist(err) {
		t.Fatalf("backup should be consumed after rollback, err=%v", err)
	}
}

func TestMarkGoodMakesActivatedVersionNextRollbackTarget(t *testing.T) {
	store := NewPluginStore(t.TempDir())
	for _, version := range []string{"1.0.0", "2.0.0"} {
		pkg := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"`+version+`","entry":"entry.js"}`)
		if _, err := store.installVerifiedMarket(pkg, "3.0.0", "demo", version, testPluginProvenance(version), nil); err != nil {
			t.Fatal(err)
		}
	}
	list, err := store.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list failed: %+v, %v", list, err)
	}
	active := list[0]
	if err := store.MarkGood(active.ID, active.Version, active.LoadToken, active.Provenance); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.backupPath("demo")); !os.IsNotExist(err) {
		t.Fatalf("successful activation should consume stale backup: %v", err)
	}
	v3 := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"3.0.0","entry":"entry.js"}`)
	if _, err := store.installVerifiedMarket(v3, "3.0.0", "demo", "3.0.0", testPluginProvenance("3.0.0"), nil); err != nil {
		t.Fatal(err)
	}
	backup, err := readManifestAt(store.backupPath("demo"))
	if err != nil || backup.Version != "2.0.0" {
		t.Fatalf("latest known good should be v2, got %+v, err=%v", backup, err)
	}
}

func TestVerifiedInstallCASRejectsChangedCurrentPlugin(t *testing.T) {
	store := NewPluginStore(t.TempDir())
	v1 := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"1.0.0","entry":"entry.js"}`)
	if _, err := store.installVerifiedMarket(v1, "3.0.0", "demo", "1.0.0", testPluginProvenance("1.0.0"), nil); err != nil {
		t.Fatal(err)
	}
	before, _ := store.List()
	v15 := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"1.5.0","entry":"entry.js"}`)
	if _, err := store.installVerifiedMarket(v15, "3.0.0", "demo", "1.5.0", testPluginProvenance("1.5.0"), nil); err != nil {
		t.Fatal(err)
	}
	v2 := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"2.0.0","entry":"entry.js"}`)
	_, err := store.installVerifiedMarket(v2, "3.0.0", "demo", "2.0.0", testPluginProvenance("2.0.0"), &before[0])
	if !errors.Is(err, ErrPluginChanged) {
		t.Fatalf("stale expected install should fail CAS, got %v", err)
	}
	active, _ := store.readManifest("demo")
	if active.Version != "1.5.0" {
		t.Fatalf("CAS failure replaced current plugin: %+v", active)
	}
}

func TestVerifyLoadRequiresEnabledMatchingToken(t *testing.T) {
	store := NewPluginStore(t.TempDir())
	pkg := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"1.0.0","entry":"entry.js"}`)
	if _, err := store.installVerifiedMarket(pkg, "3.0.0", "demo", "1.0.0", testPluginProvenance("1.0.0"), nil); err != nil {
		t.Fatal(err)
	}
	list, _ := store.List()
	if err := store.VerifyLoad("demo", list[0].LoadToken); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnabled("demo", false, false); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(store.VerifyLoad("demo", list[0].LoadToken), ErrPluginChanged) {
		t.Fatal("disabled plugin entry should not be served")
	}
}

func TestUnmarkedLegacyInstallIsLocalAndUntrusted(t *testing.T) {
	dir := t.TempDir()
	s := NewPluginStore(dir)
	writePlugin(t, dir, "demo", "1.0.0")

	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].Local || list[0].Provenance != nil || list[0].Enabled {
		t.Fatalf("unmarked install must not be inferred trusted: %+v", list)
	}
	if !errors.Is(s.SetEnabled("demo", true, false), ErrPluginApprovalRequired) {
		t.Fatal("legacy plugin enabled without explicit approval")
	}
	if err := s.SetEnabled("demo", true, true); err != nil {
		t.Fatal(err)
	}
	list, err = s.List()
	if err != nil || !list[0].Enabled {
		t.Fatalf("explicit approval did not enable legacy plugin: %+v, %v", list, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "demo", "entry.js"), []byte("export function setup(){ throw new Error('changed') }"), 0644); err != nil {
		t.Fatal(err)
	}
	list, err = s.List()
	if err != nil || list[0].Enabled {
		t.Fatalf("approval survived executable payload change: %+v, %v", list, err)
	}
}

func TestVerifiedInstallRejectsDowngrade(t *testing.T) {
	store := NewPluginStore(t.TempDir())
	v2 := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"2.0.0","entry":"entry.js"}`)
	if _, err := store.installVerifiedMarket(v2, "3.0.0", "demo", "2.0.0", testPluginProvenance("2.0.0"), nil); err != nil {
		t.Fatal(err)
	}
	v1 := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"1.0.0","entry":"entry.js"}`)
	if _, err := store.installVerifiedMarket(v1, "3.0.0", "demo", "1.0.0", testPluginProvenance("1.0.0"), nil); !errors.Is(err, ErrPluginDowngrade) {
		t.Fatalf("verified downgrade error = %v, want ErrPluginDowngrade", err)
	}
}

func TestInstallIncompatibleVersion(t *testing.T) {
	s := NewPluginStore(t.TempDir())
	pkg := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"1.0.0","minHostVersion":"2.0.0","entry":"entry.js"}`)
	m, err := s.InstallLocal(pkg, "1.0.0")
	if err != ErrIncompatible {
		t.Fatalf("expected ErrIncompatible, got %v", err)
	}
	if m.MinHostVersion != "2.0.0" {
		t.Fatalf("manifest should be returned for messaging, got %+v", m)
	}
}

func TestInstallRejectsBadId(t *testing.T) {
	s := NewPluginStore(t.TempDir())
	pkg := makeSekplugin(t, `{"id":"../evil","name":"Evil","version":"1.0.0","entry":"entry.js"}`)
	if _, err := s.InstallLocal(pkg, "3.0.0"); err == nil {
		t.Fatal("expected error for path-escaping id")
	}
}

func TestInstallExpectIDMismatch(t *testing.T) {
	dir := t.TempDir()
	s := NewPluginStore(dir)
	// Package advertises id "hello" but the market entry expected "newplug".
	pkg := makeSekplugin(t, `{"id":"hello","name":"Hello","version":"1.0.0","entry":"entry.js"}`)
	_, err := s.installVerifiedMarket(pkg, "3.0.0", "newplug", "1.0.0", testPluginProvenance("1.0.0"), nil)
	if !errors.Is(err, ErrIDMismatch) {
		t.Fatalf("expected ErrIDMismatch, got %v", err)
	}
	// Nothing should have been written for either id.
	if _, e := os.Stat(filepath.Join(dir, "hello")); !os.IsNotExist(e) {
		t.Fatal("mismatched package must not be extracted")
	}
}

func TestInstallExpectVersionMismatch(t *testing.T) {
	s := NewPluginStore(t.TempDir())
	pkg := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"1.0.0","entry":"entry.js"}`)
	_, err := s.installVerifiedMarket(pkg, "3.0.0", "demo", "2.0.0", testPluginProvenance("2.0.0"), nil)
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("expected ErrVersionMismatch, got %v", err)
	}
}
