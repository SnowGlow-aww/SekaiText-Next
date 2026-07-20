//go:build !windows

package service

import (
	"errors"
	"os/exec"
	"syscall"
)

// HideConsoleWindow is a no-op on Unix, but this shared process setup also puts
// each engine and its ffmpeg descendants in a dedicated process group.
func HideConsoleWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// DetachInstallerProcess places the launcher outside the sidecar process group.
func DetachInstallerProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// KillProcessTree kills the engine's process group, including ffmpeg children.
func KillProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return cmd.Process.Kill()
	}
	if err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
