package app

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deifos/cfdev/internal/config"
)

func TestMain(m *testing.M) {
	if os.Getenv("CFDEV_TEST_CLOUDFLARED_HELPER") == "1" {
		encoded, err := json.Marshal(os.Args[1:])
		if err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(os.Getenv("CFDEV_TEST_CLOUDFLARED_LOG"), encoded, 0o600); err != nil {
			os.Exit(2)
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
