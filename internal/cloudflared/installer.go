package cloudflared

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/deifos/cfdev/internal/config"
	"github.com/deifos/cfdev/internal/failure"
)

const defaultReleaseURL = "https://api.github.com/repos/cloudflare/cloudflared/releases/latest"

type release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name        string `json:"name"`
		DownloadURL string `json:"browser_download_url"`
		Digest      string `json:"digest"`
		Size        int64  `json:"size"`
	} `json:"assets"`
}

func Install(ctx context.Context, paths config.Paths) (*Client, string, error) {
	releaseURL := strings.TrimSpace(os.Getenv("CFDEV_RELEASES_URL"))
	if releaseURL == "" {
		releaseURL = defaultReleaseURL
	}
	httpClient := &http.Client{Timeout: 2 * time.Minute}
	metadata, err := fetchRelease(ctx, httpClient, releaseURL)
	if err != nil {
		return nil, "", err
	}
	assetName, compressed, err := platformAsset()
	if err != nil {
		return nil, "", err
	}
	var assetURL, digest string
	var assetSize int64
	for _, asset := range metadata.Assets {
		if asset.Name == assetName {
			assetURL = asset.DownloadURL
			digest = strings.TrimPrefix(strings.ToLower(asset.Digest), "sha256:")
			assetSize = asset.Size
			break
		}
	}
	if assetURL == "" {
		return nil, "", failure.New("CLOUDFLARED_PLATFORM_UNSUPPORTED", "the latest cloudflared release has no build for this platform", failure.ExitDependency)
	}
	if len(digest) != 64 {
		failureErr := failure.New("CLOUDFLARED_UNVERIFIED", "the cloudflared release did not include a SHA-256 digest", failure.ExitDependency)
		failureErr.Hint = "Install cloudflared through your system package manager, then retry `cfdev init`."
		return nil, "", failureErr
	}
	if assetSize <= 0 || assetSize > 200<<20 {
		return nil, "", failure.New("CLOUDFLARED_DOWNLOAD_INVALID", "the cloudflared release has an unexpected size", failure.ExitDependency)
	}

	if err := os.MkdirAll(filepath.Dir(paths.ManagedBin), 0o700); err != nil {
		return nil, "", failure.Wrap("CLOUDFLARED_INSTALL_FAILED", "could not create the managed binary directory", failure.ExitDependency, err)
	}
	archive, err := os.CreateTemp(filepath.Dir(paths.ManagedBin), ".cloudflared-download-*")
	if err != nil {
		return nil, "", err
	}
	archivePath := archive.Name()
	archive.Close()
	defer os.Remove(archivePath)

	actualDigest, err := download(ctx, httpClient, assetURL, archivePath, assetSize)
	if err != nil {
		return nil, "", err
	}
	if actualDigest != digest {
		failureErr := failure.New("CLOUDFLARED_CHECKSUM_MISMATCH", "the downloaded cloudflared binary failed checksum verification", failure.ExitDependency)
		failureErr.Hint = "Nothing was installed. Check your network and try again."
		return nil, "", failureErr
	}

	staged := archivePath + ".bin"
	defer os.Remove(staged)
	if compressed {
		if err := extractCloudflared(archivePath, staged); err != nil {
			return nil, "", failure.Wrap("CLOUDFLARED_INSTALL_FAILED", "could not unpack cloudflared", failure.ExitDependency, err)
		}
	} else if err := copyFile(archivePath, staged); err != nil {
		return nil, "", err
	}
	if err := os.Chmod(staged, 0o700); err != nil {
		return nil, "", err
	}
	_ = os.Remove(paths.ManagedBin)
	if err := os.Rename(staged, paths.ManagedBin); err != nil {
		return nil, "", failure.Wrap("CLOUDFLARED_INSTALL_FAILED", "could not install the managed cloudflared binary", failure.ExitDependency, err)
	}
	return &Client{Binary: paths.ManagedBin}, metadata.TagName, nil
}

func fetchRelease(ctx context.Context, client *http.Client, releaseURL string) (release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "cfdev/0.1")
	response, err := client.Do(req)
	if err != nil {
		return release{}, failure.Wrap("CLOUDFLARED_DOWNLOAD_FAILED", "could not look up the latest cloudflared release", failure.ExitDependency, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return release{}, failure.New("CLOUDFLARED_DOWNLOAD_FAILED", fmt.Sprintf("could not look up cloudflared (HTTP %d)", response.StatusCode), failure.ExitDependency)
	}
	var result release
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&result); err != nil {
		return release{}, failure.Wrap("CLOUDFLARED_DOWNLOAD_FAILED", "could not understand the cloudflared release metadata", failure.ExitDependency, err)
	}
	return result, nil
}

func download(ctx context.Context, client *http.Client, assetURL, target string, expectedSize int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "cfdev/0.1")
	response, err := client.Do(req)
	if err != nil {
		return "", failure.Wrap("CLOUDFLARED_DOWNLOAD_FAILED", "could not download cloudflared", failure.ExitDependency, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", failure.New("CLOUDFLARED_DOWNLOAD_FAILED", fmt.Sprintf("could not download cloudflared (HTTP %d)", response.StatusCode), failure.ExitDependency)
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, expectedSize+1))
	closeErr := file.Close()
	if copyErr != nil {
		return "", failure.Wrap("CLOUDFLARED_DOWNLOAD_FAILED", "could not save cloudflared", failure.ExitDependency, copyErr)
	}
	if closeErr != nil {
		return "", closeErr
	}
	if written != expectedSize {
		return "", failure.New("CLOUDFLARED_DOWNLOAD_INVALID", "the cloudflared download was incomplete", failure.ExitDependency)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func platformAsset() (name string, compressed bool, err error) {
	arch := runtime.GOARCH
	switch runtime.GOOS {
	case "windows":
		if arch != "amd64" && arch != "arm64" && arch != "386" {
			return "", false, unsupportedPlatform()
		}
		return "cloudflared-windows-" + arch + ".exe", false, nil
	case "darwin":
		if arch != "amd64" && arch != "arm64" {
			return "", false, unsupportedPlatform()
		}
		return "cloudflared-darwin-" + arch + ".tgz", true, nil
	case "linux":
		suffix := arch
		if arch == "arm" {
			suffix = "arm"
		}
		if arch != "amd64" && arch != "arm64" && arch != "386" && arch != "arm" {
			return "", false, unsupportedPlatform()
		}
		return "cloudflared-linux-" + suffix, false, nil
	default:
		return "", false, unsupportedPlatform()
	}
}

func extractCloudflared(archivePath, target string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != "cloudflared" {
			continue
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, io.LimitReader(tarReader, 200<<20))
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	return fmt.Errorf("archive contained no cloudflared binary")
}

func copyFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func unsupportedPlatform() error {
	return failure.New("CLOUDFLARED_PLATFORM_UNSUPPORTED", fmt.Sprintf("managed cloudflared installation is not supported on %s/%s", runtime.GOOS, runtime.GOARCH), failure.ExitDependency)
}
