//go:build !windows

package process

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func alive(pid int) bool {
	return pid > 0 && syscall.Kill(pid, 0) == nil
}

func configureBackground(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func processMatches(pid int, binary string) bool {
	expected := strings.ToLower(filepath.Base(binary))
	result, err := exec.Command("ps", "-p", fmt.Sprint(pid), "-o", "comm=").Output()
	return err == nil && strings.Contains(strings.ToLower(filepath.Base(strings.TrimSpace(string(result)))), expected)
}

func terminate(pid int, timeout time.Duration, processGroup bool) error {
	target := pid
	if processGroup {
		target = -pid
	}
	if err := syscall.Kill(target, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		return err
	}
	if waitUntilStopped(pid, timeout) {
		return nil
	}
	if err := syscall.Kill(target, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return err
	}
	if !waitUntilStopped(pid, time.Second) {
		return fmt.Errorf("process %d is still running", pid)
	}
	return nil
}
