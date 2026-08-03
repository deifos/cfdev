package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/deifos/cfdev/internal/failure"
)

var (
	domainPattern    = regexp.MustCompile(`^(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)(?:\.(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?))+$`)
	subdomainPattern = regexp.MustCompile(`^(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)$`)
	uuidPattern      = regexp.MustCompile(`^(?i:[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12})$`)
)

type Mapping struct {
	Subdomain string    `json:"subdomain"`
	Port      int       `json:"port"`
	Protocol  string    `json:"protocol"`
	CreatedAt time.Time `json:"created_at"`
}

type Preferences struct {
	OpenBrowserOnAdd bool `json:"open_browser_on_add"`
}

type Config struct {
	Version         int         `json:"version"`
	Domain          string      `json:"domain"`
	TunnelName      string      `json:"tunnel_name"`
	TunnelID        string      `json:"tunnel_id"`
	CredentialsFile string      `json:"credentials_file"`
	MachineID       string      `json:"machine_id"`
	Mappings        []Mapping   `json:"mappings"`
	Preferences     Preferences `json:"preferences"`
}

type Paths struct {
	Home         string
	Config       string
	Ingress      string
	Process      string
	ConnectorPID string
	Log          string
	Inspector    string
	InspectorLog string
	ManagedBin   string
	MachineID    string
}

func ResolvePaths() (Paths, error) {
	home := strings.TrimSpace(os.Getenv("CFDEV_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, failure.Wrap("HOME_NOT_FOUND", "could not determine your home directory", failure.ExitConfig, err)
		}
		home = filepath.Join(userHome, ".cfdev")
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		return Paths{}, err
	}
	binName := "cloudflared"
	if strings.EqualFold(filepath.Ext(os.Args[0]), ".exe") || os.PathSeparator == '\\' {
		binName += ".exe"
	}
	return Paths{
		Home:         absHome,
		Config:       filepath.Join(absHome, "config.json"),
		Ingress:      filepath.Join(absHome, "cloudflared.yml"),
		Process:      filepath.Join(absHome, "process.json"),
		ConnectorPID: filepath.Join(absHome, "cloudflared.pid"),
		Log:          filepath.Join(absHome, "cloudflared.log"),
		Inspector:    filepath.Join(absHome, "inspector.json"),
		InspectorLog: filepath.Join(absHome, "inspector.log"),
		ManagedBin:   filepath.Join(absHome, "bin", binName),
		MachineID:    filepath.Join(absHome, "machine-id"),
	}, nil
}

