//go:build windows

package process

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
	processQueryLimited   = 0x1000
	stillActive           = 259
)

func configureBackground(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | createNoWindow}
}

func processMatches(pid int, binary string) bool {
	expected := strings.TrimSuffix(strings.ToLower(filepath.Base(binary)), ".exe")
	result, _ := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").CombinedOutput()
	return strings.Contains(strings.ToLower(string(result)), expected)
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := syscall.OpenProcess(processQueryLimited, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)
	var exitCode uint32
	if err := syscall.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false
	}
	return exitCode == stillActive
}

func terminate(pid int, timeout time.Duration, _ bool) error {
	_ = exec.Command("taskkill", "/PID", fmt.Sprint(pid), "/T").Run()
	if waitUntilStopped(pid, timeout) {
		return nil
	}
	if err := exec.Command("taskkill", "/PID", fmt.Sprint(pid), "/T", "/F").Run(); err != nil {
		return err
	}
	if !waitUntilStopped(pid, time.Second) {
		return fmt.Errorf("process %d is still running", pid)
	}
	return nil
}
