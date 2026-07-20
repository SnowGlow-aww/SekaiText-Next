package service

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"sekaitext/backend/internal/fsutil"
)

func testSigningKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return publicKey, privateKey
}

func signTestMarketEntry(entry *MarketEntry, privateKey ed25519.PrivateKey) {
	entry.Publisher = OfficialPluginPublisher
	entry.KeyID = "test-2026-01"
	entry.SignatureAlgorithm = pluginSignatureAlgorithm
	entry.PackageSignature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, canonicalMarketEntryPayload(entry)))
}

func signTestMarketEntryV3(entry *MarketEntry, privateKey ed25519.PrivateKey) {
	signTestMarketEntry(entry, privateKey)
	entry.Sequence = 42
	entry.ExpiresAt = time.Now().UTC().Add(time.Hour).Truncate(time.Second).Format(time.RFC3339)
	entry.MetadataSignature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, canonicalMarketMetadataPayload(entry)))
}

func signTestMarketIndexV3(entries []MarketEntry, privateKey ed25519.PrivateKey) MarketIndex {
	index := MarketIndex{
		Version:            3,
		Plugins:            append([]MarketEntry(nil), entries...),
		Publisher:          OfficialPluginPublisher,
		KeyID:              "test-2026-01",
		SignatureAlgorithm: pluginSignatureAlgorithm,
		Sequence:           42,
		ExpiresAt:          time.Now().UTC().Add(time.Hour).Truncate(time.Second).Format(time.RFC3339),
	}
	for i := range index.Plugins {
		entry := &index.Plugins[i]
		entry.Publisher = index.Publisher
		entry.KeyID = index.KeyID
		entry.SignatureAlgorithm = index.SignatureAlgorithm
		entry.Sequence = index.Sequence
		entry.ExpiresAt = index.ExpiresAt
		entry.PackageSignature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, canonicalMarketEntryPayload(entry)))
		entry.MetadataSignature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, canonicalMarketMetadataPayload(entry)))
	}
	index.SnapshotSignature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, canonicalMarketSnapshotPayload(&index)))
	return index
}

func marketWithTestKey(store *PluginStore, publicKey ed25519.PublicKey) *MarketService {
	market := NewMarketService(store)
	market.trustedKeys = map[string]ed25519.PublicKey{"test-2026-01": publicKey}
	market.keyConfigErr = nil
	return market
}

func serveSignedMarket(t *testing.T, packagePath string, entry MarketEntry, privateKey ed25519.PrivateKey) (*httptest.Server, *MarketEntry) {
	t.Helper()
	packageData, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(packageData)
	entry.SHA256 = hex.EncodeToString(digest[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(MarketIndex{Version: 2, Plugins: []MarketEntry{entry}})
		case "/plugin.sekplugin":
			_, _ = w.Write(packageData)
		default:
			http.NotFound(w, r)
		}
	}))
	entry.Download = server.URL + "/plugin.sekplugin"
	signTestMarketEntry(&entry, privateKey)
	return server, &entry
}

func TestCanonicalMarketEntryPayload(t *testing.T) {
	entry := MarketEntry{
		Publisher:          "p",
		KeyID:              "k",
		SignatureAlgorithm: "ed25519",
		ID:                 "demo",
		Version:            "1.0.0",
		Download:           "u",
		SHA256:             strings.Repeat("0", 64),
	}
	want := "SekaiText-Plugin-Signature-V1\n" +
		"publisher:1:p\n" +
		"keyId:1:k\n" +
		"algorithm:7:ed25519\n" +
		"id:4:demo\n" +
		"version:5:1.0.0\n" +
		"download:1:u\n" +
		"sha256:64:" + strings.Repeat("0", 64) + "\n"
	if got := string(canonicalMarketEntryPayload(&entry)); got != want {
		t.Fatalf("canonical payload mismatch\ngot:  %q\nwant: %q", got, want)
	}
}

