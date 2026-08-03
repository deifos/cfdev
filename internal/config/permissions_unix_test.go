//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateConfigPermissions(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CFDEV_HOME", filepath.Join(base, "cfdev-home"))
	paths, err := ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Version: 1, Domain: "example.com", TunnelName: "cfdev-permissions", TunnelID: testTunnelID,
		CredentialsFile: filepath.Join(base, "credential.json"), MachineID: "permissions",
		Mappings: []Mapping{{Subdomain: "app", Port: 3000, Protocol: "http"}},
	}
	if err := Save(paths, cfg); err != nil {
		t.Fatal(err)
	}
	if _, _, err := MachineIdentity(paths); err != nil {
		t.Fatal(err)
	}

	assertPermission(t, paths.Home, 0o700)
	assertPermission(t, paths.Config, 0o600)
	assertPermission(t, paths.Ingress, 0o600)
	assertPermission(t, paths.MachineID, 0o600)
}

func assertPermission(t *testing.T, path string, expected os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if actual := info.Mode().Perm(); actual != expected {
		t.Fatalf("%s permissions = %04o, want %04o", path, actual, expected)
	}
}
