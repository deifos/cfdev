package cloudflared

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/deifos/cfdev/internal/config"
	"github.com/deifos/cfdev/internal/failure"
)

type Client struct {
	Binary string
}

type Tunnel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	DeletedAt any    `json:"deletedAt"`
}

type Result struct {
	Stdout string
	Stderr string
	Err    error
}

func Find(paths config.Paths) (*Client, error) {
	if override := strings.TrimSpace(os.Getenv("CFDEV_CLOUDFLARED")); override != "" {
		if filepath.IsAbs(override) {
			if info, err := os.Stat(override); err == nil && !info.IsDir() {
				return &Client{Binary: override}, nil
			}
		} else if found, err := exec.LookPath(override); err == nil {
			return &Client{Binary: found}, nil
		}
		return nil, notFound()
	}
	if info, err := os.Stat(paths.ManagedBin); err == nil && !info.IsDir() {
		return &Client{Binary: paths.ManagedBin}, nil
	}
	if found, err := exec.LookPath("cloudflared"); err == nil {
		return &Client{Binary: found}, nil
	}
	return nil, notFound()
}

func (client *Client) Version(ctx context.Context) (string, error) {
	result := client.Run(ctx, "--version")
	if result.Err != nil {
		return "", commandError("could not run cloudflared", result)
	}
	return strings.TrimSpace(firstNonEmpty(result.Stdout, result.Stderr)), nil
}

func (client *Client) Login(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
	command := exec.CommandContext(ctx, client.Binary, "tunnel", "login")
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		failureErr := failure.Wrap("AUTH_FAILED", "Cloudflare browser authentication did not complete", failure.ExitConfig, err)
		failureErr.Hint = "Run `cloudflared tunnel login` directly for more detail, then retry `cfdev init`."
		return failureErr
	}
	return nil
}

func (client *Client) ListTunnels(ctx context.Context, certPath, name string) ([]Tunnel, error) {
	result := client.Run(ctx, "tunnel", "--origincert", certPath, "list", "--name", name, "--output", "json")
	if result.Err != nil {
		return nil, commandError("could not list Cloudflare tunnels", result)
	}
	var tunnels []Tunnel
	if err := json.Unmarshal([]byte(result.Stdout), &tunnels); err != nil {
		return nil, outputError("tunnel list", err)
	}
	active := make([]Tunnel, 0, len(tunnels))
	for _, tunnel := range tunnels {
		if tunnel.Name == name {
			active = append(active, tunnel)
		}
	}
	return active, nil
}

func (client *Client) CreateTunnel(ctx context.Context, certPath, name string) (Tunnel, error) {
	result := client.Run(ctx, "tunnel", "--origincert", certPath, "create", "--output", "json", name)
	if result.Err != nil {
		return Tunnel{}, commandError("could not create the Cloudflare tunnel", result)
	}
	var tunnel Tunnel
	if err := json.Unmarshal([]byte(result.Stdout), &tunnel); err != nil {
		return Tunnel{}, outputError("tunnel creation", err)
	}
	if tunnel.ID == "" {
		return Tunnel{}, outputError("tunnel creation", fmt.Errorf("missing tunnel ID"))
	}
	return tunnel, nil
}

func (client *Client) RouteDNS(ctx context.Context, certPath, tunnelID, hostname string, force bool) error {
	args := []string{"tunnel", "--origincert", certPath, "route", "dns"}
	if force {
		args = append(args, "--overwrite-dns")
	}
	args = append(args, tunnelID, hostname)
	result := client.Run(ctx, args...)
	if result.Err != nil {
		failureErr := commandError("could not create the DNS route", result)
		failureErr.Hint = "Check for an existing DNS record, or retry intentionally with `--force`."
		return failureErr
	}
	return nil
}

func (client *Client) ValidateIngress(ctx context.Context, ingressPath string) error {
	result := client.Run(ctx, "tunnel", "--config", ingressPath, "ingress", "validate")
	if result.Err != nil {
		return commandError("the generated ingress rules are invalid", result)
	}
	return nil
}

func (client *Client) Run(ctx context.Context, args ...string) Result {
	var stdout, stderr bytes.Buffer
	command := exec.CommandContext(ctx, client.Binary, args...)
	command.Stdout = &stdout
	command.Stderr = &stderr
	command.Env = os.Environ()
	err := command.Run()
	return Result{Stdout: stdout.String(), Stderr: stderr.String(), Err: err}
}

func CredentialsPath(certPath, tunnelID string) string {
	return filepath.Join(filepath.Dir(certPath), tunnelID+".json")
}

func commandError(message string, result Result) *failure.Error {
	detail := cleanOutput(firstNonEmpty(result.Stderr, result.Stdout))
	if detail != "" {
		message += ": " + detail
	}
	err := failure.Wrap("CLOUDFLARED_ERROR", message, failure.ExitGeneral, result.Err)
	err.Hint = "Run `cfdev doctor` to check browser authentication and local configuration."
	return err
}

func outputError(operation string, cause error) error {
	err := failure.Wrap("CLOUDFLARED_OUTPUT_ERROR", "could not understand cloudflared's "+operation+" response", failure.ExitGeneral, cause)
	err.Hint = "Update cloudflared and try again."
	return err
}

func cleanOutput(value string) string {
	lines := strings.FieldsFunc(value, func(char rune) bool { return char == '\n' || char == '\r' })
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			clean = append(clean, trimmed)
		}
	}
	if len(clean) > 4 {
		clean = clean[len(clean)-4:]
	}
	result := strings.Join(clean, " ")
	if len(result) > 600 {
		result = result[:600]
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func notFound() error {
	err := failure.New("CLOUDFLARED_NOT_FOUND", "cloudflared is required but was not found", failure.ExitDependency)
	err.Hint = "Run `cfdev init` to install a managed copy."
	return err
}

func ManagementContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}
