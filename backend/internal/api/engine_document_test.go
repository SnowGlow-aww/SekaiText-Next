package api

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sekaitext/backend/internal/service"
)

func taggedASS(tag, text string) string {
	return `[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:00.00,0:00:01.00,Line1,,0,0,0,` + tag + `,` + text + "\n"
}

func TestLegacySyncCompatibilityRequiresOneDocumentInDirectory(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.ass")
	b := filepath.Join(dir, "b.ass")
	if err := os.WriteFile(a, []byte(taggedASS("st:1", "A")), 0644); err != nil {
		t.Fatal(err)
	}
	if !legacySyncFileIsUnique(a) {
		t.Fatal("single legacy document should be the safe compatibility case")
	}
	if err := os.WriteFile(b, []byte(taggedASS("st:1", "B")), 0644); err != nil {
		t.Fatal(err)
	}
	if legacySyncFileIsUnique(a) || legacySyncFileIsUnique(b) {
		t.Fatal("two legacy documents with the same line ID must both be rejected")
	}
}

func TestLegacyExportUpgradeRequiresCurrentUniqueBinding(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.ass")
	content := []byte(taggedASS("st:1", "legacy"))
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	hash := contentSHA256(content)
	if err := validateExportDestination(path, path, hash, "doc-a"); err != nil {
		t.Fatalf("safe legacy re-export was rejected: %v", err)
	}
	if err := validateExportDestination(path, "", "", "doc-a"); err == nil {
		t.Fatal("unbound legacy output was accepted")
	}
	other := filepath.Join(dir, "other.ass")
	if err := os.WriteFile(other, []byte(taggedASS("st:2", "other")), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateExportDestination(path, path, hash, "doc-a"); err == nil {
		t.Fatal("ambiguous legacy output was accepted")
	}
}

func TestExportDestinationRejectsForeignDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "same.ass")
	content := []byte(taggedASS("st:doc-a:1", "A"))
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	hash := contentSHA256(content)
	if err := validateExportDestination(path, "", "", "doc-b"); err == nil {
		t.Fatal("unowned tagged output was silently overwritten")
	}
	if err := validateExportDestination(path, path, hash, "doc-b"); err == nil {
		t.Fatal("owned path with a mismatched document ID was accepted")
	}
	if err := validateExportDestination(path, path, hash, "doc-a"); err != nil {
		t.Fatalf("same document re-export rejected: %v", err)
	}
	if err := os.WriteFile(path, []byte(taggedASS("st:doc-a:1", "Aegisub edit")), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateExportDestination(path, path, hash, "doc-a"); err == nil {
		t.Fatal("external edit was silently overwritten")
	}
}

func TestTimingOutputConflictIsExplicit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "same.ass")
	conflict := timingOutputConflictIn([]service.EngineTaskSnapshot{{
		TaskID: "task-a", ExportAssPath: path,
	}}, "task-b", path)
	if conflict == "" || !strings.Contains(conflict, "task-a") || !strings.Contains(conflict, path) {
		t.Fatalf("missing explicit output conflict: %q", conflict)
	}
	if conflict := timingOutputConflictIn([]service.EngineTaskSnapshot{{
		TaskID: "task-a", ExportAssPath: path,
	}}, "task-a", path); conflict != "" {
		t.Fatalf("same task should be allowed to re-export: %q", conflict)
	}
}

func TestContentHashDetectsSameSizeReplacement(t *testing.T) {
	a := []byte(strings.Repeat("a", 1024))
	b := []byte(strings.Repeat("b", 1024))
	if len(a) != len(b) {
		t.Fatal("test setup requires equal sizes")
	}
	if contentSHA256(a) == contentSHA256(b) {
		t.Fatal("same-size content replacement was not detected")
	}
}

func TestPublishTimingASSVersionsInsteadOfOverwriting(t *testing.T) {
	dir := t.TempDir()
	preferred := filepath.Join(dir, "event.ass")
	if err := os.WriteFile(preferred, []byte("external edit"), 0644); err != nil {
		t.Fatal(err)
	}
	actual, err := publishTimingASS(preferred, "doc-a", 4, []byte("new export"), false)
	if err != nil {
		t.Fatal(err)
	}
	if sameOutputPath(actual, preferred) {
		t.Fatalf("occupied path was overwritten instead of versioned: %s", actual)
	}
	old, err := os.ReadFile(preferred)
	if err != nil || string(old) != "external edit" {
		t.Fatalf("existing ASS changed: %q, %v", old, err)
	}
	got, err := os.ReadFile(actual)
	if err != nil || string(got) != "new export" {
		t.Fatalf("versioned ASS = %q, %v", got, err)
	}
	if err := writeFileNoReplaceAtomic(actual, []byte("overwrite"), 0644); !errors.Is(err, os.ErrExist) {
		t.Fatalf("exclusive publication error = %v, want os.ErrExist", err)
	}
}