func TestCanonicalMarketSnapshotPayloadPreservesPluginOrder(t *testing.T) {
	index := MarketIndex{
		Version:            3,
		Publisher:          "p",
		KeyID:              "k",
		SignatureAlgorithm: "ed25519",
		Sequence:           42,
		ExpiresAt:          "2030-01-01T00:00:00Z",
		Plugins: []MarketEntry{
			{ID: "first", MetadataSignature: "m1"},
			{ID: "second", MetadataSignature: "m2"},
		},
	}
	want := "SekaiText-Plugin-Market-Snapshot-V1\n" +
		"publisher:1:p\n" +
		"keyId:1:k\n" +
		"algorithm:7:ed25519\n" +
		"version:1:3\n" +
		"sequence:2:42\n" +
		"expiresAt:20:2030-01-01T00:00:00Z\n" +
		"pluginCount:1:2\n" +
		"pluginId:5:first\n" +
		"metadataSignature:2:m1\n" +
		"pluginId:6:second\n" +
		"metadataSignature:2:m2\n"
	if got := string(canonicalMarketSnapshotPayload(&index)); got != want {
		t.Fatalf("canonical snapshot mismatch\ngot:  %q\nwant: %q", got, want)
	}
}

func TestMarketInstallAcceptsValidOfficialSignature(t *testing.T) {
	publicKey, privateKey := testSigningKey(t)
	packagePath := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"2.0.0","entry":"entry.js"}`)
	server, _ := serveSignedMarket(t, packagePath, MarketEntry{ID: "demo", Name: "Demo", Version: "2.0.0"}, privateKey)
	defer server.Close()

	store := NewPluginStore(t.TempDir())
	manifest, err := marketWithTestKey(store, publicKey).Install(server.URL+"/index.json", "demo", "3.0.0")
	if err != nil {
		t.Fatalf("signed market install failed: %v", err)
	}
	if manifest.ID != "demo" || manifest.Version != "2.0.0" {
		t.Fatalf("unexpected installed manifest: %+v", manifest)
	}
	installed, err := store.List()
	if err != nil || len(installed) != 1 || installed[0].Local || !installed[0].Enabled || installed[0].Provenance == nil {
		t.Fatalf("official install should be enabled and non-local: %+v, err=%v", installed, err)
	}
}

func TestMarketInstallReattestsLegacyPluginAtEqualVersion(t *testing.T) {
	publicKey, privateKey := testSigningKey(t)
	packagePath := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"1.0.0","entry":"entry.js"}`)
	server, _ := serveSignedMarket(t, packagePath, MarketEntry{ID: "demo", Name: "Demo", Version: "1.0.0"}, privateKey)
	defer server.Close()

	dir := t.TempDir()
	writePlugin(t, dir, "demo", "1.0.0")
	store := NewPluginStore(dir)
	before, err := store.List()
	if err != nil || len(before) != 1 || before[0].Enabled || !before[0].Local {
		t.Fatalf("legacy payload should begin quarantined: %+v, err=%v", before, err)
	}
	listings, err := marketWithTestKey(store, publicKey).Listings(server.URL + "/index.json")
	if err != nil || len(listings) != 1 || !listings[0].ReinstallAvailable {
		t.Fatalf("equal-version verified reinstall was not offered: %+v, err=%v", listings, err)
	}
	if _, err := marketWithTestKey(store, publicKey).Install(server.URL+"/index.json", "demo", "3.0.0"); err != nil {
		t.Fatalf("equal-version market re-attestation failed: %v", err)
	}
	after, err := store.List()
	if err != nil || len(after) != 1 || after[0].Local || after[0].Provenance == nil || !after[0].Enabled {
		t.Fatalf("market reinstall did not replace legacy bytes with trusted provenance: %+v, err=%v", after, err)
	}
}

func TestMarketSignatureRejectsTamperedDigest(t *testing.T) {
	publicKey, privateKey := testSigningKey(t)
	entry := MarketEntry{ID: "demo", Version: "1.0.0", Download: "https://example.test/demo.sekplugin", SHA256: strings.Repeat("a", 64)}
	signTestMarketEntry(&entry, privateKey)
	entry.SHA256 = strings.Repeat("b", 64)

	err := marketWithTestKey(NewPluginStore(t.TempDir()), publicKey).verifyEntrySignature(&entry, 2)
	if !errors.Is(err, ErrInvalidPluginSignature) {
		t.Fatalf("tampered digest error = %v, want ErrInvalidPluginSignature", err)
	}
}

func TestMarketSignatureRejectsUnknownKey(t *testing.T) {
	publicKey, privateKey := testSigningKey(t)
	entry := MarketEntry{ID: "demo", Version: "1.0.0", Download: "https://example.test/demo.sekplugin", SHA256: strings.Repeat("a", 64)}
	signTestMarketEntry(&entry, privateKey)
	entry.KeyID = "future-key"

	err := marketWithTestKey(NewPluginStore(t.TempDir()), publicKey).verifyEntrySignature(&entry, 2)
	if !errors.Is(err, ErrUnknownPluginSigningKey) {
		t.Fatalf("unknown key error = %v, want ErrUnknownPluginSigningKey", err)
	}
}

