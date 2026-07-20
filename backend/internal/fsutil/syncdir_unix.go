//go:build !windows

package fsutil

import (
	"errors"
	"os"
	"syscall"
)

// SyncDir makes directory-entry changes durable where the platform supports it.
func SyncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	if errors.Is(syncErr, syscall.EINVAL) || errors.Is(syncErr, syscall.ENOTSUP) {
		syncErr = nil
	}
	return errors.Join(syncErr, dir.Close())
}
