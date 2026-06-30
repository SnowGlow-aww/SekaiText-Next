//go:build !windows

package ipc

import (
	"os/signal"
	"syscall"
)

// ignoreSIGPIPE makes a write to a broken stdout pipe return EPIPE instead of
// terminating the process via SIGPIPE's default disposition. The stdout pipe is
// the frame channel; if Rust closes its read end while an in-flight response is
// being written, we want EPIPE (handled/logged), not a signal kill.
func ignoreSIGPIPE() {
	signal.Ignore(syscall.SIGPIPE)
}
