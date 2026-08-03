package app

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deifos/cfdev/internal/cli"
	"github.com/deifos/cfdev/internal/cloudflare"
	"github.com/deifos/cfdev/internal/cloudflared"
	"github.com/deifos/cfdev/internal/config"
	processmanager "github.com/deifos/cfdev/internal/process"
	"github.com/deifos/cfdev/internal/ui"
)

func TestMain(m *testing.M) {
	if os.Getenv("CFDEV_TEST_START_BACKGROUND_FIXTURE") == "1" {
		if err := os.Setenv("CFDEV_TEST_START_BACKGROUND_FIXTURE", ""); err != nil {
			os.Exit(2)
		}
		if err := os.Setenv("CFDEV_TEST_CLOUDFLARED_HELPER", "1"); err != nil {
			os.Exit(2)
		}
		if err := os.Setenv("CFDEV_TEST_CLOUDFLARED_HOLD_RUN", "1"); err != nil {
			os.Exit(2)
		}
		if err := os.Setenv("CFDEV_TEST_CLOUDFLARED_FILL_LOG", "1"); err != nil {
			os.Exit(2)
		}
		paths, err := config.ResolvePaths()
		if err != nil {
			os.Exit(2)
		}
		cfg, err := config.Load(paths)
		if err != nil {
			os.Exit(2)
		}
		executable, err := os.Executable()
		if err != nil {
			os.Exit(2)
		}
		manager := processmanager.Manager{Paths: paths, Client: &cloudflared.Client{Binary: executable}}
		status, _, err := manager.StartBackground(cfg)
		if err != nil {
			os.Exit(2)
		}
		_, _ = fmt.Fprintln(os.Stdout, status.PID)
		os.Exit(0)
	}
	if os.Getenv("CFDEV_TEST_CLOUDFLARED_HELPER") == "1" {
		isLogin, isList, isRun := false, false, false
		for _, argument := range os.Args[1:] {
			isLogin = isLogin || argument == "login"
			isList = isList || argument == "list"
			isRun = isRun || argument == "run"
		}
		encoded, err := json.Marshal(os.Args[1:])
		if err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(os.Getenv("CFDEV_TEST_CLOUDFLARED_LOG"), encoded, 0o600); err != nil {
			os.Exit(2)
		}
		if isLogin {
			if source := os.Getenv("CFDEV_TEST_LOGIN_CERT_SOURCE"); source != "" {
				contents, readErr := os.ReadFile(source)
				if readErr != nil {
					os.Exit(2)
				}
				target := filepath.Join(os.Getenv("HOME"), ".cloudflared", "cert.pem")
				if mkdirErr := os.MkdirAll(filepath.Dir(target), 0o700); mkdirErr != nil {
					os.Exit(2)
				}
				if writeErr := os.WriteFile(target, contents, 0o600); writeErr != nil {
					os.Exit(2)
				}
			}
		}
		if output := os.Getenv("CFDEV_TEST_CLOUDFLARED_STDOUT"); isList && output != "" {
			_, _ = os.Stdout.WriteString(output)
		}
		if failedArgument := os.Getenv("CFDEV_TEST_CLOUDFLARED_FAIL_ARG"); failedArgument != "" && containsArgument(os.Args[1:], failedArgument) {
			_, _ = os.Stderr.WriteString("forced cloudflared failure\n")
			os.Exit(1)
		}
		if isRun && os.Getenv("CFDEV_TEST_CLOUDFLARED_FILL_LOG") == "1" {
			_, _ = os.Stdout.WriteString(strings.Repeat("x", (2<<20)+1))
		}
		if isRun && os.Getenv("CFDEV_TEST_CLOUDFLARED_HOLD_RUN") == "1" {
			for {
				time.Sleep(time.Minute)
			}
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestEmptyDashboardJSON(t *testing.T) {
	t.Setenv("CFDEV_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	application := New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"--json"}); code != 0 {
		t.Fatalf("exit code = %d, stderr=%s", code, stderr.String())
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Initialized bool `json:"initialized"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK || envelope.Data.Initialized {
		t.Fatalf("unexpected envelope: %s", stdout.String())
	}
}

func TestUnknownCommandJSONHasStableError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	application := New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"wat", "--json"}); code != 3 {
		t.Fatalf("exit code = %d", code)
	}
	var envelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OK || envelope.Error.Code != "INVALID_USAGE" {
		t.Fatalf("unexpected envelope: %s", stdout.String())
	}
}

func TestHostnameWithoutActionSuggestsShortRemoveCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	application := New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"screenslick.example.com", "--json"}); code != 3 {
		t.Fatalf("exit code = %d", code)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
			Hint string `json:"hint"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "INVALID_USAGE" || !strings.Contains(envelope.Error.Hint, "cfdev remove screenslick") {
		t.Fatalf("unexpected envelope: %s", stdout.String())
	}
}

func TestSetupAliasHasDedicatedHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	application := New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"setup", "--help"}); code != 0 {
		t.Fatalf("exit code = %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "cfdev setup [domain]") {
		t.Fatalf("unexpected help: %s", stdout.String())
	}
}

func TestSetupExplicitDomainRequiresMatchingBrowserAuthorization(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CFDEV_HOME", home)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFDEV_CLOUDFLARED", executable)
	t.Setenv("CFDEV_TEST_CLOUDFLARED_HELPER", "1")
	t.Setenv("CFDEV_TEST_CLOUDFLARED_LOG", filepath.Join(home, "cloudflared-args.json"))
	certPath := filepath.Join(home, "cert.pem")
	writeTestOriginCert(t, certPath)
	t.Setenv("CFDEV_ORIGIN_CERT", certPath)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"success":true,"result":{"name":"old.example"}}`))
	}))
	defer server.Close()
	t.Setenv("CFDEV_API_URL", server.URL)

	var stdout, stderr bytes.Buffer
	application := New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"setup", "new.example", "--json"}); code != 2 {
		t.Fatalf("exit code = %d, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"code":"AUTH_REQUIRED"`) || !strings.Contains(stdout.String(), `"interactive_command":"cfdev setup new.example"`) {
		t.Fatalf("unexpected setup handoff: %s", stdout.String())
	}
}

