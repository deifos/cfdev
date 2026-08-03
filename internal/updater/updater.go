package updater

import (
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
	"strconv"
	"strings"
	"time"

	"github.com/deifos/cfdev/internal/failure"
)

const defaultReleaseURL = "https://api.github.com/repos/deifos/cfdev/releases/latest"

type Result struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	Asset          string `json:"asset,omitempty"`
	Executable     string `json:"executable"`
	Updated        bool   `json:"updated"`
	Pending        bool   `json:"pending_restart"`
}

type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
	Size        int64  `json:"size"`
}

func Upgrade(ctx context.Context, currentVersion string) (Result, error) {
	executable, err := os.Executable()
	if err != nil {
		return Result{}, failure.Wrap("UPGRADE_FAILED", "could not locate the running cfdev executable", failure.ExitGeneral, err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return Result{}, failure.Wrap("UPGRADE_FAILED", "could not resolve the running cfdev executable", failure.ExitGeneral, err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	result := Result{CurrentVersion: cleanVersion(currentVersion), Executable: executable}
	if managerCommand := packageManager(executable); managerCommand != "" {
		managerName := strings.Fields(managerCommand)[0]
		failureErr := failure.New("PACKAGE_MANAGED", "this cfdev installation is managed by "+managerName, failure.ExitConfig)
		failureErr.Hint = "Use `" + managerCommand + "` instead of `cfdev upgrade`."
		return result, failureErr
	}

	endpoint := strings.TrimSpace(os.Getenv("CFDEV_UPDATE_URL"))
	if endpoint == "" {
		endpoint = defaultReleaseURL
	}
	latest, err := fetchRelease(ctx, endpoint)
	if err != nil {
		return result, err
	}
	result.LatestVersion = cleanVersion(latest.TagName)
	if _, ok := parseVersion(result.LatestVersion); !ok {
		return result, failure.New("UPGRADE_METADATA_INVALID", "the latest cfdev release has an invalid version", failure.ExitGeneral)
	}
	if comparison, ok := compareVersions(result.CurrentVersion, result.LatestVersion); ok && comparison >= 0 {
		return result, nil
	}

	wantedName := assetName(runtime.GOOS, runtime.GOARCH)
	binaryAsset, ok := findAsset(latest.Assets, wantedName)
	if !ok {
		return result, failure.New("UPGRADE_UNSUPPORTED", fmt.Sprintf("release %s has no build for %s/%s", latest.TagName, runtime.GOOS, runtime.GOARCH), failure.ExitDependency)
	}
	checksumsAsset, ok := findAsset(latest.Assets, "checksums.txt")
	if !ok {
		return result, failure.New("UPGRADE_UNVERIFIED", "the cfdev release has no checksum manifest", failure.ExitDependency)
	}
	result.Asset = wantedName

	checksums, err := downloadBytes(ctx, checksumsAsset.DownloadURL, 2<<20)
	if err != nil {
		return result, err
	}
	expectedHash, ok := checksumFor(checksums, wantedName)
	if !ok {
		return result, failure.New("UPGRADE_UNVERIFIED", "the cfdev release has no checksum for "+wantedName, failure.ExitDependency)
	}

	temporary, err := os.CreateTemp(filepath.Dir(executable), ".cfdev-upgrade-*")
	if err != nil {
		failureErr := failure.Wrap("UPGRADE_PERMISSION_DENIED", "cfdev cannot write an upgrade beside the current executable", failure.ExitConfig, err)
		failureErr.Hint = "Use the installer or package manager that installed cfdev."
		return result, failureErr
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := downloadVerified(ctx, binaryAsset, expectedHash, temporary); err != nil {
		temporary.Close()
		return result, err
	}
	if err := temporary.Chmod(0o755); err != nil {
		temporary.Close()
		return result, failure.Wrap("UPGRADE_FAILED", "could not mark the downloaded cfdev executable as runnable", failure.ExitGeneral, err)
	}
	if err := temporary.Close(); err != nil {
		return result, failure.Wrap("UPGRADE_FAILED", "could not finish the downloaded cfdev executable", failure.ExitGeneral, err)
	}

	pending, err := installUpgrade(temporaryPath, executable)
	if err != nil {
		failureErr := failure.Wrap("UPGRADE_FAILED", "could not replace the cfdev executable", failure.ExitGeneral, err)
		failureErr.Hint = "Retry the installer or update through your package manager."
		return result, failureErr
	}
	result.Updated = true
	result.Pending = pending
	return result, nil
}

func HandleInternal(args []string) (bool, int) {
	if len(args) != 2 || args[0] != "__cfdev_replace" {
		return false, 0
	}
	if err := runReplacement(args[1]); err != nil {
		return true, 1
	}
	return true, 0
}

func fetchRelease(ctx context.Context, endpoint string) (release, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return release{}, failure.Wrap("UPGRADE_CHECK_FAILED", "could not prepare the cfdev update check", failure.ExitGeneral, err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "cfdev")
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return release{}, failure.Wrap("UPGRADE_CHECK_FAILED", "could not check for a cfdev release", failure.ExitGeneral, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return release{}, failure.New("UPGRADE_CHECK_FAILED", fmt.Sprintf("could not check for a cfdev release (HTTP %d)", response.StatusCode), failure.ExitGeneral)
	}
	var latest release
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&latest); err != nil {
		return release{}, failure.Wrap("UPGRADE_METADATA_INVALID", "could not understand the latest cfdev release", failure.ExitGeneral, err)
	}
	return latest, nil
}

func downloadBytes(ctx context.Context, url string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, failure.Wrap("UPGRADE_DOWNLOAD_FAILED", "could not prepare the cfdev download", failure.ExitGeneral, err)
	}
	request.Header.Set("User-Agent", "cfdev")
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return nil, failure.Wrap("UPGRADE_DOWNLOAD_FAILED", "could not download the cfdev release", failure.ExitGeneral, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, failure.New("UPGRADE_DOWNLOAD_FAILED", fmt.Sprintf("could not download the cfdev release (HTTP %d)", response.StatusCode), failure.ExitGeneral)
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, failure.Wrap("UPGRADE_DOWNLOAD_FAILED", "could not read the cfdev release", failure.ExitGeneral, err)
	}
	if int64(len(contents)) > limit {
		return nil, failure.New("UPGRADE_DOWNLOAD_INVALID", "the cfdev release metadata is unexpectedly large", failure.ExitGeneral)
	}
	return contents, nil
}

func downloadVerified(ctx context.Context, item asset, expectedHash string, destination *os.File) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, item.DownloadURL, nil)
	if err != nil {
		return failure.Wrap("UPGRADE_DOWNLOAD_FAILED", "could not prepare the cfdev binary download", failure.ExitGeneral, err)
	}
	request.Header.Set("User-Agent", "cfdev")
	response, err := (&http.Client{Timeout: 2 * time.Minute}).Do(request)
	if err != nil {
		return failure.Wrap("UPGRADE_DOWNLOAD_FAILED", "could not download the cfdev binary", failure.ExitGeneral, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return failure.New("UPGRADE_DOWNLOAD_FAILED", fmt.Sprintf("could not download the cfdev binary (HTTP %d)", response.StatusCode), failure.ExitGeneral)
	}
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(destination, hasher), io.LimitReader(response.Body, 256<<20))
	if err != nil {
		return failure.Wrap("UPGRADE_DOWNLOAD_FAILED", "could not save the cfdev binary", failure.ExitGeneral, err)
	}
	if item.Size > 0 && written != item.Size {
		return failure.New("UPGRADE_DOWNLOAD_INVALID", "the downloaded cfdev binary has an unexpected size", failure.ExitGeneral)
	}
	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actualHash, expectedHash) {
		return failure.New("UPGRADE_CHECKSUM_MISMATCH", "the downloaded cfdev binary failed checksum verification", failure.ExitDependency)
	}
	return nil
}

