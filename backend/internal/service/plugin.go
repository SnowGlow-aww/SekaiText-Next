package service

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"sekaitext/backend/internal/fsutil"
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
	Enabled    bool              `json:"enabled"`
	Local      bool              `json:"local"`
	Provenance *PluginProvenance `json:"provenance,omitempty"`
	LoadToken  string            `json:"loadToken"`
}

// PluginProvenance is written only by the verified-market install path. Its
// presence is positive evidence that the active bytes were authenticated; old
// unmarked installs and file installs are deliberately treated as local.
type PluginProvenance struct {
	Source       string `json:"source"`
	Publisher    string `json:"publisher"`
	KeyID        string `json:"keyId"`
	SHA256       string `json:"sha256"`
	IndexVersion int    `json:"indexVersion"`
	Sequence     uint64 `json:"sequence,omitempty"`
}

const (
	localPluginMarker      = ".sekaitext-local"
	pluginProvenanceMarker = ".sekaitext-provenance.json"
	localApprovalsFile     = "local-approvals.json"
	maxSafeSequence        = uint64(1<<53 - 1)
)

var ErrPluginChanged = errors.New("installed plugin changed")
var ErrMarketReplay = errors.New("plugin market index replay detected")
var ErrPluginApprovalRequired = errors.New("local plugin requires explicit approval")
var ErrPluginDowngrade = errors.New("plugin downgrade rejected")

// PluginStore manages installed plugins under a writable dir, one subdir per
// plugin id. Enable-state lives in {dir}/state.json (a map id->enabled) so it
// survives across reinstalls and never mutates the plugin payloads themselves.
type PluginStore struct {
	mu                   sync.Mutex // guards state, provenance checks, installs, rollback, and activation marks
	dir                  string
	marketStateLoaded    bool
	marketState          pluginMarketState
	writeMarketStateFile func(string, []byte, os.FileMode) error
}

func NewPluginStore(pluginsDir string) *PluginStore {
	return &PluginStore{dir: pluginsDir}
}

func (s *PluginStore) statePath() string {
	return filepath.Join(s.dir, "state.json")
}

func (s *PluginStore) marketStatePath() string {
	return filepath.Join(s.dir, "market-state.json")
}

func (s *PluginStore) localApprovalsPath() string {
	return filepath.Join(s.dir, localApprovalsFile)
}

func (s *PluginStore) backupPath(id string) string {
	return filepath.Join(s.dir, ".backup-"+id)
}

// pluginState maps plugin id -> enabled. A missing file means "no overrides"
// (every plugin defaults to enabled); any other read/parse error is surfaced so
// callers don't silently treat a corrupt state.json as "all enabled". Callers
// must hold s.mu.
func (s *PluginStore) loadState() (map[string]bool, error) {
	state := map[string]bool{}
	data, err := os.ReadFile(s.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return state, nil
}

// saveState atomically replaces state.json: it writes a temp file in the same
// dir then renames it into place, so a concurrent reader never observes a
// truncated/half-written file. Callers must hold s.mu.
func (s *PluginStore) saveState(state map[string]bool) error {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, "state-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, s.statePath()); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// Local approvals live outside plugin-controlled directories and bind a user's
// confirmation to the exact executable load token. Legacy installs have no
// approval, so they are listed disabled until the user confirms or reinstalls
// authenticated bytes from the market.
func (s *PluginStore) loadLocalApprovals() (map[string]string, error) {
	approvals := map[string]string{}
	data, err := os.ReadFile(s.localApprovalsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return approvals, nil
		}
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&approvals); err != nil {
		return nil, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("local plugin approvals contain trailing JSON")
	}
	for id, token := range approvals {
		if !validPluginID(id) || !validSHA256(token) {
			return nil, errors.New("invalid local plugin approval")
		}
	}
	return approvals, nil
}

func (s *PluginStore) saveLocalApprovals(approvals map[string]string) error {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(approvals, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, "local-approvals-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		return err
	}
	return os.Rename(tmpName, s.localApprovalsPath())
}

type pluginMarketState struct {
	RequireV3         bool              `json:"requireV3"`
	HighestSequence   uint64            `json:"highestSequence"`
	SequenceByKey     map[string]uint64 `json:"sequenceByKey"` // retained for existing state files
	KeyID             string            `json:"keyId,omitempty"`
	SnapshotSignature string            `json:"snapshotSignature,omitempty"`
}

func clonePluginMarketState(state pluginMarketState) pluginMarketState {
	cloned := state
	cloned.SequenceByKey = make(map[string]uint64, len(state.SequenceByKey))
	for key, sequence := range state.SequenceByKey {
		cloned.SequenceByKey[key] = sequence
	}
	return cloned
}

// loadMarketStateLocked caches the durable replay floor for this process. Callers
// clone before mutation so a failed pre-commit write cannot advance the cache.
func (s *PluginStore) loadMarketStateLocked() (pluginMarketState, error) {
	if s.marketStateLoaded {
		return clonePluginMarketState(s.marketState), nil
	}
	state := pluginMarketState{SequenceByKey: map[string]uint64{}}
	data, err := os.ReadFile(s.marketStatePath())
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&state); err != nil {
			return pluginMarketState{}, err
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			return pluginMarketState{}, errors.New("plugin market state contains trailing JSON")
		}
		if state.SequenceByKey == nil {
			state.SequenceByKey = map[string]uint64{}
		}
	} else if !os.IsNotExist(err) {
		return pluginMarketState{}, err
	}
	s.marketState = clonePluginMarketState(state)
	s.marketStateLoaded = true
	return state, nil
}