func TestSetupRollsBackAuthorizationWhenTunnelPreparationFails(t *testing.T) {
	userHome := t.TempDir()
	cfdevHome := filepath.Join(userHome, "cfdev")
	setTestUserHome(t, userHome)
	t.Setenv("CFDEV_HOME", cfdevHome)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFDEV_CLOUDFLARED", executable)
	t.Setenv("CFDEV_TEST_CLOUDFLARED_HELPER", "1")
	t.Setenv("CFDEV_TEST_CLOUDFLARED_LOG", filepath.Join(userHome, "cloudflared-args.json"))
	t.Setenv("CFDEV_TEST_CLOUDFLARED_FAIL_ARG", "list")

	cloudflaredHome := filepath.Join(userHome, ".cloudflared")
	if err := os.MkdirAll(cloudflaredHome, 0o700); err != nil {
		t.Fatal(err)
	}
	activeCertPath := filepath.Join(cloudflaredHome, "cert.pem")
	writeOriginCert(t, activeCertPath, "old-zone", "account-test")
	newCertSource := filepath.Join(userHome, "new-cert.pem")
	writeOriginCert(t, newCertSource, "new-zone", "account-test")
	t.Setenv("CFDEV_TEST_LOGIN_CERT_SOURCE", newCertSource)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		domain := map[string]string{"/zones/old-zone": "old.example", "/zones/new-zone": "new.example"}[request.URL.Path]
		if domain == "" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprintf(writer, `{"success":true,"result":{"name":%q}}`, domain)
	}))
	defer server.Close()
	t.Setenv("CFDEV_API_URL", server.URL)

	var stdout, stderr bytes.Buffer
	application := New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"setup", "new.example"}); code == 0 {
		t.Fatalf("setup unexpectedly succeeded: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	active, err := cloudflare.ReadOriginCert(activeCertPath)
	if err != nil {
		t.Fatal(err)
	}
	if active.ZoneID != "old-zone" {
		t.Fatalf("active authorization zone = %q, want rolled-back old-zone", active.ZoneID)
	}
	savedTarget, err := cloudflare.ReadOriginCert(filepath.Join(cloudflaredHome, "cfdev-cert-new.example.pem"))
	if err != nil {
		t.Fatalf("new authorization was not preserved during rollback: %v", err)
	}
	if savedTarget.ZoneID != "new-zone" {
		t.Fatalf("saved target authorization zone = %q, want new-zone", savedTarget.ZoneID)
	}
	paths, _ := config.ResolvePaths()
	if _, err := os.Stat(paths.Config); !os.IsNotExist(err) {
		t.Fatalf("failed setup persisted config: %v", err)
	}
}

func TestSetupJSONDomainDiscoveryContractAndExplicitFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CFDEV_HOME", home)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFDEV_CLOUDFLARED", executable)
	t.Setenv("CFDEV_TEST_CLOUDFLARED_HELPER", "1")
	t.Setenv("CFDEV_TEST_CLOUDFLARED_LOG", filepath.Join(home, "cloudflared-args.json"))
	t.Setenv("CFDEV_TEST_CLOUDFLARED_STDOUT", `[{"id":"123e4567-e89b-42d3-a456-426614174000","name":"cfdev-test-a1b2c3"}]`)

	certPath := filepath.Join(home, "cert.pem")
	writeTestOriginCert(t, certPath)
	t.Setenv("CFDEV_ORIGIN_CERT", certPath)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = writer.Write([]byte(`{"success":false,"errors":[{"message":"temporarily unavailable"}]}`))
	}))
	defer server.Close()
	t.Setenv("CFDEV_API_URL", server.URL)

	var stdout, stderr bytes.Buffer
	application := New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"setup", "--json"}); code != 2 {
		t.Fatalf("discovery failure exit code = %d, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"code":"DOMAIN_REQUIRED"`) || !strings.Contains(stdout.String(), `"retry_command":"cfdev setup example.com --yes --json"`) {
		t.Fatalf("missing DOMAIN_REQUIRED retry contract: %s", stdout.String())
	}

	paths, _ := config.ResolvePaths()
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.MachineID, []byte("test-a1b2c3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	credentialsPath := filepath.Join(home, "123e4567-e89b-42d3-a456-426614174000.json")
	if err := os.WriteFile(credentialsPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	application = New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"setup", "example.com", "--json"}); code != 0 {
		t.Fatalf("explicit fallback exit code = %d, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			DomainValidated bool   `json:"domain_validated"`
			Domain          string `json:"domain"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK || envelope.Data.Domain != "example.com" || envelope.Data.DomainValidated {
		t.Fatalf("unexpected explicit fallback result: %s", stdout.String())
	}
}

func TestAuthorizeDomainFastPathUsesSavedAccountID(t *testing.T) {
	userHome := t.TempDir()
	setTestUserHome(t, userHome)
	t.Setenv("CFDEV_ORIGIN_CERT", "")
	t.Setenv("TUNNEL_ORIGIN_CERT", "")
	cloudflaredHome := filepath.Join(userHome, ".cloudflared")
	if err := os.MkdirAll(cloudflaredHome, 0o700); err != nil {
		t.Fatal(err)
	}
	activePath := filepath.Join(cloudflaredHome, "cert.pem")
	writeOriginCert(t, activePath, "new-zone", "account-test")
	writeOriginCert(t, filepath.Join(cloudflaredHome, "cfdev-cert-old.example.pem"), "old-zone", "account-test")

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path != "/zones/new-zone" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = writer.Write([]byte(`{"success":true,"result":{"name":"new.example"}}`))
	}))
	defer server.Close()
	t.Setenv("CFDEV_API_URL", server.URL)

	view := ui.New(io.Discard, io.Discard, cli.Options{})
	progress := view.Progress("validating")
	defer progress.Stop()
	application := New(bytes.NewBuffer(nil), io.Discard, io.Discard, t.TempDir())
	authorization, err := application.authorizeDomain(
		context.Background(), &cloudflared.Client{Binary: "unused"}, "old.example", "new.example",
		cli.Invocation{}, view, progress, true, "cfdev domain new.example",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !authorization.sameAccount {
		t.Fatal("same Cloudflare account was not recognized from the saved certificate")
	}
	writeOriginCert(t, filepath.Join(cloudflaredHome, "cfdev-cert-old.example.pem"), "old-zone", "other-account")
	authorization, err = application.authorizeDomain(
		context.Background(), &cloudflared.Client{Binary: "unused"}, "old.example", "new.example",
		cli.Invocation{}, view, progress, true, "cfdev domain new.example",
	)
	if err != nil {
		t.Fatal(err)
	}
	if authorization.sameAccount {
		t.Fatal("different Cloudflare accounts were treated as the same account")
	}
}

func TestDomainShowsCurrentAndRefusesToAbandonMappings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CFDEV_HOME", home)
	paths, _ := config.ResolvePaths()
	cfg := &config.Config{
		Version: 1, Domain: "example.com", TunnelName: "cfdev-test-a1b2c3",
		TunnelID:        "123e4567-e89b-42d3-a456-426614174000",
		CredentialsFile: filepath.Join(home, "credential.json"), MachineID: "test-a1b2c3",
		Mappings: []config.Mapping{{Subdomain: "demo", Port: 3000, Protocol: "http"}},
	}
	if err := config.Save(paths, cfg); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	application := New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"domain", "--json"}); code != 0 {
		t.Fatalf("show exit code = %d, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"domain":"example.com"`) {
		t.Fatalf("unexpected current domain: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	application = New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"domain", "other.example", "--json"}); code != 5 {
		t.Fatalf("switch exit code = %d, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"code":"DOMAIN_HAS_MAPPINGS"`) {
		t.Fatalf("unexpected switch error: %s", stdout.String())
	}
	saved, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Domain != "example.com" || len(saved.Mappings) != 1 {
		t.Fatalf("domain switch mutated config: %#v", saved)
	}
}

func TestDomainSwitchValidatesZoneAndReusesMachineTunnel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CFDEV_HOME", home)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(home, "cloudflared-args.json")
	t.Setenv("CFDEV_CLOUDFLARED", executable)
	t.Setenv("CFDEV_TEST_CLOUDFLARED_HELPER", "1")
	t.Setenv("CFDEV_TEST_CLOUDFLARED_LOG", logPath)
	t.Setenv("CFDEV_TEST_CLOUDFLARED_STDOUT", `[{"id":"123e4567-e89b-42d3-a456-426614174000","name":"cfdev-test-a1b2c3"}]`)

	certPath := filepath.Join(home, "cert.pem")
	writeTestOriginCert(t, certPath)
	t.Setenv("CFDEV_ORIGIN_CERT", certPath)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path != "/zones/zone-test" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = writer.Write([]byte(`{"success":true,"result":{"name":"new.example"}}`))
	}))
	defer server.Close()
	t.Setenv("CFDEV_API_URL", server.URL)

	credentialsPath := filepath.Join(home, "123e4567-e89b-42d3-a456-426614174000.json")
	if err := os.WriteFile(credentialsPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths, _ := config.ResolvePaths()
	cfg := &config.Config{
		Version: 1, Domain: "old.example", TunnelName: "cfdev-test-a1b2c3",
		TunnelID:        "123e4567-e89b-42d3-a456-426614174000",
		CredentialsFile: credentialsPath, MachineID: "test-a1b2c3", Mappings: []config.Mapping{},
	}
	if err := config.Save(paths, cfg); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	application := New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"domain", "new.example", "--json"}); code != 0 {
		t.Fatalf("exit code = %d, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	saved, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Domain != "new.example" || saved.TunnelID != cfg.TunnelID {
		t.Fatalf("unexpected switched config: %#v", saved)
	}
	var args []string
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, &args); err != nil {
		t.Fatal(err)
	}
	if !containsArgument(args, "list") || !containsArgument(args, cfg.TunnelName) {
		t.Fatalf("unexpected cloudflared args: %v", args)
	}
}