func checksumFor(contents []byte, name string) (string, bool) {
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		candidate := strings.TrimPrefix(fields[len(fields)-1], "*")
		if candidate == name {
			if decoded, err := hex.DecodeString(fields[0]); err == nil && len(decoded) == sha256.Size {
				return strings.ToLower(fields[0]), true
			}
		}
	}
	return "", false
}

func assetName(goos, goarch string) string {
	name := "cfdev-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func findAsset(assets []asset, name string) (asset, bool) {
	for _, item := range assets {
		if item.Name == name && item.DownloadURL != "" {
			return item, true
		}
	}
	return asset{}, false
}

type parsedVersion struct {
	Numbers    [3]int
	Prerelease string
}

func cleanVersion(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "v")
}

func parseVersion(value string) (parsedVersion, bool) {
	value = strings.SplitN(cleanVersion(value), "+", 2)[0]
	parts := strings.SplitN(value, "-", 2)
	numbers := strings.Split(parts[0], ".")
	if len(numbers) != 3 {
		return parsedVersion{}, false
	}
	parsed := parsedVersion{}
	for index, number := range numbers {
		value, err := strconv.Atoi(number)
		if err != nil || value < 0 {
			return parsedVersion{}, false
		}
		parsed.Numbers[index] = value
	}
	if len(parts) == 2 {
		parsed.Prerelease = parts[1]
	}
	return parsed, true
}

func compareVersions(left, right string) (int, bool) {
	a, okA := parseVersion(left)
	b, okB := parseVersion(right)
	if !okA || !okB {
		return 0, false
	}
	for index := range a.Numbers {
		if a.Numbers[index] < b.Numbers[index] {
			return -1, true
		}
		if a.Numbers[index] > b.Numbers[index] {
			return 1, true
		}
	}
	if a.Prerelease == "" && b.Prerelease != "" {
		return 1, true
	}
	if a.Prerelease != "" && b.Prerelease == "" {
		return -1, true
	}
	return strings.Compare(a.Prerelease, b.Prerelease), true
}

func packageManager(executable string) string {
	normalized := strings.ToLower(strings.ReplaceAll(filepath.ToSlash(executable), `\`, "/"))
	switch {
	case strings.Contains(normalized, "/homebrew/cellar/") || strings.Contains(normalized, "/linuxbrew/cellar/"):
		return "brew upgrade cfdev"
	case strings.Contains(normalized, "/scoop/apps/"):
		return "scoop update cfdev"
	case strings.Contains(normalized, "/microsoft/winget/packages/") || strings.Contains(normalized, "/winget/packages/") || strings.Contains(normalized, "/microsoft/winget/links/") || strings.Contains(normalized, "/winget/links/"):
		return "winget upgrade deifos.cfdev"
	default:
		return ""
	}
}