// AcceptMarketIndex persists anti-replay state only after every v3 entry and the
// complete ordered snapshot have passed verification. Equal sequences are
// idempotent only for the same key and snapshot signature. Once v3 has been
// observed, a v2 index can no longer downgrade this installation.
func (s *PluginStore) AcceptMarketIndex(version int, keyID string, sequence uint64, snapshotSignature string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadMarketStateLocked()
	if err != nil {
		return err
	}
	if version == 2 {
		if state.RequireV3 {
			return ErrMarketReplay
		}
		return nil
	}
	if version != 3 || !validSigningKeyID(keyID) || sequence == 0 || sequence > maxSafeSequence ||
		snapshotSignature == "" || sequence < state.HighestSequence {
		return ErrMarketReplay
	}
	if sequence == state.HighestSequence && state.HighestSequence != 0 {
		if state.KeyID != keyID || state.SnapshotSignature != snapshotSignature {
			return ErrMarketReplay
		}
		return nil
	}
	state.RequireV3 = true
	state.HighestSequence = sequence
	state.KeyID = keyID
	state.SnapshotSignature = snapshotSignature
	state.SequenceByKey[keyID] = sequence
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	write := fsutil.WriteFileAtomic
	if s.writeMarketStateFile != nil {
		write = s.writeMarketStateFile
	}
	writeErr := write(s.marketStatePath(), append(encoded, '\n'), 0o644)
	if writeErr == nil || fsutil.IsWriteCommitted(writeErr) {
		// A parent-directory sync failure occurs after the atomic replace. Keep the
		// in-process floor aligned with the visible file even while surfacing the
		// durability warning to the caller.
		s.marketState = clonePluginMarketState(state)
		s.marketStateLoaded = true
	}
	return writeErr
}

// readManifest loads and validates a plugin's manifest.json.
func (s *PluginStore) readManifest(id string) (PluginManifest, error) {
	return readManifestAt(filepath.Join(s.dir, id))
}

func readManifestAt(dir string) (PluginManifest, error) {
	var m PluginManifest
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return m, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return m, errors.New("manifest contains trailing JSON")
	}
	if m.Entry == "" {
		m.Entry = "entry.js"
	}
	if err := validatePluginManifest(m); err != nil {
		return m, err
	}
	return m, nil
}

func readPluginProvenanceAt(dir string) (*PluginProvenance, error) {
	data, err := os.ReadFile(filepath.Join(dir, pluginProvenanceMarker))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var provenance PluginProvenance
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&provenance); err != nil {
		return nil, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("plugin provenance contains trailing JSON")
	}
	if provenance.Source != "verified-market" || provenance.Publisher == "" ||
		!validSigningKeyID(provenance.KeyID) || !validSHA256(provenance.SHA256) ||
		(provenance.IndexVersion != 2 && provenance.IndexVersion != 3) ||
		(provenance.IndexVersion == 3 && (provenance.Sequence == 0 || provenance.Sequence > maxSafeSequence)) {
		return nil, errors.New("invalid plugin provenance")
	}
	return &provenance, nil
}