func TestDomainSwitchRotatesUserAuthorizationAndCanSwitchBack(t *testing.T) {
	userHome := t.TempDir()
	cfdevHome := filepath.Join(userHome, "cfdev")
	setTestUserHome(t, userHome)
	t.Setenv("CFDEV_HOME", cfdevHome)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(userHome, "cloudflared-args.json")
	t.Setenv("CFDEV_CLOUDFLARED", executable)
	t.Setenv("CFDEV_TEST_CLOUDFLARED_HELPER", "1")
	t.Setenv("CFDEV_TEST_CLOUDFLARED_LOG", logPath)
	t.Setenv("CFDEV_TEST_CLOUDFLARED_STDOUT", `[{"id":"123e4567-e89b-42d3-a456-426614174000","name":"cfdev-test-a1b2c3"}]`)

	cloudflaredHome := filepath.Join(userHome, ".cloudflared")
	if err := os.MkdirAll(cloudflaredHome, 0o700); err != nil {
		t.Fatal(err)
	}
	activeCertPath := filepath.Join(cloudflaredHome, "cert.pem")
	writeOriginCert(t, activeCertPath, "old-zone", "account-test")
	newCertSource := filepath.Join(userHome, "new-cert.pem")
	writeOriginCert(t, newCertSource, "new-zone", "account-test")
	t.Setenv("CFDEV_TEST_LOGIN_CERT_SOURCE", newCertSource)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		domain := ""
		switch request.URL.Path {
		case "/zones/old-zone":
			domain = "old.example"
		case "/zones/new-zone":
			domain = "new.example"
		default:
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprintf(writer, `{"success":true,"result":{"name":%q}}`, domain)
	}))
	defer server.Close()
	t.Setenv("CFDEV_API_URL", server.URL)

	credentialsPath := filepath.Join(cloudflaredHome, "123e4567-e89b-42d3-a456-426614174000.json")
	if err := os.WriteFile(credentialsPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths, _ := config.ResolvePaths()
	cfg := &config.Config{
		Version: 1, Domain: "old.example", TunnelName: "cfdev-test-a1b2c3",
		TunnelID:        "123e4567-e89b-42d3-a456-426614174000",
		CredentialsFile: credentialsPath, MachineID: "test-a1b2c3", Mappings: []config.Mapping{},
	}
	if err := config.Save(paths, cfg); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	application := New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"domain", "new.example"}); code != 0 {
		t.Fatalf("switch exit code = %d, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	active, err := cloudflare.ReadOriginCert(activeCertPath)
	if err != nil {
		t.Fatal(err)
	}
	if active.ZoneID != "new-zone" {
		t.Fatalf("active zone = %s, want new-zone", active.ZoneID)
	}
	if _, err := os.Stat(filepath.Join(cloudflaredHome, "cfdev-cert-old.example.pem")); err != nil {
		t.Fatalf("old authorization was not preserved: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	application = New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"domain", "old.example"}); code != 0 {
		t.Fatalf("switch-back exit code = %d, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	active, err = cloudflare.ReadOriginCert(activeCertPath)
	if err != nil {
		t.Fatal(err)
	}
	if active.ZoneID != "old-zone" {
		t.Fatalf("active zone = %s, want old-zone", active.ZoneID)
	}
	if _, err := os.Stat(filepath.Join(cloudflaredHome, "cfdev-cert-new.example.pem")); err != nil {
		t.Fatalf("new authorization was not preserved: %v", err)
	}
}

func TestDomainRestartFailureReportsPersistedSwitch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CFDEV_HOME", home)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(home, "cloudflared-args.json")
	t.Setenv("CFDEV_CLOUDFLARED", executable)
	t.Setenv("CFDEV_TEST_CLOUDFLARED_HELPER", "1")
	t.Setenv("CFDEV_TEST_CLOUDFLARED_LOG", logPath)
	t.Setenv("CFDEV_TEST_CLOUDFLARED_STDOUT", `[{"id":"123e4567-e89b-42d3-a456-426614174000","name":"cfdev-test-a1b2c3"}]`)
	t.Setenv("CFDEV_TEST_CLOUDFLARED_HOLD_RUN", "1")

	certPath := filepath.Join(home, "cert.pem")
	writeTestOriginCert(t, certPath)
	t.Setenv("CFDEV_ORIGIN_CERT", certPath)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path != "/zones/zone-test" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = writer.Write([]byte(`{"success":true,"result":{"name":"new.example"}}`))
	}))
	defer server.Close()
	t.Setenv("CFDEV_API_URL", server.URL)

	credentialsPath := filepath.Join(home, "123e4567-e89b-42d3-a456-426614174000.json")
	if err := os.WriteFile(credentialsPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths, _ := config.ResolvePaths()
	cfg := &config.Config{
		Version: 1, Domain: "old.example", TunnelName: "cfdev-test-a1b2c3",
		TunnelID:        "123e4567-e89b-42d3-a456-426614174000",
		CredentialsFile: credentialsPath, MachineID: "test-a1b2c3", Mappings: []config.Mapping{},
	}
	if err := config.Save(paths, cfg); err != nil {
		t.Fatal(err)
	}
	starter := exec.Command(executable)
	starter.Env = append(os.Environ(), "CFDEV_TEST_START_BACKGROUND_FIXTURE=1")
	if output, err := starter.CombinedOutput(); err != nil {
		t.Fatalf("could not establish running tunnel fixture: %v: %s", err, output)
	}
	manager := processmanager.Manager{Paths: paths, Client: &cloudflared.Client{Binary: executable}}
	if status := manager.Status(); !status.Running || status.Mode != "background" {
		t.Fatalf("unexpected running tunnel fixture status: %#v", status)
	}
	defer func() { _, _ = manager.Stop() }()
	t.Setenv("CFDEV_TEST_CLOUDFLARED_HOLD_RUN", "")
	t.Setenv("CFDEV_TEST_CLOUDFLARED_FILL_LOG", "")
	rotatedLog := paths.Log + ".1"
	if err := os.Mkdir(rotatedLog, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rotatedLog, "keep"), []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	application := New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"domain", "new.example", "--json"}); code != 1 {
		t.Fatalf("exit code = %d, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		Data  map[string]any `json:"data"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "DOMAIN_SWITCHED_TUNNEL_STOPPED" || envelope.Data["domain_changed"] != true || envelope.Data["retry_command"] != "cfdev up -d" {
		t.Fatalf("restart failure lost the persisted-switch contract: %s", stdout.String())
	}
	saved, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Domain != "new.example" {
		t.Fatalf("saved domain = %q, want new.example", saved.Domain)
	}
}

