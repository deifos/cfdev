package cloudflare

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestReadOriginCert(t *testing.T) {
	token := OriginCert{ZoneID: "zone", AccountID: "account", APIToken: "secret"}
	contents, _ := json.Marshal(token)
	certPath := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "ARGO TUNNEL TOKEN", Bytes: contents}), 0o600); err != nil {
		t.Fatal(err)
	}
	decoded, err := ReadOriginCert(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ZoneID != token.ZoneID || decoded.APIToken != token.APIToken {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestAPIProvidesZoneAndSafelyDeletesOwnedDNS(t *testing.T) {
	var mutex sync.Mutex
	deleted := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret" {
			writer.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(writer, `{"success":false,"errors":[{"message":"bad auth"}]}`)
			return
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/zones/zone":
			fmt.Fprint(writer, `{"success":true,"result":{"name":"Example.COM"}}`)
		case request.Method == http.MethodGet && request.URL.Path == "/zones/zone/dns_records":
			fmt.Fprint(writer, `{"success":true,"result":[`+
				`{"id":"owned","name":"app.example.com","type":"CNAME","content":"tunnel.cfargotunnel.com"},`+
				`{"id":"other-tunnel","name":"app.example.com","type":"CNAME","content":"other.cfargotunnel.com"},`+
				`{"id":"foreign","name":"app.example.com","type":"TXT","content":"keep me"}`+
				`]}`)
		case request.Method == http.MethodDelete && request.URL.Path == "/zones/zone/dns_records/owned":
			mutex.Lock()
			deleted = append(deleted, "owned")
			mutex.Unlock()
			fmt.Fprint(writer, `{"success":true,"result":{"id":"owned"}}`)
		default:
			writer.WriteHeader(http.StatusNotFound)
			fmt.Fprint(writer, `{"success":false,"errors":[{"message":"not found"}]}`)
		}
	}))
	defer server.Close()
	t.Setenv("CFDEV_API_URL", server.URL)
	api := NewAPI(OriginCert{ZoneID: "zone", AccountID: "account", APIToken: "secret"})
	zone, err := api.ZoneName(context.Background())
	if err != nil || zone != "example.com" {
		t.Fatalf("ZoneName = %q, %v", zone, err)
	}
	state, err := api.DNSState(context.Background(), "app.example.com", "tunnel")
	if err != nil {
		t.Fatal(err)
	}
	if !state.Owned || !state.Conflicting || !state.ForeignTunnel || !state.NonTunnelConflict {
		t.Fatalf("unexpected DNS state: %#v", state)
	}
	removed, err := api.DeleteOwnedDNS(context.Background(), "app.example.com", "tunnel")
	if err != nil || !removed {
		t.Fatalf("DeleteOwnedDNS = %v, %v", removed, err)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(deleted) != 1 || deleted[0] != "owned" {
		t.Fatalf("deleted records = %v; foreign record must remain", deleted)
	}
}

func TestTunnelTargetDetection(t *testing.T) {
	tests := map[string]bool{
		"tunnel-id.cfargotunnel.com":   true,
		"TUNNEL.cfargotunnel.com.":     true,
		"cfargotunnel.com":             false,
		"app.example.com":              false,
		"notcfargotunnel.com":          false,
		"tunnel.cfargotunnel.com.evil": false,
	}
	for value, expected := range tests {
		if actual := isTunnelTarget(value); actual != expected {
			t.Fatalf("isTunnelTarget(%q) = %v, want %v", value, actual, expected)
		}
	}
}
