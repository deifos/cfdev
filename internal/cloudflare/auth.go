package cloudflare

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/deifos/cfdev/internal/failure"
)

type OriginCert struct {
	ZoneID    string `json:"zoneID"`
	AccountID string `json:"accountID"`
	APIToken  string `json:"apiToken"`
	Endpoint  string `json:"endpoint,omitempty"`
}

type API struct {
	cert    OriginCert
	baseURL string
	client  *http.Client
}

type DNSState struct {
	Owned             bool
	Conflicting       bool
	ForeignTunnel     bool
	NonTunnelConflict bool
	RecordIDs         []string
}

type apiEnvelope struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

type dnsRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

func FindOriginCert() string {
	if explicit := strings.TrimSpace(firstNonEmpty(os.Getenv("CFDEV_ORIGIN_CERT"), os.Getenv("TUNNEL_ORIGIN_CERT"))); explicit != "" {
		return explicit
	}
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".cloudflared", "cert.pem"),
		filepath.Join(home, ".cloudflare-warp", "cert.pem"),
		filepath.Join(home, "cloudflare-warp", "cert.pem"),
		filepath.FromSlash("/etc/cloudflared/cert.pem"),
		filepath.FromSlash("/usr/local/etc/cloudflared/cert.pem"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return candidates[0]
}

func DefaultOriginCertPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cloudflared", "cert.pem")
}

func ReadOriginCert(certPath string) (OriginCert, error) {
	contents, err := os.ReadFile(certPath)
	if err != nil {
		failureErr := failure.Wrap("AUTH_NOT_FOUND", "Cloudflare browser authentication was not found", failure.ExitConfig, err)
		failureErr.Hint = "Run `cfdev setup` once to authenticate in your browser."
		return OriginCert{}, failureErr
	}
	var cert OriginCert
	remaining := contents
	for {
		block, rest := pem.Decode(remaining)
		if block == nil {
			break
		}
		remaining = rest
		if block.Type != "ARGO TUNNEL TOKEN" {
			continue
		}
		if cert.APIToken != "" {
			return OriginCert{}, invalidAuth("the certificate contains multiple tunnel tokens")
		}
		if err := json.Unmarshal(block.Bytes, &cert); err != nil {
			return OriginCert{}, invalidAuth("the tunnel token could not be decoded")
		}
	}
	if cert.ZoneID == "" || cert.AccountID == "" || cert.APIToken == "" {
		return OriginCert{}, invalidAuth("the certificate is missing its zone, account, or token")
	}
	return cert, nil
}

func NewAPI(cert OriginCert) *API {
	baseURL := "https://api.cloudflare.com/client/v4"
	if override := strings.TrimSpace(os.Getenv("CFDEV_API_URL")); override != "" {
		baseURL = strings.TrimRight(override, "/")
	} else if strings.EqualFold(cert.Endpoint, "fed") {
		baseURL = "https://api.fed.cloudflare.com/client/v4"
	}
	return &API{
		cert:    cert,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (api *API) ZoneName(ctx context.Context) (string, error) {
	var result struct {
		Name string `json:"name"`
	}
	if err := api.get(ctx, "/zones/"+url.PathEscape(api.cert.ZoneID), &result); err != nil {
		return "", err
	}
	if result.Name == "" {
		return "", failure.New("ZONE_DISCOVERY_FAILED", "Cloudflare returned no domain for the selected zone", failure.ExitConfig)
	}
	return strings.ToLower(result.Name), nil
}

func (api *API) DNSState(ctx context.Context, hostname, tunnelID string) (DNSState, error) {
	query := url.Values{}
	query.Set("name", hostname)
	var records []dnsRecord
	if err := api.get(ctx, "/zones/"+url.PathEscape(api.cert.ZoneID)+"/dns_records?"+query.Encode(), &records); err != nil {
		return DNSState{}, err
	}
	expected := strings.ToLower(tunnelID + ".cfargotunnel.com")
	state := DNSState{}
	for _, record := range records {
		if !strings.EqualFold(strings.TrimSuffix(record.Name, "."), strings.TrimSuffix(hostname, ".")) {
			continue
		}
		if strings.EqualFold(record.Type, "CNAME") && strings.EqualFold(strings.TrimSuffix(record.Content, "."), expected) {
			state.Owned = true
			state.RecordIDs = append(state.RecordIDs, record.ID)
		} else {
			state.Conflicting = true
			if strings.EqualFold(record.Type, "CNAME") && isTunnelTarget(record.Content) {
				state.ForeignTunnel = true
			} else {
				state.NonTunnelConflict = true
			}
		}
	}
	return state, nil
}

func isTunnelTarget(value string) bool {
	value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	prefix := strings.TrimSuffix(value, ".cfargotunnel.com")
	return prefix != value && prefix != ""
}

func (api *API) DeleteOwnedDNS(ctx context.Context, hostname, tunnelID string) (bool, error) {
	state, err := api.DNSState(ctx, hostname, tunnelID)
	if err != nil {
		return false, err
	}
	if !state.Owned {
		return false, nil
	}
	for _, recordID := range state.RecordIDs {
		if err := api.delete(ctx, "/zones/"+url.PathEscape(api.cert.ZoneID)+"/dns_records/"+url.PathEscape(recordID)); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (api *API) get(ctx context.Context, endpoint string, result any) error {
	envelope, err := api.request(ctx, http.MethodGet, endpoint)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return failure.Wrap("CLOUDFLARE_RESPONSE_INVALID", "Cloudflare returned an unexpected response", failure.ExitGeneral, err)
	}
	return nil
}

func (api *API) delete(ctx context.Context, endpoint string) error {
	_, err := api.request(ctx, http.MethodDelete, endpoint)
	return err
}

func (api *API) request(ctx context.Context, method, endpoint string) (apiEnvelope, error) {
	req, err := http.NewRequestWithContext(ctx, method, api.baseURL+endpoint, nil)
	if err != nil {
		return apiEnvelope{}, err
	}
	req.Header.Set("Authorization", "Bearer "+api.cert.APIToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "cfdev/0.1")
	response, err := api.client.Do(req)
	if err != nil {
		failureErr := failure.Wrap("CLOUDFLARE_UNREACHABLE", "could not reach Cloudflare", failure.ExitGeneral, err)
		failureErr.Hint = "Check your internet connection and try again."
		return apiEnvelope{}, failureErr
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return apiEnvelope{}, err
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(contents, &envelope); err != nil {
		return apiEnvelope{}, failure.Wrap("CLOUDFLARE_RESPONSE_INVALID", "Cloudflare returned an unexpected response", failure.ExitGeneral, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !envelope.Success {
		reason := http.StatusText(response.StatusCode)
		if len(envelope.Errors) > 0 && envelope.Errors[0].Message != "" {
			reason = envelope.Errors[0].Message
		}
		code := "CLOUDFLARE_API_ERROR"
		exitCode := failure.ExitGeneral
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			code = "AUTH_EXPIRED"
			exitCode = failure.ExitConfig
		}
		failureErr := failure.New(code, fmt.Sprintf("Cloudflare rejected the request: %s", reason), exitCode)
		failureErr.Hint = "Run `cfdev setup --force` if your browser authorization has expired."
		return apiEnvelope{}, failureErr
	}
	return envelope, nil
}

func invalidAuth(reason string) error {
	err := failure.New("AUTH_INVALID", "the Cloudflare browser certificate is invalid: "+reason, failure.ExitConfig)
	err.Hint = "Run `cloudflared tunnel login`, then try again."
	return err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
