//go:build windows

package updater

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8
	createNoWindow          = 0x08000000
)

var moveFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func installUpgrade(staged, destination string) (bool, error) {
	helper := destination + ".upgrade.exe"
	_ = os.Remove(helper)
	if err := os.Rename(staged, helper); err != nil {
		return false, err
	}
	command := exec.Command(helper, "__cfdev_replace", destination)
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = nil
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	if err := command.Start(); err != nil {
		return false, err
	}
	if err := command.Process.Release(); err != nil {
		return false, err
	}
	return true, nil
}

func runReplacement(destination string) error {
	if filepath.Base(destination) != "cfdev.exe" {
		return fmt.Errorf("refusing to replace an unexpected executable")
	}
	source, err := os.Executable()
	if err != nil {
		return err
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".cfdev-replace-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o755); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := replaceExecutable(temporaryPath, destination); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting to replace cfdev.exe")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func replaceExecutable(source, target string) error {
	sourcePointer, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	targetPointer, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	result, _, callErr := moveFileEx.Call(
		uintptr(unsafe.Pointer(sourcePointer)),
		uintptr(unsafe.Pointer(targetPointer)),
		uintptr(moveFileReplaceExisting|moveFileWriteThrough),
	)
	if result == 0 {
		return callErr
	}
	return nil
}

func CleanupStaged() {
	executable, err := os.Executable()
	if err == nil {
		_ = os.Remove(executable + ".upgrade.exe")
	}
}
