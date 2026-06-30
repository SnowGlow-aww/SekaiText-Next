//go:build windows

package ipc

// Windows has no SIGPIPE; a write to a broken pipe already returns an error
// rather than raising a signal, so there is nothing to ignore.
func ignoreSIGPIPE() {}