func TestResetRequiresConfirmationThenForgetsExactMachine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CFDEV_HOME", home)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(home, "cloudflared-args.json")
	t.Setenv("CFDEV_CLOUDFLARED", executable)
	t.Setenv("CFDEV_TEST_CLOUDFLARED_HELPER", "1")
	t.Setenv("CFDEV_TEST_CLOUDFLARED_LOG", logPath)

	certPath := filepath.Join(home, "cert.pem")
	writeTestOriginCert(t, certPath)
	t.Setenv("CFDEV_ORIGIN_CERT", certPath)
	deletedDNS := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodGet:
			if request.URL.Path == "/zones/zone-test" {
				_, _ = writer.Write([]byte(`{"success":true,"result":{"name":"example.com"}}`))
			} else {
				_, _ = writer.Write([]byte(`{"success":true,"result":[{"id":"demo-record","name":"demo.example.com","type":"CNAME","content":"123e4567-e89b-42d3-a456-426614174000.cfargotunnel.com"}]}`))
			}
		case http.MethodDelete:
			deletedDNS = request.URL.Path == "/zones/zone-test/dns_records/demo-record"
			_, _ = writer.Write([]byte(`{"success":true,"result":{}}`))
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	t.Setenv("CFDEV_API_URL", server.URL)

	paths, _ := config.ResolvePaths()
	credentialsPath := filepath.Join(home, "123e4567-e89b-42d3-a456-426614174000.json")
	if err := os.WriteFile(credentialsPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.MachineID, []byte("test-a1b2c3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Version: 1, Domain: "example.com", TunnelName: "cfdev-test-a1b2c3",
		TunnelID:        "123e4567-e89b-42d3-a456-426614174000",
		CredentialsFile: credentialsPath, MachineID: "test-a1b2c3",
		Mappings: []config.Mapping{{Subdomain: "demo", Port: 3000, Protocol: "http"}},
	}
	if err := config.Save(paths, cfg); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	application := New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"reset", "--json"}); code != 2 {
		t.Fatalf("unconfirmed exit code = %d, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(paths.Config); err != nil {
		t.Fatalf("unconfirmed reset changed state: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	application = New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"reset", "--yes", "--json"}); code != 0 {
		t.Fatalf("confirmed exit code = %d, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !deletedDNS {
		t.Fatal("owned DNS record was not deleted")
	}
	for _, removedPath := range []string{paths.Config, paths.Ingress, paths.MachineID, credentialsPath} {
		if _, err := os.Stat(removedPath); !os.IsNotExist(err) {
			t.Fatalf("%s still exists or returned unexpected error: %v", removedPath, err)
		}
	}
	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("origin certificate was not preserved: %v", err)
	}
	var args []string
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, &args); err != nil {
		t.Fatal(err)
	}
	if !containsArgument(args, "delete") || !containsArgument(args, cfg.TunnelID) {
		t.Fatalf("unexpected cloudflared delete args: %v", args)
	}

	stdout.Reset()
	stderr.Reset()
	application = New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"reset", "--json"}); code != 0 {
		t.Fatalf("idempotent reset exit code = %d, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"already_reset":true`) {
		t.Fatalf("unexpected idempotent reset: %s", stdout.String())
	}
}

