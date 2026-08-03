package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deifos/cfdev/internal/failure"
)

const testTunnelID = "123e4567-e89b-42d3-a456-426614174000"

func TestSaveLoadAndBuildIngress(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CFDEV_HOME", home)
	paths, err := ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	credentials := filepath.Join(home, "cloudflared", testTunnelID+".json")
	cfg := &Config{
		Version: 1, Domain: "Example.COM", TunnelName: "cfdev-laptop", TunnelID: testTunnelID,
		CredentialsFile: credentials, MachineID: "laptop",
		Mappings: []Mapping{
			{Subdomain: "web", Port: 3000, Protocol: "http", CreatedAt: time.Now().UTC()},
			{Subdomain: "api", Port: 8080, Protocol: "http", CreatedAt: time.Now().UTC()},
		},
	}
	if err := Save(paths, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Domain != "example.com" || len(loaded.Mappings) != 2 {
		t.Fatalf("unexpected config: %#v", loaded)
	}
	ingress, err := os.ReadFile(paths.Ingress)
	if err != nil {
		t.Fatal(err)
	}
	text := string(ingress)
	if strings.Index(text, "api.example.com") > strings.Index(text, "web.example.com") {
		t.Fatalf("ingress mappings should be deterministic:\n%s", text)
	}
	if !strings.HasSuffix(text, "  - service: http_status:404\n") {
		t.Fatalf("missing catch-all rule:\n%s", text)
	}
	if !strings.Contains(text, filepath.ToSlash(credentials)) {
		t.Fatalf("credentials path missing from ingress:\n%s", text)
	}
}

func TestSaveAtomicallyReplacesExistingConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CFDEV_HOME", home)
	paths, _ := ResolvePaths()
	cfg := &Config{
		Version: 1, Domain: "example.com", TunnelName: "cfdev-test", TunnelID: testTunnelID,
		CredentialsFile: filepath.Join(home, "credential.json"), MachineID: "test", Mappings: []Mapping{},
	}
	if err := Save(paths, cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Domain = "example.net"
	if err := Save(paths, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Domain != "example.net" {
		t.Fatalf("domain = %q, want example.net", loaded.Domain)
	}
}

func TestSaveRestoresConfigWhenIngressWriteFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CFDEV_HOME", home)
	paths, _ := ResolvePaths()
	cfg := &Config{
		Version: 1, Domain: "example.com", TunnelName: "cfdev-test", TunnelID: testTunnelID,
		CredentialsFile: filepath.Join(home, "credential.json"), MachineID: "test", Mappings: []Mapping{},
	}
	if err := Save(paths, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.Ingress); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(paths.Ingress, 0o700); err != nil {
		t.Fatal(err)
	}

	cfg.Domain = "example.net"
	if err := Save(paths, cfg); err == nil {
		t.Fatal("expected ingress write to fail")
	}
	loaded, err := Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Domain != "example.com" {
		t.Fatalf("domain = %q, want rolled-back example.com", loaded.Domain)
	}
}

func TestValidationRejectsDuplicateMappings(t *testing.T) {
	cfg := &Config{
		Version: 1, Domain: "example.com", TunnelName: "cfdev-test", TunnelID: testTunnelID,
		CredentialsFile: filepath.Join(t.TempDir(), "credential.json"), MachineID: "test",
		Mappings: []Mapping{{Subdomain: "web", Port: 3000}, {Subdomain: "WEB", Port: 4000}},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected duplicate mapping error")
	}
}

func TestSuggestSubdomain(t *testing.T) {
	got := SuggestSubdomain(filepath.Join("projects", "QTable-Frontend"))
	if got != "qtable" {
		t.Fatalf("SuggestSubdomain = %q, want qtable", got)
	}
}

func TestNormalizeProjectNameAcceptsShortNameAndConfiguredHostname(t *testing.T) {
	tests := map[string]string{
		"screenslick":                 "screenslick",
		"ScreenSlick":                 "screenslick",
		"screenslick.example.com":     "screenslick",
		"ScreenSlick.Example.COM.":    "screenslick",
		"screenslick.example.com.   ": "screenslick",
	}
	for input, expected := range tests {
		actual, err := NormalizeProjectName(input, "example.com")
		if err != nil || actual != expected {
			t.Fatalf("NormalizeProjectName(%q) = %q, %v; want %q", input, actual, err, expected)
		}
	}
}

func TestNormalizeProjectNameExplainsWrongDomain(t *testing.T) {
	_, err := NormalizeProjectName("screenslick.other.com", "example.com")
	if err == nil {
		t.Fatal("expected a domain mismatch error")
	}
	typed := failure.As(err)
	if typed.Code != "INVALID_SUBDOMAIN" || !strings.Contains(typed.Hint, "cfdev list") {
		t.Fatalf("unexpected error: %#v", typed)
	}
}

func TestMachineIdentityPersistsBeforeSetup(t *testing.T) {
	t.Setenv("CFDEV_HOME", t.TempDir())
	paths, _ := ResolvePaths()
	firstID, firstName, err := MachineIdentity(paths)
	if err != nil {
		t.Fatal(err)
	}
	secondID, secondName, err := MachineIdentity(paths)
	if err != nil {
		t.Fatal(err)
	}
	if firstID != secondID || firstName != secondName {
		t.Fatalf("machine identity changed: %q/%q then %q/%q", firstID, firstName, secondID, secondName)
	}
	if !strings.HasPrefix(firstName, "cfdev-") {
		t.Fatalf("tunnel name = %q", firstName)
	}
}