func TestMarketSignatureRejectsInvalidEncoding(t *testing.T) {
	publicKey, privateKey := testSigningKey(t)
	entry := MarketEntry{ID: "demo", Version: "1.0.0", Download: "https://example.test/demo.sekplugin", SHA256: strings.Repeat("a", 64)}
	signTestMarketEntry(&entry, privateKey)
	entry.PackageSignature = "not+canonical="

	err := marketWithTestKey(NewPluginStore(t.TempDir()), publicKey).verifyEntrySignature(&entry, 2)
	if !errors.Is(err, ErrInvalidPluginSignature) {
		t.Fatalf("invalid signature encoding error = %v, want ErrInvalidPluginSignature", err)
	}

	if _, err := parseOfficialPluginPublicKeys(`{"bad":"not-base64"}`); !errors.Is(err, ErrInvalidPluginKeyConfig) {
		t.Fatalf("invalid public key encoding error = %v, want ErrInvalidPluginKeyConfig", err)
	}
}

func TestV3SignatureCoversDisplayedMetadata(t *testing.T) {
	publicKey, privateKey := testSigningKey(t)
	entry := MarketEntry{
		ID: "demo", Name: "Demo", Version: "1.0.0", Description: "visible",
		Author: "author", Icon: "Puzzle", MinHostVersion: "1.0.0",
		Download: "https://example.test/demo-1.0.0.sekplugin", Homepage: "https://example.test/demo",
		SHA256: strings.Repeat("a", 64),
	}
	signTestMarketEntryV3(&entry, privateKey)
	market := marketWithTestKey(NewPluginStore(t.TempDir()), publicKey)
	if err := market.verifyEntrySignature(&entry, 3); err != nil {
		t.Fatalf("valid v3 signature rejected: %v", err)
	}
	entry.Description = "tampered"
	if err := market.verifyEntrySignature(&entry, 3); !errors.Is(err, ErrInvalidPluginSignature) {
		t.Fatalf("tampered metadata error = %v", err)
	}
}

func TestFetchIndexVerifiesSnapshotBeforePersistingReplayState(t *testing.T) {
	publicKey, privateKey := testSigningKey(t)
	index := signTestMarketIndexV3([]MarketEntry{{
		ID: "demo", Name: "Demo", Version: "1.0.0",
		Download: "https://example.test/demo-1.0.0.sekplugin", SHA256: strings.Repeat("a", 64),
	}}, privateKey)

	serve := func(index MarketIndex) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(index)
		}))
	}

	t.Run("valid snapshot persists its identity", func(t *testing.T) {
		store := NewPluginStore(t.TempDir())
		server := serve(index)
		defer server.Close()
		if _, err := marketWithTestKey(store, publicKey).FetchIndex(server.URL); err != nil {
			t.Fatalf("valid snapshot rejected: %v", err)
		}
		data, err := os.ReadFile(store.marketStatePath())
		if err != nil {
			t.Fatal(err)
		}
		var state pluginMarketState
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatal(err)
		}
		if !state.RequireV3 || state.HighestSequence != index.Sequence || state.KeyID != index.KeyID ||
			state.SnapshotSignature != index.SnapshotSignature {
			t.Fatalf("snapshot identity was not persisted: %+v", state)
		}
	})

	t.Run("invalid snapshot leaves no replay state", func(t *testing.T) {
		store := NewPluginStore(t.TempDir())
		tampered := index
		tampered.SnapshotSignature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
		server := serve(tampered)
		defer server.Close()
		_, err := marketWithTestKey(store, publicKey).FetchIndex(server.URL)
		if !errors.Is(err, ErrInvalidPluginSignature) {
			t.Fatalf("invalid snapshot error = %v", err)
		}
		if _, err := os.Stat(store.marketStatePath()); !os.IsNotExist(err) {
			t.Fatalf("invalid snapshot persisted replay state: %v", err)
		}
	})
}

func TestMarketV3RequiresEntryMetadataToMatchSnapshot(t *testing.T) {
	_, privateKey := testSigningKey(t)
	index := signTestMarketIndexV3([]MarketEntry{{
		ID: "demo", Name: "Demo", Version: "1.0.0",
		Download: "https://example.test/demo-1.0.0.sekplugin", SHA256: strings.Repeat("a", 64),
	}}, privateKey)
	index.Plugins[0].Sequence++
	if err := validateMarketIndex(&index); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("snapshot metadata mismatch should be rejected, got %v", err)
	}
}

