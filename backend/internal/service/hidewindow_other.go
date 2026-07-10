//go:build !windows

package service

import "os/exec"

// HideConsoleWindow is Windows-only; no-op elsewhere.
func HideConsoleWindow(cmd *exec.Cmd) {}