func Load(paths Paths) (*Config, error) {
	contents, err := os.ReadFile(paths.Config)
	if os.IsNotExist(err) {
		failureErr := failure.New("NOT_INITIALIZED", "cfdev has not been set up yet", failure.ExitConfig)
		failureErr.Hint = "Run `cfdev setup` to sign in and create your permanent tunnel."
		return nil, failureErr
	}
	if err != nil {
		return nil, failure.Wrap("CONFIG_READ_FAILED", "could not read the cfdev config", failure.ExitConfig, err)
	}
	var cfg Config
	if err := json.Unmarshal(contents, &cfg); err != nil {
		failureErr := failure.Wrap("INVALID_CONFIG", "the cfdev config is not valid JSON", failure.ExitConfig, err)
		failureErr.Hint = "Run `cfdev config path` to locate it, repair the JSON, then run `cfdev doctor`."
		return nil, failureErr
	}
	if err := Validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func LoadOptional(paths Paths) (*Config, error) {
	cfg, err := Load(paths)
	if typed, ok := err.(*failure.Error); ok && typed.Code == "NOT_INITIALIZED" {
		return nil, nil
	}
	return cfg, err
}

func Save(paths Paths, cfg *Config) error {
	if err := Validate(cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		return failure.Wrap("CONFIG_WRITE_FAILED", "could not create the cfdev directory", failure.ExitConfig, err)
	}
	encoded, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	previousConfig, previousErr := os.ReadFile(paths.Config)
	configExisted := previousErr == nil
	if previousErr != nil && !os.IsNotExist(previousErr) {
		return failure.Wrap("CONFIG_READ_FAILED", "could not preserve the current cfdev config before saving", failure.ExitConfig, previousErr)
	}
	if err := atomicWrite(paths.Config, encoded, 0o600); err != nil {
		return failure.Wrap("CONFIG_WRITE_FAILED", "could not save the cfdev config", failure.ExitConfig, err)
	}
	if err := WriteIngress(paths, cfg); err != nil {
		rollbackErr := restoreConfig(paths.Config, previousConfig, configExisted)
		if rollbackErr != nil {
			typed := failure.As(err)
			if typed.Hint != "" {
				typed.Hint += " "
			}
			typed.Hint += "Restoring the previous config also failed: " + rollbackErr.Error()
			return typed
		}
		return err
	}
	return nil
}

func restoreConfig(path string, previous []byte, existed bool) error {
	if existed {
		return atomicWrite(path, previous, 0o600)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func WriteIngress(paths Paths, cfg *Config) error {
	return WriteIngressMode(paths, cfg, false)
}

// WriteInspectorIngress routes public traffic through cfdev's loopback-only
// inspection gateway. WriteIngress keeps the direct route as the safe fallback.
func WriteInspectorIngress(paths Paths, cfg *Config) error {
	return WriteIngressMode(paths, cfg, true)
}

func WriteIngressMode(paths Paths, cfg *Config, inspector bool) error {
	contents := BuildIngressMode(cfg, inspector)
	if err := atomicWrite(paths.Ingress, []byte(contents), 0o600); err != nil {
		return failure.Wrap("INGRESS_WRITE_FAILED", "could not generate the cloudflared ingress file", failure.ExitConfig, err)
	}
	return nil
}

func BuildIngress(cfg *Config) string {
	return BuildIngressMode(cfg, false)
}

func BuildIngressMode(cfg *Config, inspector bool) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "tunnel: %s\n", strconv.Quote(cfg.TunnelID))
	fmt.Fprintf(&builder, "credentials-file: %s\n\n", strconv.Quote(filepath.ToSlash(cfg.CredentialsFile)))
	builder.WriteString("ingress:\n")
	ordered := append([]Mapping(nil), cfg.Mappings...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Subdomain < ordered[j].Subdomain })
	for _, mapping := range ordered {
		fmt.Fprintf(&builder, "  - hostname: %s\n", strconv.Quote(mapping.Subdomain+"."+cfg.Domain))
		service := fmt.Sprintf("%s://localhost:%d", mapping.Protocol, mapping.Port)
		if inspector {
			service = "http://127.0.0.1:4041"
		}
		fmt.Fprintf(&builder, "    service: %s\n", strconv.Quote(service))
	}
	builder.WriteString("  - service: http_status:404\n")
	return builder.String()
}

func Validate(cfg *Config) error {
	if cfg.Version != 1 {
		return invalid("unsupported or missing config version")
	}
	domain, err := NormalizeDomain(cfg.Domain)
	if err != nil {
		return err
	}
	cfg.Domain = domain
	if cfg.TunnelName == "" || len(cfg.TunnelName) > 63 {
		return invalid("invalid tunnel name")
	}
	if !uuidPattern.MatchString(cfg.TunnelID) {
		return invalid("invalid tunnel ID")
	}
	if cfg.CredentialsFile == "" || !filepath.IsAbs(cfg.CredentialsFile) {
		return invalid("credentials_file must be an absolute path")
	}
	seen := make(map[string]bool)
	for index := range cfg.Mappings {
		mapping := &cfg.Mappings[index]
		mapping.Subdomain, err = NormalizeSubdomain(mapping.Subdomain)
		if err != nil {
			return err
		}
		if seen[mapping.Subdomain] {
			return invalid("duplicate mapping: " + mapping.Subdomain)
		}
		seen[mapping.Subdomain] = true
		if err := ValidatePort(mapping.Port); err != nil {
			return err
		}
		if mapping.Protocol == "" {
			mapping.Protocol = "http"
		}
		if mapping.Protocol != "http" && mapping.Protocol != "https" {
			return invalid("mapping protocol must be http or https")
		}
	}
	return nil
}

func NormalizeDomain(value string) (string, error) {
	domain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	domain = strings.TrimPrefix(strings.TrimPrefix(domain, "https://"), "http://")
	if len(domain) > 253 || !domainPattern.MatchString(domain) {
		err := failure.New("INVALID_DOMAIN", fmt.Sprintf("%q is not a valid domain", value), failure.ExitUsage)
		err.Hint = "Use a domain already active on Cloudflare, such as example.com."
		return "", err
	}
	return domain, nil
}