func TestResetRestoresDeletedDNSWhenLaterCleanupFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CFDEV_HOME", home)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(home, "cloudflared-args.json")
	t.Setenv("CFDEV_CLOUDFLARED", executable)
	t.Setenv("CFDEV_TEST_CLOUDFLARED_HELPER", "1")
	t.Setenv("CFDEV_TEST_CLOUDFLARED_LOG", logPath)

	certPath := filepath.Join(home, "cert.pem")
	writeTestOriginCert(t, certPath)
	t.Setenv("CFDEV_ORIGIN_CERT", certPath)
	firstDeleted := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/zones/zone-test":
			_, _ = writer.Write([]byte(`{"success":true,"result":{"name":"example.com"}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/zones/zone-test/dns_records" && request.URL.Query().Get("name") == "first.example.com":
			_, _ = writer.Write([]byte(`{"success":true,"result":[{"id":"first-record","name":"first.example.com","type":"CNAME","content":"123e4567-e89b-42d3-a456-426614174000.cfargotunnel.com"}]}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/zones/zone-test/dns_records/first-record":
			firstDeleted = true
			_, _ = writer.Write([]byte(`{"success":true,"result":{}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/zones/zone-test/dns_records" && request.URL.Query().Get("name") == "second.example.com":
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"success":false,"errors":[{"message":"temporary DNS failure"}]}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"success":false,"errors":[{"message":"not found"}]}`))
		}
	}))
	defer server.Close()
	t.Setenv("CFDEV_API_URL", server.URL)

	paths, _ := config.ResolvePaths()
	credentialsPath := filepath.Join(home, "123e4567-e89b-42d3-a456-426614174000.json")
	if err := os.WriteFile(credentialsPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Version: 1, Domain: "example.com", TunnelName: "cfdev-test-a1b2c3",
		TunnelID:        "123e4567-e89b-42d3-a456-426614174000",
		CredentialsFile: credentialsPath, MachineID: "test-a1b2c3",
		Mappings: []config.Mapping{
			{Subdomain: "first", Port: 3000, Protocol: "http"},
			{Subdomain: "second", Port: 3001, Protocol: "http"},
		},
	}
	if err := config.Save(paths, cfg); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	application := New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"reset", "--yes", "--json"}); code != 1 {
		t.Fatalf("exit code = %d, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !firstDeleted {
		t.Fatal("the fixture did not delete the first DNS record")
	}
	var envelope struct {
		Data struct {
			DNSRestored []string `json:"dns_restored"`
		} `json:"data"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "CLOUDFLARE_API_ERROR" || len(envelope.Data.DNSRestored) != 1 || envelope.Data.DNSRestored[0] != "first.example.com" {
		t.Fatalf("unexpected reset rollback result: %s", stdout.String())
	}
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal(contents, &args); err != nil {
		t.Fatal(err)
	}
	if !containsArgument(args, "route") || !containsArgument(args, "dns") || !containsArgument(args, "first.example.com") {
		t.Fatalf("DNS rollback was not routed through the exact tunnel: %v", args)
	}
	if _, err := config.Load(paths); err != nil {
		t.Fatalf("failed reset removed local state: %v", err)
	}
}

func writeTestOriginCert(t *testing.T, path string) {
	t.Helper()
	writeOriginCert(t, path, "zone-test", "account-test")
}

func setTestUserHome(t *testing.T, path string) {
	t.Helper()
	t.Setenv("HOME", path)
	t.Setenv("USERPROFILE", path)
}

func writeOriginCert(t *testing.T, path, zoneID, accountID string) {
	t.Helper()
	certJSON, err := json.Marshal(map[string]string{"zoneID": zoneID, "accountID": accountID, "apiToken": "token-test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "ARGO TUNNEL TOKEN", Bytes: certJSON}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func containsArgument(args []string, wanted string) bool {
	for _, argument := range args {
		if argument == wanted {
			return true
		}
	}
	return false
}

func TestInvalidOptionStillHonorsJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	application := New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"list", "--wat", "--json"}); code != 3 {
		t.Fatalf("exit code = %d", code)
	}
	var envelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("stdout is not JSON: %q: %v", stdout.String(), err)
	}
	if envelope.OK || envelope.Error.Code != "INVALID_USAGE" {
		t.Fatalf("unexpected envelope: %s", stdout.String())
	}
}

func TestConfiguredDashboardReturnsMappings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CFDEV_HOME", home)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFDEV_CLOUDFLARED", executable)
	paths, _ := config.ResolvePaths()
	cfg := &config.Config{
		Version: 1, Domain: "example.com", TunnelName: "cfdev-test-a1b2c3",
		TunnelID:        "123e4567-e89b-42d3-a456-426614174000",
		CredentialsFile: filepath.Join(home, "credential.json"), MachineID: "test-a1b2c3",
		Mappings: []config.Mapping{{Subdomain: "demo", Port: 65534, Protocol: "http", CreatedAt: time.Now().UTC()}},
	}
	if err := config.Save(paths, cfg); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	application := New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"--json"}); code != 0 {
		t.Fatalf("exit code = %d, stderr=%s", code, stderr.String())
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Mappings []struct {
				Hostname string `json:"hostname"`
			} `json:"mappings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK || len(envelope.Data.Mappings) != 1 || envelope.Data.Mappings[0].Hostname != "demo.example.com" {
		t.Fatalf("unexpected envelope: %s", stdout.String())
	}
}

