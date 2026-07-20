//go:build !windows

package service

import "os/exec"

// Unix process groups remain addressable after their leader exits, so the
// existing KillProcessTree fallback is itself the retained cleanup authority.
func newProcessTreeAuthority(*exec.Cmd) (processTreeAuthority, error) {
	return nil, nil
}