func NormalizeSubdomain(value string) (string, error) {
	subdomain := strings.ToLower(strings.TrimSpace(value))
	if !subdomainPattern.MatchString(subdomain) {
		err := failure.New("INVALID_SUBDOMAIN", fmt.Sprintf("%q is not a valid subdomain", value), failure.ExitUsage)
		err.Hint = "Use one DNS label containing letters, numbers, or hyphens, such as my-app."
		return "", err
	}
	return subdomain, nil
}

// NormalizeProjectName accepts the short project name used by cfdev commands
// and, as a forgiving fallback, the full hostname on the configured domain.
func NormalizeProjectName(value, domain string) (string, error) {
	normalizedDomain, err := NormalizeDomain(domain)
	if err != nil {
		return "", err
	}
	identifier := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	suffix := "." + normalizedDomain
	if strings.HasSuffix(identifier, suffix) {
		identifier = strings.TrimSuffix(identifier, suffix)
	} else if strings.Contains(identifier, ".") {
		invalid := failure.New("INVALID_SUBDOMAIN", fmt.Sprintf("%q is not a project name or hostname on %s", value, normalizedDomain), failure.ExitUsage)
		invalid.Hint = "Use just the project name, such as `screenslick`, or run `cfdev list` to see available names."
		return "", invalid
	}
	if strings.Contains(identifier, ".") {
		invalid := failure.New("INVALID_SUBDOMAIN", fmt.Sprintf("%q contains more than one project-name label", value), failure.ExitUsage)
		invalid.Hint = "Use one short name, such as `screenslick`."
		return "", invalid
	}
	subdomain, err := NormalizeSubdomain(identifier)
	if err != nil {
		typed := failure.As(err)
		typed.Hint = fmt.Sprintf("Use just the project name, such as `screenslick`; the full hostname `screenslick.%s` also works.", normalizedDomain)
		return "", typed
	}
	return subdomain, nil
}

func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		err := failure.New("INVALID_PORT", fmt.Sprintf("%d is not a valid port", port), failure.ExitUsage)
		err.Hint = "Use a port from 1 to 65535, such as 3000."
		return err
	}
	return nil
}

func MachineIdentity(paths Paths) (string, string, error) {
	if contents, err := os.ReadFile(paths.MachineID); err == nil {
		machineID := strings.TrimSpace(string(contents))
		if subdomainPattern.MatchString(machineID) {
			return machineID, "cfdev-" + machineID, nil
		}
	}
	hostname, _ := os.Hostname()
	slug := slugify(hostname)
	if slug == "" {
		sum := sha256.Sum256([]byte(hostname + os.Getenv("USERNAME") + os.Getenv("USER")))
		slug = "machine-" + hex.EncodeToString(sum[:])[:8]
	}
	if len(slug) > 55 {
		sum := sha256.Sum256([]byte(slug))
		slug = strings.Trim(slug[:46], "-") + "-" + hex.EncodeToString(sum[:])[:8]
	}
	identifier := make([]byte, 3)
	if _, err := rand.Read(identifier); err != nil {
		fallback := sha256.Sum256([]byte(slug + time.Now().UTC().String()))
		identifier = fallback[:3]
	}
	machineID := slug + "-" + hex.EncodeToString(identifier)
	if err := atomicWrite(paths.MachineID, []byte(machineID+"\n"), 0o600); err != nil {
		return "", "", failure.Wrap("MACHINE_ID_WRITE_FAILED", "could not save this machine's cfdev identity", failure.ExitConfig, err)
	}
	return machineID, "cfdev-" + machineID, nil
}

func SuggestSubdomain(directory string) string {
	name := strings.ToLower(filepath.Base(filepath.Clean(directory)))
	for _, suffix := range []string{"-frontend", "-server", "-app", "-web", "-api"} {
		name = strings.TrimSuffix(name, suffix)
	}
	name = slugify(name)
	if name == "" {
		return "app"
	}
	if len(name) > 63 {
		name = strings.Trim(name[:63], "-")
	}
	return name
}

func slugify(value string) string {
	value = strings.ToLower(value)
	var builder strings.Builder
	lastDash := false
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			builder.WriteRune(char)
			lastDash = false
		} else if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func atomicWrite(target string, contents []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".cfdev-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return replaceFile(temporaryName, target)
}

func invalid(reason string) error {
	err := failure.New("INVALID_CONFIG", "the cfdev config is invalid: "+reason, failure.ExitConfig)
	err.Hint = "Repair the config and run `cfdev doctor` again."
	return err
}
