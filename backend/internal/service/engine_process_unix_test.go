//go:build !windows

package service

import (
	"bufio"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestForceKillProcessGroupAfterLeaderExit(t *testing.T) {
	cmd := exec.Command("sh", "-c", `sh -c 'trap "" HUP; exec sleep 30' >/dev/null 2>&1 & echo $!`)
	HideConsoleWindow(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(childPID, syscall.SIGKILL) }()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("leader wait: %v", err)
	}

	proc := &engineProc{cmd: cmd}
	proc.forceKill()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(childPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant %d survived process-group kill after leader exit", childPID)
}
