//go:build !windows

package fsutil

import (
	"errors"
	"os"
)

func moveFileNoReplace(oldPath, newPath string) error {
	// Creating the destination link is an atomic create-if-absent operation on
	// Unix. Remove the source only after publication; if that fails, roll the new
	// link back so callers never receive a failed move with two visible names.
	if err := os.Link(oldPath, newPath); err != nil {
		return err
	}
	if err := os.Remove(oldPath); err != nil {
		return errors.Join(err, os.Remove(newPath))
	}
	return nil
}
