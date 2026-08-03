package updater

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/deifos/cfdev/internal/failure"
)

func TestVersionComparison(t *testing.T) {
	tests := []struct {
		left, right string
		want        int
	}{
		{"0.1.0", "0.1.0", 0},
		{"0.1.0-dev", "0.1.0", -1},
		{"0.2.0", "0.1.9", 1},
		{"v1.0.0", "1.1.0", -1},
	}
	for _, test := range tests {
		got, ok := compareVersions(test.left, test.right)
		if !ok || got != test.want {
			t.Fatalf("compareVersions(%q, %q) = %d, %v; want %d", test.left, test.right, got, ok, test.want)
		}
	}
}

func TestChecksumManifestAndVerifiedDownload(t *testing.T) {
	contents := []byte("verified cfdev binary")
	digest := fmt.Sprintf("%x", sha256.Sum256(contents))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(contents)
	}))
	defer server.Close()

	manifest := []byte(digest + "  cfdev-linux-amd64\n")
	expected, ok := checksumFor(manifest, "cfdev-linux-amd64")
	if !ok || expected != digest {
		t.Fatalf("checksumFor = %q, %v", expected, ok)
	}
	destination, err := os.CreateTemp(t.TempDir(), "download-*")
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	if err := downloadVerified(context.Background(), asset{DownloadURL: server.URL, Size: int64(len(contents))}, expected, destination); err != nil {
		t.Fatal(err)
	}
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(destination.Name())
	if string(got) != string(contents) {
		t.Fatalf("download = %q", got)
	}
}

func TestVerifiedDownloadRejectsChecksumMismatch(t *testing.T) {
	contents := []byte("tampered cfdev binary")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(contents)
	}))
	defer server.Close()
	destination, err := os.CreateTemp(t.TempDir(), "download-*")
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()

	err = downloadVerified(context.Background(), asset{DownloadURL: server.URL, Size: int64(len(contents))}, strings.Repeat("0", 64), destination)
	if typed := failure.As(err); typed.Code != "UPGRADE_CHECKSUM_MISMATCH" {
		t.Fatalf("unexpected error: %#v", typed)
	}
}

func TestAssetNamesAndPackageManagers(t *testing.T) {
	if got := assetName("windows", "amd64"); got != "cfdev-windows-amd64.exe" {
		t.Fatalf("assetName = %q", got)
	}
	if manager := packageManager(`/Users/me/homebrew/Cellar/cfdev/0.1.0/bin/cfdev`); !strings.HasPrefix(manager, "brew") {
		t.Fatalf("packageManager = %q", manager)
	}
	if manager := packageManager(`C:\Users\me\AppData\Local\Microsoft\WinGet\Links\cfdev.exe`); !strings.HasPrefix(manager, "winget") {
		t.Fatalf("packageManager = %q", manager)
	}
}

func TestUpgradeDoesNotDownloadWhenAlreadyCurrent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"tag_name":"v0.1.0","assets":[]}`))
	}))
	defer server.Close()
	t.Setenv("CFDEV_UPDATE_URL", server.URL)

	result, err := Upgrade(context.Background(), "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated || result.CurrentVersion != "0.1.0" || result.LatestVersion != "0.1.0" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestUpgradeOverrideRejectsBadChecksumEndToEnd(t *testing.T) {
	binary := []byte("tampered cfdev release")
	name := assetName(runtime.GOOS, runtime.GOARCH)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/release":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"tag_name": "v9.9.9",
				"assets": []map[string]any{
					{"name": name, "browser_download_url": server.URL + "/binary", "size": len(binary)},
					{"name": "checksums.txt", "browser_download_url": server.URL + "/checksums", "size": 68 + len(name)},
				},
			})
		case "/checksums":
			_, _ = fmt.Fprintf(writer, "%s  %s\n", strings.Repeat("0", 64), name)
		case "/binary":
			_, _ = writer.Write(binary)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("CFDEV_UPDATE_URL", server.URL+"/release")

	result, err := Upgrade(context.Background(), "0.1.0")
	if typed := failure.As(err); typed.Code != "UPGRADE_CHECKSUM_MISMATCH" {
		t.Fatalf("unexpected error: %#v", typed)
	}
	if result.Updated {
		t.Fatalf("bad-checksum release was marked updated: %#v", result)
	}
}