func TestFullHostnameRemovalAndClearOwnedHostnames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CFDEV_HOME", home)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFDEV_CLOUDFLARED", executable)

	certPath := filepath.Join(home, "cert.pem")
	certJSON := []byte(`{"zoneID":"zone-test","accountID":"account-test","apiToken":"token-test"}`)
	certContents := pem.EncodeToMemory(&pem.Block{Type: "ARGO TUNNEL TOKEN", Bytes: certJSON})
	if err := os.WriteFile(certPath, certContents, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFDEV_ORIGIN_CERT", certPath)

	deleted := make(map[string]bool)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodGet:
			hostname := request.URL.Query().Get("name")
			id := strings.TrimSuffix(hostname, ".example.com")
			_, _ = fmt.Fprintf(writer, `{"success":true,"result":[{"id":%q,"name":%q,"type":"CNAME","content":"123e4567-e89b-42d3-a456-426614174000.cfargotunnel.com"}]}`, id, hostname)
		case http.MethodDelete:
			deleted[filepath.Base(request.URL.Path)] = true
			_, _ = writer.Write([]byte(`{"success":true,"result":{}}`))
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	t.Setenv("CFDEV_API_URL", server.URL)

	paths, _ := config.ResolvePaths()
	cfg := &config.Config{
		Version: 1, Domain: "example.com", TunnelName: "cfdev-test-a1b2c3",
		TunnelID:        "123e4567-e89b-42d3-a456-426614174000",
		CredentialsFile: filepath.Join(home, "credential.json"), MachineID: "test-a1b2c3",
		Mappings: []config.Mapping{
			{Subdomain: "app-one", Port: 3000, Protocol: "http", CreatedAt: time.Now().UTC()},
			{Subdomain: "app-two", Port: 3005, Protocol: "http", CreatedAt: time.Now().UTC()},
		},
	}
	if err := config.Save(paths, cfg); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	application := New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"remove", "app-one.example.com", "--json"}); code != 0 {
		t.Fatalf("full-hostname removal exit code = %d, stdout=%s, stderr=%s", code, stdout.String(), stderr.String())
	}
	saved, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Mappings) != 1 || saved.Mappings[0].Subdomain != "app-two" || !deleted["app-one"] {
		t.Fatalf("full-hostname removal failed: mappings=%#v deleted=%#v", saved.Mappings, deleted)
	}

	stdout.Reset()
	stderr.Reset()
	application = New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"clear", "--json"}); code != 2 {
		t.Fatalf("unconfirmed exit code = %d, stdout=%s, stderr=%s", code, stdout.String(), stderr.String())
	}
	var unconfirmed struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &unconfirmed); err != nil {
		t.Fatal(err)
	}
	if unconfirmed.OK || unconfirmed.Error.Code != "CONFIRMATION_REQUIRED" || len(deleted) != 1 {
		t.Fatalf("unexpected unconfirmed result: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	application = New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
	if code := application.Run(context.Background(), []string{"remove", "--all", "--yes", "--json"}); code != 0 {
		t.Fatalf("confirmed exit code = %d, stdout=%s, stderr=%s", code, stdout.String(), stderr.String())
	}
	if len(deleted) != 2 || !deleted["app-one"] || !deleted["app-two"] {
		t.Fatalf("deleted records = %#v", deleted)
	}
	saved, err = config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Mappings) != 0 {
		t.Fatalf("mappings were not cleared: %#v", saved.Mappings)
	}
}

