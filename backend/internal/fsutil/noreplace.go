package fsutil

// MoveFileNoReplace moves a regular file while atomically refusing to replace
// an existing destination. Both paths must be on the same filesystem.
func MoveFileNoReplace(oldPath, newPath string) error {
	return moveFileNoReplace(oldPath, newPath)
}
