//go:build !windows

package process

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deifos/cfdev/internal/cloudflared"
	"github.com/deifos/cfdev/internal/config"
)

func TestPrivateProcessStatePermissions(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CFDEV_HOME", filepath.Join(base, "cfdev-home"))
	paths, err := config.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	manager := Manager{Paths: paths, Client: &cloudflared.Client{Binary: "/usr/bin/cloudflared"}}
	if err := manager.writeState(State{PID: 123, Binary: manager.Client.Binary, StartedAt: time.Now().UTC(), Mode: "background"}); err != nil {
		t.Fatal(err)
	}
	logFile, err := manager.openLog()
	if err != nil {
		t.Fatal(err)
	}
	if err := logFile.Close(); err != nil {
		t.Fatal(err)
	}

	assertPrivateMode(t, paths.Home, 0o700)
	assertPrivateMode(t, paths.Process, 0o600)
	assertPrivateMode(t, paths.Log, 0o600)
}

func assertPrivateMode(t *testing.T, path string, expected os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if actual := info.Mode().Perm(); actual != expected {
		t.Fatalf("%s permissions = %04o, want %04o", path, actual, expected)
	}
}