func TestASSPublicationPropagatesDirectorySyncFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event.ass")
	wantErr := errors.New("directory sync failed")
	err := writeFileNoReplaceAtomicWithSync(path, []byte("subtitle"), 0o644, func(string) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != "subtitle" {
		t.Fatalf("published ASS should remain after post-publication sync error: %q, %v", got, readErr)
	}
}

func TestPartialTimingPullDoesNotAdvanceBaseline(t *testing.T) {
	job := &service.EngineTimingJob{
		SyncRevision: 5, ExportHash: strings.Repeat("a", 64),
		ExportRevision: 3, ExportSyncHash: strings.Repeat("b", 64),
	}
	metadata := service.AegisubSyncMetadata{
		DocumentID: "doc-a", Revision: 4, ContentHash: strings.Repeat("c", 64),
	}
	revision, advanced := commitTimingPull(job, nil, os.ErrNotExist, strings.Repeat("d", 64), job.ExportHash,
		metadata, false, true)
	if advanced {
		t.Fatal("partial pull advanced the whole-file baseline")
	}
	if job.ExportHash != strings.Repeat("a", 64) || job.ExportRevision != 3 || job.ExportSyncHash != strings.Repeat("b", 64) {
		t.Fatalf("partial pull changed baseline: %#v", job)
	}
	if revision != 6 {
		t.Fatalf("applied engine changes did not advance document revision: %d", revision)
	}

	_, advanced = commitTimingPull(job, nil, os.ErrNotExist, strings.Repeat("d", 64), job.ExportHash,
		metadata, true, false)
	if !advanced || job.ExportHash != strings.Repeat("d", 64) || job.ExportRevision != 4 || job.ExportSyncHash != strings.Repeat("c", 64) {
		t.Fatalf("complete pull did not commit baseline: %#v", job)
	}
}

func TestConflictedTimingPullPreservesDirtyLineAndBaselineForRetry(t *testing.T) {
	job := &service.EngineTimingJob{
		DirtyLines:     map[int]bool{2: true},
		SyncRevision:   7,
		ExportHash:     strings.Repeat("a", 64),
		ExportRevision: 5,
		ExportSyncHash: strings.Repeat("b", 64),
	}
	metadata := service.AegisubSyncMetadata{
		DocumentID: "doc-a", Revision: 6, ContentHash: strings.Repeat("c", 64),
	}
	_, advanced := commitTimingPull(job, nil, os.ErrNotExist, strings.Repeat("d", 64), job.ExportHash,
		metadata, false, false)
	if advanced || job.ExportHash != strings.Repeat("a", 64) || job.ExportRevision != 5 {
		t.Fatalf("conflicted pull advanced baseline: %#v", job)
	}
	if !job.DirtyLines[2] {
		t.Fatal("conflicted dirty line was cleared, preventing a safe push/retry")
	}
}

func TestTimingPullConflictsWithEngineLineNewerThanASSUntilRetry(t *testing.T) {
	dirty := map[int]bool{1: true}
	revisions := map[int]uint64{1: 8}
	if !timingPullLineConflicts(dirty, revisions, 1, 7) {
		t.Fatal("dirty line was not reported as a pull conflict")
	}
	delete(dirty, 1) // sidecar publication clears DirtyLines before Aegisub applies it
	if !timingPullLineConflicts(dirty, revisions, 1, 7) {
		t.Fatal("newer pushed engine edit was exposed to stale ASS overwrite")
	}
	if timingPullLineConflicts(dirty, revisions, 1, 8) {
		t.Fatal("line stayed conflicted after ASS reached its engine revision")
	}
}

func TestNewerLineEditRemainsDirtyAfterSerializedExportCommit(t *testing.T) {
	job := &service.EngineTimingJob{DirtyLines: map[int]bool{1: true}}
	job.DocumentMu.Lock()
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(started)
		job.DocumentMu.Lock()
		markDirtyLine(job, 1)
		job.DocumentMu.Unlock()
		close(done)
	}()
	<-started
	select {
	case <-done:
		t.Fatal("line mutation did not serialize behind export")
	case <-time.After(20 * time.Millisecond):
	}
	job.Mu.Lock()
	job.DirtyLines = map[int]bool{}
	job.Mu.Unlock()
	job.DocumentMu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("line mutation remained blocked")
	}
	job.Mu.Lock()
	dirty := job.DirtyLines[1]
	job.Mu.Unlock()
	if !dirty {
		t.Fatal("newer line edit was cleared by older export commit")
	}
}
