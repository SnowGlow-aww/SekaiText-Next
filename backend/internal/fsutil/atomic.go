package fsutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data through a unique sibling temporary file and only
// replaces path after the new contents have been written, synced, and closed.
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) error {
	return WriteFileAtomicFunc(path, perm, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// PostCommitError reports a durability failure after the destination name was
// already atomically replaced. Callers that manage data referenced by the file
// must not roll that data back as though publication never happened.
type PostCommitError struct {
	Err error
}

func (e *PostCommitError) Error() string { return e.Err.Error() }
func (e *PostCommitError) Unwrap() error { return e.Err }

// IsWriteCommitted reports whether an atomic write reached the rename/replace
// commit point before returning an error.
func IsWriteCommitted(err error) bool {
	var committed *PostCommitError
	return errors.As(err, &committed)
}

// WriteFileAtomicFunc is the streaming form of WriteFileAtomic. A failure before
// replaceFile leaves an existing destination unchanged.
func WriteFileAtomicFunc(path string, perm fs.FileMode, write func(io.Writer) error) (err error) {
	return writeFileAtomicFunc(path, perm, write, SyncDir)
}

func writeFileAtomicFunc(path string, perm fs.FileMode, write func(io.Writer) error, syncDir func(string) error) (err error) {
	return writeFileAtomicWithCommit(path, perm, write, syncDir, replaceFile)
}

// WriteFileNoReplaceAtomic writes path atomically only when it does not exist.
// The final publication is a filesystem-level create-if-absent operation, so a
// destination created while the temporary file is being written is preserved.
func WriteFileNoReplaceAtomic(path string, data []byte, perm fs.FileMode) error {
	return WriteFileNoReplaceAtomicFunc(path, perm, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// WriteFileNoReplaceAtomicFunc is the streaming form of
// WriteFileNoReplaceAtomic.
func WriteFileNoReplaceAtomicFunc(path string, perm fs.FileMode, write func(io.Writer) error) error {
	return writeFileAtomicWithCommit(path, perm, write, SyncDir, moveFileNoReplace)
}

func writeFileAtomicWithCommit(
	path string,
	perm fs.FileMode,
	write func(io.Writer) error,
	syncDir func(string) error,
	commit func(string, string) error,
) (err error) {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, f.Close())
		}
		if removeErr := os.Remove(tmp); removeErr != nil && !os.IsNotExist(removeErr) {
			err = errors.Join(err, removeErr)
		}
	}()

	if err := f.Chmod(perm); err != nil {
		return err
	}
	if err := write(f); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err := commit(tmp, path); err != nil {
		return err
	}

	// Persist the directory entry as well as the file contents. SyncDir only
	// suppresses errors that mean directory fsync is unsupported; real open,
	// sync, and close failures are returned to the caller.
	if err := syncDir(dir); err != nil {
		return &PostCommitError{Err: err}
	}
	return nil
}

// CopyFileAtomic copies a regular file without exposing a truncated or partial
// destination. Cancellation is checked while streaming the source.
func CopyFileAtomic(ctx context.Context, src, dst string, perm fs.FileMode) error {
	return copyFileAtomic(ctx, src, dst, perm, WriteFileAtomicFunc)
}

// CopyFileNoReplaceAtomic copies src to dst without ever replacing dst. The
// destination is published only after the complete temporary copy is durable.
func CopyFileNoReplaceAtomic(ctx context.Context, src, dst string, perm fs.FileMode) error {
	return copyFileAtomic(ctx, src, dst, perm, WriteFileNoReplaceAtomicFunc)
}

func copyFileAtomic(
	ctx context.Context,
	src, dst string,
	perm fs.FileMode,
	writeAtomic func(string, fs.FileMode, func(io.Writer) error) error,
) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = in.Close()
		}
	}()

	err = writeAtomic(dst, perm, func(w io.Writer) error {
		_, copyErr := io.Copy(w, &contextReader{ctx: ctx, r: in})
		closeErr := in.Close()
		closed = true
		return errors.Join(copyErr, closeErr)
	})
	if err != nil {
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
