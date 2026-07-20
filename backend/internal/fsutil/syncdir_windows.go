//go:build windows

package fsutil

import (
	"fmt"
	"os"
)

// SyncDir validates the directory on Windows. Go cannot open directories for
// FlushFileBuffers there, so the durability operation itself is unsupported.
func SyncDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", path)
	}
	return nil
}