func TestMarketSequenceRejectsDowngrade(t *testing.T) {
	dir := t.TempDir()
	store := NewPluginStore(dir)
	if err := store.AcceptMarketIndex(2, "", 0, ""); err != nil {
		t.Fatalf("v2 bridge should be accepted before v3: %v", err)
	}
	if err := store.AcceptMarketIndex(3, "key", 10, "snapshot-a"); err != nil {
		t.Fatal(err)
	}
	// A fresh process must recover the same floor from the durable state file.
	store = NewPluginStore(dir)
	if err := store.AcceptMarketIndex(3, "key", 10, "snapshot-a"); err != nil {
		t.Fatalf("identical snapshot retry should be accepted: %v", err)
	}
	if !errors.Is(store.AcceptMarketIndex(3, "key", 10, "snapshot-b"), ErrMarketReplay) {
		t.Fatal("different snapshot at the same sequence should be rejected")
	}
	if !errors.Is(store.AcceptMarketIndex(3, "other-key", 10, "snapshot-a"), ErrMarketReplay) {
		t.Fatal("different key at the same sequence should be rejected")
	}
	if !errors.Is(store.AcceptMarketIndex(3, "key", 9, "snapshot-old"), ErrMarketReplay) {
		t.Fatal("lower v3 sequence should be rejected")
	}
	if err := store.AcceptMarketIndex(3, "other-key", 11, "snapshot-next"); err != nil {
		t.Fatalf("higher sequence signed by a trusted rotated key should be accepted: %v", err)
	}
	if !errors.Is(store.AcceptMarketIndex(2, "", 0, ""), ErrMarketReplay) {
		t.Fatal("v2 downgrade should be rejected after v3")
	}
}

func TestMarketSequencePostCommitFailureKeepsReplayFloorInMemoryAndAfterRestart(t *testing.T) {
	dir := t.TempDir()
	store := NewPluginStore(dir)
	wantErr := errors.New("sync market state directory")
	store.writeMarketStateFile = func(path string, data []byte, mode os.FileMode) error {
		if err := fsutil.WriteFileAtomic(path, data, mode); err != nil {
			return err
		}
		return &fsutil.PostCommitError{Err: wantErr}
	}
	err := store.AcceptMarketIndex(3, "key", 10, "snapshot-a")
	if !errors.Is(err, wantErr) || !fsutil.IsWriteCommitted(err) {
		t.Fatalf("post-commit error = %v", err)
	}
	if !errors.Is(store.AcceptMarketIndex(3, "key", 9, "snapshot-old"), ErrMarketReplay) {
		t.Fatal("in-memory replay floor moved backwards after post-commit error")
	}

	restarted := NewPluginStore(dir)
	if !errors.Is(restarted.AcceptMarketIndex(3, "key", 9, "snapshot-old"), ErrMarketReplay) {
		t.Fatal("restart did not recover the committed replay floor")
	}
}

func TestMarketSequencePreCommitFailureDoesNotAdvanceReplayFloor(t *testing.T) {
	store := NewPluginStore(t.TempDir())
	wantErr := errors.New("disk full before replace")
	store.writeMarketStateFile = func(string, []byte, os.FileMode) error { return wantErr }
	if err := store.AcceptMarketIndex(3, "key", 10, "snapshot-a"); !errors.Is(err, wantErr) {
		t.Fatalf("pre-commit error = %v", err)
	}
	store.writeMarketStateFile = nil
	if err := store.AcceptMarketIndex(3, "key", 9, "snapshot-old"); err != nil {
		t.Fatalf("pre-commit failure advanced replay floor: %v", err)
	}
}

func TestLegacyMarketV2HasBuiltInSunset(t *testing.T) {
	cutoff, err := time.Parse(time.RFC3339, LegacyMarketV2Cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if !legacyMarketV2Allowed(cutoff.Add(-time.Second)) {
		t.Fatal("v2 should remain available immediately before the transition cutoff")
	}
	if legacyMarketV2Allowed(cutoff) || legacyMarketV2Allowed(cutoff.Add(time.Second)) {
		t.Fatal("v2 should fail closed at and after the transition cutoff")
	}
}

func TestFetchIndexRejectsUnknownSchemaFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":2,"plugins":[],"unexpected":true}`))
	}))
	defer server.Close()
	_, err := NewMarketService(NewPluginStore(t.TempDir())).FetchIndex(server.URL)
	if err == nil || !strings.Contains(err.Error(), "invalid market index JSON") {
		t.Fatalf("unknown schema field should be rejected, got %v", err)
	}
}