func pluginLoadTokenAt(dir, id string, manifest PluginManifest, provenance *PluginProvenance) (string, error) {
	p := "local"
	if provenance != nil {
		data, _ := json.Marshal(provenance)
		p = string(data)
	}
	entry, err := os.Open(filepath.Join(dir, filepath.FromSlash(manifest.Entry)))
	if err != nil {
		return "", err
	}
	defer entry.Close()
	entryHash := sha256.New()
	if _, err := io.Copy(entryHash, entry); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(id + "\x00" + manifest.Version + "\x00" + p + "\x00" + hex.EncodeToString(entryHash.Sum(nil))))
	return fmt.Sprintf("%x", digest[:]), nil
}

func (s *PluginStore) pluginInfoLocked(id string, state map[string]bool, approvals map[string]string) (PluginInfo, error) {
	dir := filepath.Join(s.dir, id)
	m, err := readManifestAt(dir)
	if err != nil {
		return PluginInfo{}, err
	}
	m.ID = id
	provenance, err := readPluginProvenanceAt(dir)
	if err != nil {
		// Corrupt or forged provenance is never interpreted as trusted.
		provenance = nil
	}
	loadToken, err := pluginLoadTokenAt(dir, id, m, provenance)
	if err != nil {
		return PluginInfo{}, err
	}
	enabled := true
	if value, ok := state[id]; ok {
		enabled = value
	}
	if provenance == nil && approvals[id] != loadToken {
		enabled = false
	}
	return PluginInfo{
		PluginManifest: m,
		Enabled:        enabled,
		Local:          provenance == nil,
		Provenance:     provenance,
		LoadToken:      loadToken,
	}, nil
}

