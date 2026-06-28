package service

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// PluginManifest is the metadata every installed plugin carries in its
// manifest.json. id/entry are required; the rest is display/compat metadata.
// Mirrors the frontend PluginManifest type.
type PluginManifest struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Version        string `json:"version"`
	Description    string `json:"description,omitempty"`
	Author         string `json:"author,omitempty"`
	Entry          string `json:"entry,omitempty"`          // defaults to entry.js
	MinHostVersion string `json:"minHostVersion,omitempty"` // semver; host checks compat
	Icon           string `json:"icon,omitempty"`           // lucide icon name
}

// PluginInfo is one entry in the installed-plugins listing: manifest fields plus
// runtime state (enabled).
type PluginInfo struct {
	PluginManifest
	Enabled bool `json:"enabled"`
}

// PluginStore manages installed plugins under a writable dir, one subdir per
// plugin id. Enable-state lives in {dir}/state.json (a map id->enabled) so it
// survives across reinstalls and never mutates the plugin payloads themselves.
type PluginStore struct {
	mu  sync.Mutex // guards the state.json read-modify-write in SetEnabled/Uninstall
	dir string
}

func NewPluginStore(pluginsDir string) *PluginStore {
	return &PluginStore{dir: pluginsDir}
}

func (s *PluginStore) statePath() string {
	return filepath.Join(s.dir, "state.json")
}

// pluginState maps plugin id -> enabled. Absent id defaults to enabled.
func (s *PluginStore) loadState() map[string]bool {
	state := map[string]bool{}
	data, err := os.ReadFile(s.statePath())
	if err != nil {
		return state
	}
	_ = json.Unmarshal(data, &state)
	return state
}

func (s *PluginStore) saveState(state map[string]bool) error {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.statePath(), data, 0644)
}

// readManifest loads and validates a plugin's manifest.json.
func (s *PluginStore) readManifest(id string) (PluginManifest, error) {
	var m PluginManifest
	data, err := os.ReadFile(filepath.Join(s.dir, id, "manifest.json"))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.Entry == "" {
		m.Entry = "entry.js"
	}
	return m, nil
}

