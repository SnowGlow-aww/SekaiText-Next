//go:build windows

package service

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

// HideConsoleWindow keeps console-subsystem children (SekaiCoreEngine, cmd.exe)
// from opening a visible console: the backend itself is built with -H windowsgui
// and has no console to inherit, and a user closing that window would kill the
// child mid-job.
func HideConsoleWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
