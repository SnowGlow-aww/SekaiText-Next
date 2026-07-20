package fsutil

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWriteFileAtomicFailureKeepsOldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("injected write failure")
	err := WriteFileAtomicFunc(path, 0o644, func(w io.Writer) error {
		if _, err := w.Write([]byte("partial-new")); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("destination changed after failure: %q", got)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".state.json.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files leaked: %v", matches)
	}
}

func TestWriteFileAtomicConcurrentWritersNeverTear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "translation.txt")
	payloads := make([][]byte, 12)
	for i := range payloads {
		payloads[i] = bytes.Repeat([]byte{byte('a' + i)}, 64*1024)
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(payloads))
	for _, payload := range payloads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := WriteFileAtomic(path, payload, 0o644); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range payloads {
		if bytes.Equal(got, payload) {
			return
		}
	}
	t.Fatalf("final file is a torn write (%d bytes)", len(got))
}

func TestWriteFileAtomicMarksPostRenameSyncFailureCommitted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("injected directory sync failure")
	err := writeFileAtomicFunc(path, 0o644, func(w io.Writer) error {
		_, err := w.Write([]byte("new"))
		return err
	}, func(string) error { return wantErr })
	if !errors.Is(err, wantErr) || !IsWriteCommitted(err) {
		t.Fatalf("error = %v, want committed %v", err, wantErr)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != "new" {
		t.Fatalf("destination was not committed before sync error: %q, %v", got, readErr)
	}
}

func TestWriteFileNoReplaceAtomicRefusesDestinationCreatedBeforeCommit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	tmpData := []byte("new")
	err := writeFileAtomicWithCommit(path, 0o644, func(w io.Writer) error {
		_, err := w.Write(tmpData)
		return err
	}, SyncDir, func(tmp, dst string) error {
		if err := os.WriteFile(dst, []byte("raced-in"), 0o644); err != nil {
			t.Fatal(err)
		}
		return moveFileNoReplace(tmp, dst)
	})
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("error = %v, want os.ErrExist", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != "raced-in" {
		t.Fatalf("raced-in destination changed: %q, %v", got, readErr)
	}
}

func TestSyncDirRejectsMissingPath(t *testing.T) {
	if err := SyncDir(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("SyncDir ignored a real open failure")
	}
}

func TestSyncDirAcceptsDirectory(t *testing.T) {
	if err := SyncDir(t.TempDir()); err != nil {
		t.Fatalf("SyncDir: %v", err)
	}
}
