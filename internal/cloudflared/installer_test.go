package cloudflared

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/deifos/cfdev/internal/config"
)

func TestInstallUsesReleaseDigest(t *testing.T) {
	assetName, compressed, err := platformAsset()
	if err != nil {
		t.Skip(err)
	}
	payload := []byte("fake cloudflared executable")
	asset := payload
	if compressed {
		var buffer bytes.Buffer
		gzipWriter := gzip.NewWriter(&buffer)
		tarWriter := tar.NewWriter(gzipWriter)
		_ = tarWriter.WriteHeader(&tar.Header{Name: "cloudflared", Mode: 0o700, Size: int64(len(payload))})
		_, _ = tarWriter.Write(payload)
		_ = tarWriter.Close()
		_ = gzipWriter.Close()
		asset = buffer.Bytes()
	}
	digest := sha256.Sum256(asset)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/release" {
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"tag_name": "test-release",
				"assets": []map[string]any{{
					"name": assetName, "browser_download_url": server.URL + "/asset",
					"digest": "sha256:" + hex.EncodeToString(digest[:]), "size": len(asset),
				}},
			})
			return
		}
		if request.URL.Path == "/asset" {
			_, _ = writer.Write(asset)
			return
		}
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	t.Setenv("CFDEV_RELEASES_URL", server.URL+"/release")
	t.Setenv("CFDEV_HOME", t.TempDir())
	paths, _ := config.ResolvePaths()
	client, release, err := Install(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if release != "test-release" || client.Binary != paths.ManagedBin {
		t.Fatalf("client=%#v release=%q", client, release)
	}
	installed, err := os.ReadFile(paths.ManagedBin)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(installed, payload) {
		t.Fatalf("installed content = %q", installed)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(paths.ManagedBin)
		if info.Mode().Perm()&0o100 == 0 {
			t.Fatal("managed binary is not executable")
		}
	}
	_ = fmt.Sprint(filepath.Separator)
}