// List returns all installed plugins (dirs with a valid manifest.json), sorted
// by id, with enabled state resolved.
func (s *PluginStore) List() ([]PluginInfo, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []PluginInfo{}, nil
		}
		return nil, err
	}
	state := s.loadState()
	out := []PluginInfo{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		m, err := s.readManifest(id)
		if err != nil {
			continue // not a valid plugin dir
		}
		if m.ID == "" {
			m.ID = id
		}
		enabled := true
		if v, ok := state[id]; ok {
			enabled = v
		}
		out = append(out, PluginInfo{
			PluginManifest: m,
			Enabled:        enabled,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// SetEnabled persists a plugin's enabled flag.
func (s *PluginStore) SetEnabled(id string, enabled bool) error {
	if !validPluginID(id) {
		return errors.New("invalid plugin id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.loadState()
	state[id] = enabled
	return s.saveState(state)
}

// Uninstall removes a plugin's directory and its state entry.
func (s *PluginStore) Uninstall(id string) error {
	// Validate id is a single safe segment before any filesystem op: an id like
	// ".." would make filepath.Join(s.dir, id) escape the plugins dir and have
	// os.RemoveAll wipe the parent (app data) directory.
	if !validPluginID(id) {
		return errors.New("invalid plugin id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	target := filepath.Join(s.dir, id)
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	state := s.loadState()
	delete(state, id)
	return s.saveState(state)
}

// PluginDir returns the absolute directory for a plugin id (for file serving).
// An invalid id returns "" so callers don't serve files outside the plugins dir.
func (s *PluginStore) PluginDir(id string) string {
	if !validPluginID(id) {
		return ""
	}
	return filepath.Join(s.dir, id)
}

// ErrIncompatible is returned when a plugin's minHostVersion exceeds hostVersion.
var ErrIncompatible = errors.New("plugin requires a newer host version")

// ErrBadPackage is returned for malformed .sekplugin archives.
var ErrBadPackage = errors.New("invalid plugin package")

// ErrIDMismatch is returned when a package's manifest id differs from the id the
// caller expected (marketplace install).
var ErrIDMismatch = errors.New("plugin id mismatch")

// Install unpacks a .sekplugin archive (a zip of manifest.json + entry.js +
// assets) into the plugins dir. It validates the manifest, the entry file's
// presence, and minHostVersion against hostVersion (when both are set). An
// existing plugin with the same id is replaced (its enable-state preserved).
// NOTE: there is currently no first-party/reserved-id protection — any id can be
// overwritten by an install or removed by Uninstall. Returns the manifest.
//
// expectID, when non-empty, asserts the package's manifest id matches it — used
// by the marketplace so a misconfigured or malicious index can't install a
// different (or pre-existing) plugin than the one advertised. File installs
// pass "" to skip the check.
func (s *PluginStore) Install(archivePath, hostVersion, expectID string) (PluginManifest, error) {
	var zero PluginManifest
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return zero, ErrBadPackage
	}
	defer zr.Close()

	m, err := manifestFromZip(zr)
	if err != nil {
		return zero, err
	}
	if m.ID == "" {
		return zero, errors.New("manifest missing id")
	}
	if !validPluginID(m.ID) {
		return zero, errors.New("invalid plugin id")
	}
	if expectID != "" && m.ID != expectID {
		return m, fmt.Errorf("%w: 包内插件 id (%s) 与市场条目 (%s) 不一致", ErrIDMismatch, m.ID, expectID)
	}
	if m.Entry == "" {
		m.Entry = "entry.js"
	}
	if hostVersion != "" && m.MinHostVersion != "" && versionNewer(m.MinHostVersion, hostVersion) {
		return m, ErrIncompatible
	}
	if !zipHasFile(zr, m.Entry) {
		return zero, errors.New("entry file not found in package: " + m.Entry)
	}

	dst := filepath.Join(s.dir, m.ID)
	if err := os.RemoveAll(dst); err != nil {
		return zero, err
	}
	if err := os.MkdirAll(dst, 0755); err != nil {
		return zero, err
	}
	if err := extractZip(zr, dst); err != nil {
		return zero, err
	}
	return m, nil
}

// validPluginID allows lowercase alphanumerics, dash, underscore (no slashes/dots
// → prevents path escapes via the id).
func validPluginID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func manifestFromZip(zr *zip.ReadCloser) (PluginManifest, error) {
	var m PluginManifest
	for _, f := range zr.File {
		if f.Name == "manifest.json" {
			rc, err := f.Open()
			if err != nil {
				return m, err
			}
			defer rc.Close()
			data, err := io.ReadAll(io.LimitReader(rc, 1<<20))
			if err != nil {
				return m, err
			}
			if err := json.Unmarshal(data, &m); err != nil {
				return m, ErrBadPackage
			}
			return m, nil
		}
	}
	return m, errors.New("manifest.json not found in package root")
}

func zipHasFile(zr *zip.ReadCloser, name string) bool {
	for _, f := range zr.File {
		if f.Name == name {
			return true
		}
	}
	return false
}

// extractZip writes all archive entries under dst, guarding against zip-slip.
func extractZip(zr *zip.ReadCloser, dst string) error {
	dstClean := filepath.Clean(dst)
	// Guard against zip-bombs: cap entry count and total uncompressed bytes (the
	// per-entry LimitReader below already caps any single file at 64 MiB).
	const maxEntries = 4000
	const maxTotalSize = 256 << 20
	if len(zr.File) > maxEntries {
		return errors.New("plugin package has too many entries")
	}
	var written int64
	for _, f := range zr.File {
		target := filepath.Join(dst, filepath.Clean("/"+f.Name))
		if target != dstClean && !strings.HasPrefix(target, dstClean+string(filepath.Separator)) {
			return errors.New("illegal path in package: " + f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			rc.Close()
			return err
		}
		n, err := io.Copy(out, io.LimitReader(rc, 64<<20))
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
		written += n
		if written > maxTotalSize {
			return errors.New("plugin package exceeds the uncompressed size limit")
		}
	}
	return nil
}

// versionNewer reports whether a is a newer semver than b (numeric dot compare,
// pre-release suffixes ignored — sufficient for seed-update decisions).
func versionNewer(a, b string) bool {
	pa := parseVer(a)
	pb := parseVer(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parseVer(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	parts := strings.Split(v, ".")
	for i := 0; i < 3 && i < len(parts); i++ {
		n := 0
		for _, c := range parts[i] {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out
}
