//go:build windows

package service

import (
	"fmt"
	"os/exec"
	"syscall"
)

const (
	createNoWindow         = 0x08000000
	createNewProcessGroup  = 0x00000200
	createBreakawayFromJob = 0x01000000
)

// HideConsoleWindow keeps console-subsystem children (SekaiCoreEngine, cmd.exe)
// from opening a visible console: the backend itself is built with -H windowsgui
// and has no console to inherit, and a user closing that window would kill the
// child mid-job.
func HideConsoleWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow | createNewProcessGroup}
}

// DetachInstallerProcess lets an installer survive application shutdown. The
// Tauri Job Object explicitly permits breakaway for this one child.
func DetachInstallerProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow | createNewProcessGroup | createBreakawayFromJob,
	}
}

// KillProcessTree uses the system taskkill utility as the local engine-level
// fallback. The Tauri parent also owns the backend in a kill-on-close Job Object.
func KillProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	kill := exec.Command("taskkill", "/PID", fmt.Sprint(cmd.Process.Pid), "/T", "/F")
	HideConsoleWindow(kill)
	if err := kill.Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
