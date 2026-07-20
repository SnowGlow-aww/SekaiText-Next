package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAegisubSyncPathIsDocumentScoped(t *testing.T) {
	dir := t.TempDir()
	assA := filepath.Join(dir, "a.ass")
	assB := filepath.Join(dir, "b.ass")
	pathA := AegisubSyncPath(assA, "doc-a")
	pathB := AegisubSyncPath(assB, "doc-b")
	if pathA == pathB {
		t.Fatal("two documents in one directory share a sidecar")
	}
	if filepath.Base(pathA) != "_sekaitext.doc-a.sekaisync.txt" || filepath.Base(pathB) != "_sekaitext.doc-b.sekaisync.txt" {
		t.Fatalf("unexpected sidecar paths: %s / %s", pathA, pathB)
	}
	if got := AegisubSyncPath(assA, ""); got != assA+".sekaisync.txt" {
		t.Fatalf("legacy sidecar compatibility changed: %s", got)
	}
}

func TestDocumentScopedPayloadsInOneDirectoryDoNotCross(t *testing.T) {
	dir := t.TempDir()
	expectedHash := strings.Repeat("a", 64)
	replacementHash := strings.Repeat("b", 64)
	pathA := AegisubSyncPath(filepath.Join(dir, "same.ass"), "doc-a")
	pathB := AegisubSyncPath(filepath.Join(dir, "same.ass"), "doc-b")
	payloadA, err := FormatAegisubSyncPayload(AegisubSyncPayload{
		DocumentID: "doc-a", ExpectedRevision: 3, ExpectedContentHash: expectedHash,
		ReplacementRevision: 4, ReplacementContentHash: replacementHash,
		ExpectedLines: []string{"Dialogue: old-doc-a"}, ReplacementLines: []string{"Dialogue: new-doc-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	payloadB, err := FormatAegisubSyncPayload(AegisubSyncPayload{
		DocumentID: "doc-b", ExpectedRevision: 7, ExpectedContentHash: expectedHash,
		ReplacementRevision: 8, ReplacementContentHash: replacementHash,
		ExpectedLines: []string{"Dialogue: old-doc-b"}, ReplacementLines: []string{"Dialogue: new-doc-b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(pathA, []byte(payloadA), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(pathB, []byte(payloadB), 0644); err != nil {
		t.Fatal(err)
	}
	gotA, err := os.ReadFile(pathA)
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := os.ReadFile(pathB)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(gotA), "doc-b") || strings.Contains(string(gotB), "doc-a") {
		t.Fatalf("document payloads crossed:\nA=%s\nB=%s", gotA, gotB)
	}
	for _, want := range []string{
		"; SekaiText sync v3", "; document-id: doc-a", "; expected-revision: 3",
		"; expected-content-hash: " + expectedHash, "; replacement-revision: 4",
		"; begin-expected", "Dialogue: old-doc-a", "; begin-replacement", "Dialogue: new-doc-a",
	} {
		if !strings.Contains(string(gotA), want) {
			t.Fatalf("payload A missing %q: %s", want, gotA)
		}
	}
}

func TestSyncPayloadRejectsLegacyWithoutPreconditions(t *testing.T) {
	_, err := FormatAegisubSyncPayload(AegisubSyncPayload{
		ExpectedLines: []string{"Dialogue: legacy"}, ReplacementLines: []string{"Dialogue: legacy"},
	})
	if err == nil {
		t.Fatal("identity-less payload was accepted")
	}
}

func TestAegisubMacroSelectsCurrentDocumentAndNeverAppendsUnknownGroups(t *testing.T) {
	for _, forbidden := range []string{"newest_t", "_sekaitext.sekaisync.txt", "subs.append", "find_unique_legacy_sync_file"} {
		if strings.Contains(aegisubSyncScript, forbidden) {
			t.Fatalf("macro still contains unsafe selection/append behavior %q", forbidden)
		}
	}
	for _, required := range []string{
		`current_document(subs)`,
		`"_sekaitext." .. doc .. ".sekaisync.txt"`,
		`meta[META_REVISION]`,
		`meta[META_HASH]`,
		`same_event(expected[i], subs[idxs[i]])`,
		`os.remove(sync_file)`,
		`; SekaiText sync consumed`,
	} {
		if !strings.Contains(aegisubSyncScript, required) {
			t.Fatalf("macro missing identity guard %q", required)
		}
	}
}

func TestAegisubMacroRefreshesIndexesForInterleavedSizeChangingGroups(t *testing.T) {
	for _, required := range []string{
		`local function current_group_indexes(subs, tag)`,
		`local idxs = current_group_indexes(subs, replacement.tag)`,
		`for _, replacement in ipairs(replacements) do`,
		`local _, updated_meta_indices = metadata(subs)`,
	} {
		if !strings.Contains(aegisubSyncScript, required) {
			t.Fatalf("macro missing reordered-group safety step %q", required)
		}
	}
	for _, forbidden := range []string{`last_index = idxs[#idxs]`, `replacement.idxs`} {
		if strings.Contains(aegisubSyncScript, forbidden) {
			t.Fatalf("macro still retains stale group indexes via %q", forbidden)
		}
	}

	type line struct{ tag, text string }
	lines := []line{
		{tag: "st:doc:1", text: "a1"},
		{tag: "st:doc:2", text: "b1"},
		{tag: "st:doc:1", text: "a2"},
		{tag: "st:doc:2", text: "b2"},
		{tag: "st:doc:1", text: "a3"},
		{text: "unrelated"},
	}
	replace := func(tag string, replacements []line) {
		var indexes []int
		for i := range lines {
			if lines[i].tag == tag {
				indexes = append(indexes, i)
			}
		}
		n := len(indexes)
		if len(replacements) < n {
			n = len(replacements)
		}
		for i := 0; i < n; i++ {
			lines[indexes[i]] = replacements[i]
		}
		if len(replacements) > len(indexes) {
			at := indexes[len(indexes)-1] + 1
			lines = append(lines[:at], append(replacements[len(indexes):], lines[at:]...)...)
		} else {
			for i := len(indexes) - 1; i >= len(replacements); i-- {
				at := indexes[i]
				lines = append(lines[:at], lines[at+1:]...)
			}
		}
	}

	// The first replacement shrinks rows 1,3,5. The second must discover that
	// its former rows 2,4 are now rows 2,3 before it grows to three rows.
	replace("st:doc:1", []line{{tag: "st:doc:1", text: "A"}})
	replace("st:doc:2", []line{
		{tag: "st:doc:2", text: "B1"},
		{tag: "st:doc:2", text: "B2"},
		{tag: "st:doc:2", text: "B3"},
	})
	got := make([]string, len(lines))
	for i := range lines {
		got[i] = lines[i].text
	}
	want := []string{"A", "B1", "B2", "B3", "unrelated"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("interleaved replacement = %v, want %v", got, want)
	}
}

func TestAegisubSyncMetadataRoundTripAndReplacement(t *testing.T) {
	content := "[Script Info]\nTitle: Test\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n"
	first := AegisubSyncMetadata{DocumentID: "doc-a", Revision: 3, ContentHash: strings.Repeat("a", 64)}
	withMetadata, err := EmbedAegisubSyncMetadata(content, first)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseAegisubSyncMetadata(withMetadata)
	if err != nil || got != first {
		t.Fatalf("metadata round trip = %#v, %v", got, err)
	}

	second := AegisubSyncMetadata{DocumentID: "doc-a", Revision: 4, ContentHash: strings.Repeat("b", 64)}
	replaced, err := EmbedAegisubSyncMetadata(withMetadata, second)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(replaced, aegisubMetaRevision+":") != 1 || strings.Count(replaced, aegisubMetaHash+":") != 1 {
		t.Fatalf("metadata was duplicated:\n%s", replaced)
	}
	got, err = ParseAegisubSyncMetadata(replaced)
	if err != nil || got != second {
		t.Fatalf("replacement metadata = %#v, %v", got, err)
	}
}