func TestMarketSchemaRequiresHTTPSHomepage(t *testing.T) {
	index := MarketIndex{Version: 2, Plugins: []MarketEntry{{
		ID: "demo", Name: "Demo", Version: "1.0.0",
		Download: "https://example.test/demo-1.0.0.sekplugin",
		SHA256:   strings.Repeat("a", 64), Homepage: "http://example.test/demo",
		Publisher: OfficialPluginPublisher, KeyID: "key", SignatureAlgorithm: pluginSignatureAlgorithm,
		PackageSignature: base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)),
	}}}
	if err := validateMarketIndex(&index); err == nil || !strings.Contains(err.Error(), "homepage must use HTTPS") {
		t.Fatalf("HTTP homepage should fail runtime schema validation, got %v", err)
	}
}

func TestMarketSchemaDisplayLimitsUseUTF8Bytes(t *testing.T) {
	entry := MarketEntry{
		ID: "demo", Name: strings.Repeat("界", 66) + "aa", Version: "1.0.0",
		Download: "https://example.test/demo-1.0.0.sekplugin", SHA256: strings.Repeat("a", 64),
		Publisher: OfficialPluginPublisher, KeyID: "key", SignatureAlgorithm: pluginSignatureAlgorithm,
		PackageSignature: base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)),
	}
	index := MarketIndex{Version: 2, Plugins: []MarketEntry{entry}}
	if err := validateMarketIndex(&index); err != nil {
		t.Fatalf("200-byte non-ASCII name should be accepted: %v", err)
	}
	index.Plugins[0].Name = strings.Repeat("界", 67)
	if err := validateMarketIndex(&index); err == nil || !strings.Contains(err.Error(), "display metadata") {
		t.Fatalf("201-byte non-ASCII name should be rejected, got %v", err)
	}
}

func TestAutoUpdateInstallsValidSignedPackage(t *testing.T) {
	publicKey, privateKey := testSigningKey(t)
	packagePath := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"2.0.0","entry":"entry.js"}`)
	server, _ := serveSignedMarket(t, packagePath, MarketEntry{ID: "demo", Name: "Demo", Version: "2.0.0"}, privateKey)
	defer server.Close()

	dir := t.TempDir()
	store := NewPluginStore(dir)
	v1 := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"1.0.0","entry":"entry.js"}`)
	if _, err := store.installVerifiedMarket(v1, "3.0.0", "demo", "1.0.0", testPluginProvenance("1.0.0"), nil); err != nil {
		t.Fatal(err)
	}
	summary, err := marketWithTestKey(store, publicKey).AutoUpdate(server.URL+"/index.json", "3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Updated) != 1 || len(summary.Failed) != 0 {
		t.Fatalf("unexpected signed auto-update summary: %+v", summary)
	}
	manifest, err := store.readManifest("demo")
	if err != nil || manifest.Version != "2.0.0" {
		t.Fatalf("signed auto-update did not activate v2: %+v, err=%v", manifest, err)
	}
}

func TestAutoUpdateSkipsLocalDevelopmentPackage(t *testing.T) {
	publicKey, privateKey := testSigningKey(t)
	v1 := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"1.0.0","entry":"entry.js"}`)
	v2 := makeSekplugin(t, `{"id":"demo","name":"Demo","version":"2.0.0","entry":"entry.js"}`)
	server, _ := serveSignedMarket(t, v2, MarketEntry{ID: "demo", Name: "Demo", Version: "2.0.0"}, privateKey)
	defer server.Close()

	store := NewPluginStore(t.TempDir())
	if _, err := store.InstallLocal(v1, "3.0.0"); err != nil {
		t.Fatal(err)
	}
	summary, err := marketWithTestKey(store, publicKey).AutoUpdate(server.URL+"/index.json", "3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Updated) != 0 || len(summary.Failed) != 0 {
		t.Fatalf("local package should not participate in auto-update: %+v", summary)
	}
	manifest, err := store.readManifest("demo")
	if err != nil || manifest.Version != "1.0.0" {
		t.Fatalf("local package was replaced: %+v, err=%v", manifest, err)
	}
}