func TestClaimAndForceDNSConflictMatrix(t *testing.T) {
	foreignTunnel := map[string]any{
		"id": "foreign-tunnel", "name": "demo.example.com", "type": "CNAME",
		"content": "999e4567-e89b-42d3-a456-426614174999.cfargotunnel.com",
	}
	unrelatedAddress := map[string]any{
		"id": "unrelated-address", "name": "demo.example.com", "type": "A", "content": "192.0.2.10",
	}
	unrelatedText := map[string]any{
		"id": "unrelated-text", "name": "demo.example.com", "type": "TXT", "content": "keep-me",
	}
	tests := []struct {
		name          string
		args          []string
		records       []map[string]any
		wantExit      int
		wantError     string
		wantRoute     bool
		wantOverwrite bool
		wantClaimed   bool
	}{
		{
			name: "claim safely moves a foreign tunnel CNAME", args: []string{"claim", "demo", "3000", "--json"},
			records: []map[string]any{foreignTunnel}, wantRoute: true, wantOverwrite: true, wantClaimed: true,
		},
		{
			name: "claim refuses an unrelated address record", args: []string{"claim", "demo", "3000", "--json"},
			records: []map[string]any{unrelatedAddress}, wantExit: 5, wantError: "DNS_CONFLICT",
		},
		{
			name: "claim refuses mixed tunnel and unrelated records", args: []string{"claim", "demo", "3000", "--json"},
			records: []map[string]any{foreignTunnel, unrelatedText}, wantExit: 5, wantError: "DNS_CONFLICT",
		},
		{
			name: "plain add points foreign tunnel users to claim", args: []string{"add", "demo", "3000", "--json"},
			records: []map[string]any{foreignTunnel}, wantExit: 5, wantError: "DNS_CONFLICT",
		},
		{
			name: "force deliberately replaces an unrelated record", args: []string{"add", "demo", "3000", "--force", "--json"},
			records: []map[string]any{unrelatedAddress}, wantRoute: true, wantOverwrite: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CFDEV_HOME", home)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(home, "cloudflared-args.json")
			t.Setenv("CFDEV_CLOUDFLARED", executable)
			t.Setenv("CFDEV_TEST_CLOUDFLARED_HELPER", "1")
			t.Setenv("CFDEV_TEST_CLOUDFLARED_LOG", logPath)

			certPath := filepath.Join(home, "cert.pem")
			certJSON := []byte(`{"zoneID":"zone-test","accountID":"account-test","apiToken":"token-test"}`)
			if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "ARGO TUNNEL TOKEN", Bytes: certJSON}), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CFDEV_ORIGIN_CERT", certPath)

			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.Method != http.MethodGet || request.URL.Path != "/zones/zone-test/dns_records" {
					writer.WriteHeader(http.StatusNotFound)
					_, _ = writer.Write([]byte(`{"success":false,"errors":[{"message":"not found"}]}`))
					return
				}
				_ = json.NewEncoder(writer).Encode(map[string]any{"success": true, "result": test.records})
			}))
			defer server.Close()
			t.Setenv("CFDEV_API_URL", server.URL)

			paths, _ := config.ResolvePaths()
			cfg := &config.Config{
				Version: 1, Domain: "example.com", TunnelName: "cfdev-test-a1b2c3",
				TunnelID:        "123e4567-e89b-42d3-a456-426614174000",
				CredentialsFile: filepath.Join(home, "credential.json"), MachineID: "test-a1b2c3",
				Mappings: []config.Mapping{},
			}
			if err := config.Save(paths, cfg); err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			application := New(bytes.NewBuffer(nil), &stdout, &stderr, t.TempDir())
			if code := application.Run(context.Background(), test.args); code != test.wantExit {
				t.Fatalf("exit code = %d, want %d; stdout=%s stderr=%s", code, test.wantExit, stdout.String(), stderr.String())
			}
			var envelope struct {
				OK    bool           `json:"ok"`
				Data  map[string]any `json:"data"`
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatalf("invalid JSON output %q: %v", stdout.String(), err)
			}
			if envelope.Error.Code != test.wantError {
				t.Fatalf("error code = %q, want %q; output=%s", envelope.Error.Code, test.wantError, stdout.String())
			}
			if claimed, _ := envelope.Data["claimed"].(bool); claimed != test.wantClaimed {
				t.Fatalf("claimed = %v, want %v; output=%s", claimed, test.wantClaimed, stdout.String())
			}

			routeArgs := []string{}
			if contents, readErr := os.ReadFile(logPath); readErr == nil {
				if err := json.Unmarshal(contents, &routeArgs); err != nil {
					t.Fatal(err)
				}
			} else if !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			if routed := len(routeArgs) > 0; routed != test.wantRoute {
				t.Fatalf("route called = %v, want %v; args=%v", routed, test.wantRoute, routeArgs)
			}
			overwrite := false
			for _, argument := range routeArgs {
				if argument == "--overwrite-dns" {
					overwrite = true
				}
			}
			if overwrite != test.wantOverwrite {
				t.Fatalf("overwrite = %v, want %v; args=%v", overwrite, test.wantOverwrite, routeArgs)
			}
		})
	}
}