// List returns all installed plugins (dirs with a valid manifest.json), sorted
// by id, with enabled state resolved.
func (s *PluginStore) List() ([]PluginInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []PluginInfo{}, nil
		}
		return nil, err
	}
	state, err := s.loadState()
	if err != nil {
		return nil, err
	}
	approvals, err := s.loadLocalApprovals()
	if err != nil {
		return nil, err
	}
	out := []PluginInfo{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		if !validPluginID(id) {
			continue // skip scratch dirs (e.g. a crashed install's ".install-*") and non-plugin entries
		}
		info, err := s.pluginInfoLocked(id, state, approvals)
		if err != nil {
			continue // not a valid plugin dir
		}
		// The directory name is the authoritative id: Install always creates
		// {dir}/{manifest.id}, and enable-state is keyed by it. Pin the reported
		// id to the dir name so a manifest whose id disagrees (e.g. via a
		// malformed package) can't desync SetEnabled/Uninstall — which operate on
		// this id — from what List reads.
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// SetEnabled persists a plugin's enabled flag.
func (s *PluginStore) SetEnabled(id string, enabled, approveLocal bool) error {
	if !validPluginID(id) {
		return errors.New("invalid plugin id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadState()
	if err != nil {
		return err
	}
	approvals, err := s.loadLocalApprovals()
	if err != nil {
		return err
	}
	info, err := s.pluginInfoLocked(id, state, approvals)
	if err != nil {
		return err
	}
	if enabled && info.Local && !approveLocal {
		return ErrPluginApprovalRequired
	}
	state[id] = enabled
	// Write the enable state first. If approval persistence then fails, local
	// plugins remain effectively disabled because their token is still absent.
	if err := s.saveState(state); err != nil {
		return err
	}
	if info.Local {
		if enabled {
			approvals[id] = info.LoadToken
		} else {
			delete(approvals, id)
		}
		return s.saveLocalApprovals(approvals)
	}
	return nil
}

// VerifyLoad authorizes serving an executable entry only while the plugin is
// enabled and still has the exact version/provenance-derived token listed to the
// frontend. This closes the fetch-vs-install/disable race at the backend edge.
func (s *PluginStore) VerifyLoad(id, token string) error {
	if !validPluginID(id) || !validSHA256(token) {
		return ErrPluginChanged
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadState()
	if err != nil {
		return err
	}
	approvals, err := s.loadLocalApprovals()
	if err != nil {
		return err
	}
	info, err := s.pluginInfoLocked(id, state, approvals)
	if err != nil {
		return err
	}
	if !info.Enabled || info.LoadToken != token {
		return ErrPluginChanged
	}
	return nil
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
	// Load (and validate) state before removing anything so a corrupt state.json
	// aborts without having already deleted the plugin dir.
	state, err := s.loadState()
	if err != nil {
		return err
	}
	approvals, err := s.loadLocalApprovals()
	if err != nil {
		return err
	}
	target := filepath.Join(s.dir, id)
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	if err := os.RemoveAll(s.backupPath(id)); err != nil {
		return err
	}
	delete(state, id)
	if err := s.saveState(state); err != nil {
		return err
	}
	delete(approvals, id)
	return s.saveLocalApprovals(approvals)
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

// ErrVersionMismatch is returned when a verified market package's manifest
// version differs from the signed market entry.
var ErrVersionMismatch = errors.New("plugin version mismatch")

// ErrNoPluginBackup is returned when rollback is requested without a retained
// last-known-good payload.
var ErrNoPluginBackup = errors.New("plugin backup not found")

// InstallLocal installs a user-selected development package. Local packages are
// intentionally allowed without an official signature, marked local, and left
// disabled until the frontend completes its explicit full-permission warning.
func (s *PluginStore) InstallLocal(archivePath, hostVersion string) (PluginManifest, error) {
	return s.install(archivePath, hostVersion, "", "", true, nil, nil, false)
}

// installVerifiedMarket is deliberately package-private: MarketService calls it
// only after the official entry signature and package digest have verified.
func (s *PluginStore) installVerifiedMarket(
	archivePath, hostVersion, expectedID, expectedVersion string,
	provenance PluginProvenance,
	expectedCurrent *PluginInfo,
) (PluginManifest, error) {
	return s.install(archivePath, hostVersion, expectedID, expectedVersion, false, &provenance, expectedCurrent, expectedCurrent != nil)
}

func (s *PluginStore) installVerifiedMarketCAS(
	archivePath, hostVersion, expectedID, expectedVersion string,
	provenance PluginProvenance,
	expectedCurrent *PluginInfo,
) (PluginManifest, error) {
	return s.install(archivePath, hostVersion, expectedID, expectedVersion, false, &provenance, expectedCurrent, true)
}

// install unpacks a .sekplugin archive (a zip of manifest.json + entry.js +
// assets) into the plugins dir. It validates the manifest, the entry file's
// presence, and minHostVersion against hostVersion (when both are set). An
// existing plugin with the same id is retained as a rollback payload before the
// new directory is activated. Local-file installs are marked local and disabled;
// marketplace installs preserve the existing enable-state.
// NOTE: there is currently no first-party/reserved-id protection — any id can be
// overwritten by an install or removed by Uninstall. Returns the manifest.
//
// expectedID/expectedVersion bind a verified market entry to the manifest that
// is ultimately activated. Local installs do not supply either value.
func (s *PluginStore) install(
	archivePath, hostVersion, expectedID, expectedVersion string,
	isLocal bool,
	provenance *PluginProvenance,
	expectedCurrent *PluginInfo,
	requireUnchanged bool,
) (PluginManifest, error) {
	var zero PluginManifest
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return zero, ErrBadPackage
	}
	defer zr.Close()

	// Hold the lock for the whole install so it can't interleave with a
	// concurrent Uninstall/Install of the same id (which would race RemoveAll
	// against the extraction) or with List observing a half-swapped dir.
	s.mu.Lock()
	defer s.mu.Unlock()

	m, err := manifestFromZip(zr)
	if err != nil {
		return zero, err
	}
	if err := validatePluginManifest(m); err != nil {
		return zero, err
	}
	if expectedID != "" && m.ID != expectedID {
		return m, fmt.Errorf("%w: 包内插件 id (%s) 与市场条目 (%s) 不一致", ErrIDMismatch, m.ID, expectedID)
	}
	if expectedVersion != "" && m.Version != expectedVersion {
		return m, fmt.Errorf("%w: 包内插件版本 (%s) 与市场条目 (%s) 不一致", ErrVersionMismatch, m.Version, expectedVersion)
	}
	if m.Entry == "" {
		m.Entry = "entry.js"
	}
	if hostVersion != "" {
		if _, ok := parseSemver(hostVersion); !ok {
			return m, errors.New("invalid host version")
		}
		if m.MinHostVersion != "" && compareSemverStrings(m.MinHostVersion, hostVersion) > 0 {
			return m, ErrIncompatible
		}
	}
	if !zipHasFile(zr, m.Entry) {
		return zero, errors.New("entry file not found in package: " + m.Entry)
	}

	// Extract into a temp dir first, then swap it into place only after a fully
	// successful extraction. A failure mid-extraction (disk full, permission, a
	// bad/duplicate entry) thus leaves any existing install untouched instead of
	// deleting it and writing a half-finished replacement.
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return zero, err
	}
	tmpDir, err := os.MkdirTemp(s.dir, ".install-*")
	if err != nil {
		return zero, err
	}
	// Cleans up the temp dir on every failure path; a no-op once it has been
	// renamed into place on success.
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0755); err != nil {
		return zero, err
	}
	if err := extractZip(zr, tmpDir); err != nil {
		return zero, err
	}
	marker := filepath.Join(tmpDir, localPluginMarker)
	if err := os.RemoveAll(marker); err != nil {
		return zero, err
	}
	if isLocal {
		if err := os.WriteFile(marker, []byte("local file install\n"), 0644); err != nil {
			return zero, err
		}
	}
	provenancePath := filepath.Join(tmpDir, pluginProvenanceMarker)
	if err := os.RemoveAll(provenancePath); err != nil {
		return zero, err
	}
	if !isLocal {
		if provenance == nil || provenance.Source != "verified-market" ||
			provenance.Publisher == "" || !validSigningKeyID(provenance.KeyID) ||
			!validSHA256(provenance.SHA256) {
			return zero, errors.New("verified market install missing provenance")
		}
		data, err := json.Marshal(provenance)
		if err != nil {
			return zero, err
		}
		if err := os.WriteFile(provenancePath, append(data, '\n'), 0644); err != nil {
			return zero, err
		}
	}

	var state map[string]bool
	if isLocal {
		state, err = s.loadState()
		if err != nil {
			return zero, err
		}
	}

	dst := filepath.Join(s.dir, m.ID)
	backup := s.backupPath(m.ID)
	hadDst, err := pathExists(dst)
	if err != nil {
		return zero, err
	}
	var current PluginInfo
	if hadDst {
		currentState, err := s.loadState()
		if err != nil {
			return zero, err
		}
		approvals, err := s.loadLocalApprovals()
		if err != nil {
			return zero, err
		}
		current, err = s.pluginInfoLocked(m.ID, currentState, approvals)
		if err != nil {
			return zero, err
		}
		if compareSemverStrings(m.Version, current.Version) < 0 {
			return zero, ErrPluginDowngrade
		}
	}
	if requireUnchanged {
		if expectedCurrent == nil && hadDst || expectedCurrent != nil && !hadDst {
			return zero, ErrPluginChanged
		}
		if expectedCurrent != nil && (current.Version != expectedCurrent.Version ||
			current.LoadToken != expectedCurrent.LoadToken ||
			!sameProvenance(current.Provenance, expectedCurrent.Provenance)) {
			return zero, ErrPluginChanged
		}
	}
	oldPath := ""
	if hadDst {
		backupExists, err := pathExists(backup)
		if err != nil {
			return zero, err
		}
		if backupExists {
			// A backup already represents a known-good payload. Keep it and move
			// the current active version to disposable staging during this swap.
			oldPath, err = unusedTempPath(s.dir, ".previous-"+m.ID+"-*")
			if err != nil {
				return zero, err
			}
		} else {
			oldPath = backup
		}
		if err := os.Rename(dst, oldPath); err != nil {
			return zero, err
		}
	}
	restoreOld := func() error {
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
		if hadDst {
			return os.Rename(oldPath, dst)
		}
		return nil
	}
	if err := os.Rename(tmpDir, dst); err != nil {
		if restoreErr := restoreOld(); restoreErr != nil {
			return zero, fmt.Errorf("activate plugin: %v; restore previous payload: %w", err, restoreErr)
		}
		return zero, err
	}
	if isLocal {
		state[m.ID] = false
		if err := s.saveState(state); err != nil {
			if restoreErr := restoreOld(); restoreErr != nil {
				return zero, fmt.Errorf("save disabled state: %v; restore previous payload: %w", err, restoreErr)
			}
			return zero, err
		}
	}
	if hadDst && oldPath != backup {
		_ = os.RemoveAll(oldPath)
	}
	return m, nil
}

func sameProvenance(a, b *PluginProvenance) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// MarkGood records successful runtime activation. The active payload must still
// match the frontend's expected version and provenance. Consuming the old backup
// means the next install retains this just-activated payload as the latest known
// good rollback target.
func (s *PluginStore) MarkGood(id, expectedVersion, expectedLoadToken string, expectedProvenance *PluginProvenance) error {
	if !validPluginID(id) || expectedVersion == "" || !validSHA256(expectedLoadToken) {
		return ErrPluginChanged
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadState()
	if err != nil {
		return err
	}
	approvals, err := s.loadLocalApprovals()
	if err != nil {
		return err
	}
	current, err := s.pluginInfoLocked(id, state, approvals)
	if err != nil {
		return err
	}
	if !current.Enabled || current.Version != expectedVersion ||
		current.LoadToken != expectedLoadToken ||
		!sameProvenance(current.Provenance, expectedProvenance) {
		return ErrPluginChanged
	}
	return os.RemoveAll(s.backupPath(id))
}

// Rollback restores the retained payload for id. It is intentionally explicit:
// the frontend calls it only after the newly activated plugin fails to load.
func (s *PluginStore) Rollback(id string) (PluginManifest, error) {
	var zero PluginManifest
	if !validPluginID(id) {
		return zero, errors.New("invalid plugin id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	backup := s.backupPath(id)
	m, err := readManifestAt(backup)
	if err != nil {
		if os.IsNotExist(err) {
			return zero, ErrNoPluginBackup
		}
		return zero, err
	}
	m.ID = id
	dst := filepath.Join(s.dir, id)
	hadDst, err := pathExists(dst)
	if err != nil {
		return zero, err
	}
	failedPath := ""
	if hadDst {
		failedPath, err = unusedTempPath(s.dir, ".failed-"+id+"-*")
		if err != nil {
			return zero, err
		}
		if err := os.Rename(dst, failedPath); err != nil {
			return zero, err
		}
	}
	if err := os.Rename(backup, dst); err != nil {
		if hadDst {
			if restoreErr := os.Rename(failedPath, dst); restoreErr != nil {
				return zero, fmt.Errorf("rollback plugin: %v; restore active payload: %w", err, restoreErr)
			}
		}
		return zero, err
	}
	if failedPath != "" {
		_ = os.RemoveAll(failedPath)
	}
	return m, nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func unusedTempPath(dir, pattern string) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
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
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&m); err != nil {
				return m, ErrBadPackage
			}
			if decoder.Decode(&struct{}{}) != io.EOF {
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
	// per-entry limit below already caps any single file at 64 MiB).
	const maxEntries = 4000
	const maxTotalSize = 256 << 20
	const maxFileSize = 64 << 20
	if len(zr.File) > maxEntries {
		return errors.New("plugin package has too many entries")
	}
	// Reject packages with duplicate entries (same on-disk path). Zip allows
	// duplicate names, and last-write-wins extraction would otherwise let a second
	// entry overwrite what earlier validation already vetted — e.g. a second
	// manifest.json that desyncs the validated manifest from what lands on disk.
	seen := make(map[string]bool, len(zr.File))
	var written int64
	for _, f := range zr.File {
		target := filepath.Join(dst, filepath.Clean("/"+f.Name))
		if target != dstClean && !strings.HasPrefix(target, dstClean+string(filepath.Separator)) {
			return errors.New("illegal path in package: " + f.Name)
		}
		if seen[target] {
			return errors.New("duplicate entry in package: " + f.Name)
		}
		seen[target] = true
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
		// Read one byte past the cap so an oversized file is rejected outright
		// rather than silently truncated to maxFileSize (which io.LimitReader
		// alone would do, producing a corrupt resource with no error).
		n, err := io.Copy(out, io.LimitReader(rc, maxFileSize+1))
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
		if n > maxFileSize {
			return errors.New("plugin package file exceeds the per-file size limit: " + f.Name)
		}
		written += n
		if written > maxTotalSize {
			return errors.New("plugin package exceeds the uncompressed size limit")
		}
	}
	return nil
}

type semVersion struct {
	major, minor, patch uint64
	prerelease          []string
}

var semverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

func parseSemver(value string) (semVersion, bool) {
	match := semverPattern.FindStringSubmatch(value)
	if match == nil {
		return semVersion{}, false
	}
	parts := make([]uint64, 3)
	for i := range parts {
		parsed, err := strconv.ParseUint(match[i+1], 10, 64)
		if err != nil {
			return semVersion{}, false
		}
		parts[i] = parsed
	}
	var prerelease []string
	if match[4] != "" {
		prerelease = strings.Split(match[4], ".")
		for _, identifier := range prerelease {
			if identifier == "" || (allDigits(identifier) && len(identifier) > 1 && identifier[0] == '0') {
				return semVersion{}, false
			}
		}
	}
	return semVersion{major: parts[0], minor: parts[1], patch: parts[2], prerelease: prerelease}, true
}

func allDigits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return value != ""
}

func compareSemver(a, b semVersion) int {
	left := [...]uint64{a.major, a.minor, a.patch}
	right := [...]uint64{b.major, b.minor, b.patch}
	for i := range left {
		if left[i] < right[i] {
			return -1
		}
		if left[i] > right[i] {
			return 1
		}
	}
	if len(a.prerelease) == 0 || len(b.prerelease) == 0 {
		if len(a.prerelease) == len(b.prerelease) {
			return 0
		}
		if len(a.prerelease) == 0 {
			return 1
		}
		return -1
	}
	for i := 0; i < len(a.prerelease) && i < len(b.prerelease); i++ {
		leftPart, rightPart := a.prerelease[i], b.prerelease[i]
		if leftPart == rightPart {
			continue
		}
		leftNumeric, rightNumeric := allDigits(leftPart), allDigits(rightPart)
		switch {
		case leftNumeric && rightNumeric:
			if len(leftPart) < len(rightPart) || (len(leftPart) == len(rightPart) && leftPart < rightPart) {
				return -1
			}
			return 1
		case leftNumeric:
			return -1
		case rightNumeric:
			return 1
		case leftPart < rightPart:
			return -1
		default:
			return 1
		}
	}
	if len(a.prerelease) < len(b.prerelease) {
		return -1
	}
	if len(a.prerelease) > len(b.prerelease) {
		return 1
	}
	return 0
}

func compareSemverStrings(a, b string) int {
	left, leftOK := parseSemver(a)
	right, rightOK := parseSemver(b)
	if !leftOK || !rightOK {
		return 0
	}
	return compareSemver(left, right)
}

// versionNewer is strict SemVer precedence. Invalid versions are never newer.
func versionNewer(a, b string) bool {
	left, leftOK := parseSemver(a)
	right, rightOK := parseSemver(b)
	return leftOK && rightOK && compareSemver(left, right) > 0
}

func updateVersionAllowed(candidate, current string) bool {
	next, nextOK := parseSemver(candidate)
	installed, installedOK := parseSemver(current)
	if !nextOK || !installedOK || compareSemver(next, installed) <= 0 {
		return false
	}
	// Stable installations never cross implicitly onto a prerelease channel.
	return len(installed.prerelease) > 0 || len(next.prerelease) == 0
}

func validatePluginManifest(manifest PluginManifest) error {
	if !validPluginID(manifest.ID) {
		return errors.New("invalid plugin id")
	}
	if strings.TrimSpace(manifest.Name) == "" || len(manifest.Name) > 200 {
		return errors.New("manifest missing or invalid name")
	}
	if _, ok := parseSemver(manifest.Version); !ok {
		return errors.New("manifest version is not strict semver")
	}
	if manifest.MinHostVersion != "" {
		if _, ok := parseSemver(manifest.MinHostVersion); !ok {
			return errors.New("manifest minHostVersion is not strict semver")
		}
	}
	entry := manifest.Entry
	if entry == "" {
		entry = "entry.js"
	}
	clean := path.Clean("/" + entry)
	if entry == "" || strings.Contains(entry, "\\") || clean == "/" || strings.TrimPrefix(clean, "/") != entry {
		return errors.New("invalid plugin entry path")
	}
	return nil
}
