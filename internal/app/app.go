package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/deifos/cfdev/internal/cli"
	"github.com/deifos/cfdev/internal/cloudflare"
	"github.com/deifos/cfdev/internal/cloudflared"
	"github.com/deifos/cfdev/internal/config"
	"github.com/deifos/cfdev/internal/failure"
	processmanager "github.com/deifos/cfdev/internal/process"
	"github.com/deifos/cfdev/internal/ui"
	"github.com/deifos/cfdev/internal/updater"
)

type App struct {
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
	CWD    string
	reader *bufio.Reader
}

type result struct {
	Data     any
	Summary  string
	ExitCode int
}

type mappingView struct {
	Subdomain      string `json:"subdomain"`
	Hostname       string `json:"hostname"`
	URL            string `json:"url"`
	Port           int    `json:"port"`
	Protocol       string `json:"protocol"`
	LocalURL       string `json:"local_url"`
	LocalReachable bool   `json:"local_reachable"`
}

type doctorCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Level  string `json:"level"`
}

func New(in io.Reader, out, errOut io.Writer, cwd string) *App {
	return &App{In: in, Out: out, Err: errOut, CWD: cwd, reader: bufio.NewReader(in)}
}

func (app *App) Run(ctx context.Context, args []string) int {
	invocation, err := cli.Parse(args)
	if err != nil {
		fallbackOptions := cli.Options{}
		for _, arg := range args {
			if arg == "--json" {
				fallbackOptions.JSON = true
			}
			if arg == "--quiet" || arg == "-q" {
				fallbackOptions.Quiet = true
			}
		}
		fallback := ui.New(app.Out, app.Err, fallbackOptions)
		typed := failure.As(err)
		fallback.Error(typed)
		return typed.ExitCode
	}
	view := ui.New(app.Out, app.Err, invocation.Options)
	if invocation.Options.Help && invocation.Command != "help" && invocation.Command != "dashboard" {
		view.Line(helpText(invocation.Command))
		return failure.ExitOK
	}
	res, err := app.execute(ctx, invocation, view)
	if err != nil {
		typed := failure.As(err)
		view.Error(typed)
		return typed.ExitCode
	}
	view.Result(res.ExitCode == failure.ExitOK, res.Data, res.Summary)
	return res.ExitCode
}

func (app *App) execute(ctx context.Context, inv cli.Invocation, view *ui.UI) (result, error) {
	switch inv.Command {
	case "help":
		if inv.Options.JSON {
			return result{Data: map[string]any{"help": helpText("")}, Summary: "Help"}, nil
		}
		view.Line(helpText(""))
		return result{}, nil
	case "version":
		if inv.Options.JSON {
			return result{Data: map[string]any{"version": Version}, Summary: "cfdev " + Version}, nil
		}
		view.Line("cfdev " + Version)
		return result{}, nil
	case "dashboard", "tui":
		return app.dashboard(view)
	case "init":
		return app.init(ctx, inv, view)
	case "add":
		return app.add(ctx, inv, view)
	case "claim":
		inv.Options.Claim = true
		return app.add(ctx, inv, view)
	case "remove":
		return app.remove(ctx, inv, view)
	case "clear":
		inv.Options.All = true
		return app.remove(ctx, inv, view)
	case "list":
		return app.list(inv, view, true)
	case "up":
		return app.up(ctx, inv, view)
	case "down":
		return app.down(inv, view)
	case "status":
		return app.status(inv, view)
	case "open":
		return app.open(inv)
	case "config":
		return app.configCommand(inv, view)
	case "doctor":
		return app.doctor(ctx, inv, view)
	case "upgrade":
		return app.upgrade(ctx, inv, view)
	case "shortcut":
		return app.shortcut(ctx, inv, view)
	default:
		err := failure.New("INVALID_USAGE", "unknown command: "+inv.Command, failure.ExitUsage)
		err.Hint = "Run `cfdev --help` to see available commands."
		if strings.Contains(inv.Command, ".") {
			firstLabel := strings.SplitN(inv.Command, ".", 2)[0]
			if name, nameErr := config.NormalizeSubdomain(firstLabel); nameErr == nil {
				err.Message = fmt.Sprintf("%q looks like a hostname, not a command", inv.Command)
				err.Hint = fmt.Sprintf("To remove it, run `cfdev remove %s`. To open it, run `cfdev open %s`.", name, name)
			}
		}
		return result{}, err
	}
}

func (app *App) init(ctx context.Context, inv cli.Invocation, view *ui.UI) (result, error) {
	if len(inv.Args) > 1 {
		return result{}, usage("`cfdev init` accepts at most one domain", "Try `cfdev init example.com`.")
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return result{}, err
	}
	existing, err := config.LoadOptional(paths)
	if err != nil {
		return result{}, err
	}
	if existing != nil && !inv.Options.Force {
		return result{Data: publicConfig(paths, existing), Summary: "Already ready on " + existing.Domain + "."}, nil
	}

	client, err := cloudflared.Find(paths)
	if err != nil {
		typed := failure.As(err)
		if typed.Code != "CLOUDFLARED_NOT_FOUND" {
			return result{}, err
		}
		if inv.Options.JSON && !inv.Options.Yes {
			typed.Data = map[string]any{"retry_command": "cfdev init --yes --json"}
			typed.Hint = "Retry with `--yes` to install a verified managed copy."
			return result{}, typed
		}
		if !inv.Options.Yes {
			approved, promptErr := app.confirm("cloudflared is required. Install a managed copy?", true)
			if promptErr != nil {
				return result{}, promptErr
			}
			if !approved {
				return result{}, err
			}
		}
		progress := view.Progress("Downloading a verified cloudflared release…")
		installContext, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		var release string
		client, release, err = cloudflared.Install(installContext, paths)
		progress.Stop()
		if err != nil {
			return result{}, err
		}
		if !inv.Options.JSON {
			view.Success("Installed cloudflared " + release)
		}
	}
	managementContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	version, err := client.Version(managementContext)
	cancel()
	if err != nil {
		return result{}, err
	}
	if !inv.Options.JSON {
		view.Success(friendlyCloudflaredVersion(version) + " is ready")
	}

	certPath := cloudflare.FindOriginCert()
	if _, statErr := os.Stat(certPath); statErr != nil {
		if inv.Options.JSON {
			authErr := failure.New("AUTH_REQUIRED", "Cloudflare browser authentication is required", failure.ExitConfig)
			authErr.Hint = "Run `cfdev init` once to authenticate in your browser."
			authErr.Data = map[string]any{"interactive_command": "cfdev init"}
			return result{}, authErr
		}
		view.Info("Opening Cloudflare in your browser…")
		if err := client.Login(ctx, app.In, app.Out, app.Err); err != nil {
			return result{}, err
		}
		certPath = cloudflare.FindOriginCert()
	}
	cert, err := cloudflare.ReadOriginCert(certPath)
	if err != nil {
		return result{}, err
	}
	if !inv.Options.JSON {
		view.Success("Cloudflare authenticated")
	}

	domain := ""
	if len(inv.Args) == 1 {
		domain, err = config.NormalizeDomain(inv.Args[0])
	} else {
		discoveryContext, cancel := context.WithTimeout(ctx, 15*time.Second)
		domain, err = cloudflare.NewAPI(cert).ZoneName(discoveryContext)
		cancel()
		if err != nil && !inv.Options.JSON {
			domain, err = app.prompt("Domain to use")
		}
		if err == nil {
			domain, err = config.NormalizeDomain(domain)
		}
	}
	if err != nil {
		if inv.Options.JSON {
			domainErr := failure.New("DOMAIN_REQUIRED", "the selected Cloudflare domain could not be discovered", failure.ExitConfig)
			domainErr.Hint = "Retry with an explicit domain, such as `cfdev init example.com --yes --json`."
			domainErr.Data = map[string]any{"retry_command": "cfdev init example.com --yes --json"}
			return result{}, domainErr
		}
		return result{}, err
	}
	if !inv.Options.JSON {
		view.Success("Using " + domain)
	}

	machineID, tunnelName := "", ""
	if existing != nil && existing.MachineID != "" && existing.TunnelName != "" {
		machineID, tunnelName = existing.MachineID, existing.TunnelName
	} else {
		machineID, tunnelName, err = config.MachineIdentity(paths)
		if err != nil {
			return result{}, err
		}
	}
	tunnelProgress := view.Progress("Preparing this machine's permanent tunnel…")
	defer tunnelProgress.Stop()
	managementContext, cancel = context.WithTimeout(ctx, 30*time.Second)
	tunnels, err := client.ListTunnels(managementContext, certPath, tunnelName)
	cancel()
	if err != nil {
		return result{}, err
	}
	var tunnel cloudflared.Tunnel
	created := false
	if len(tunnels) > 0 {
		tunnel = tunnels[0]
	} else {
		managementContext, cancel = context.WithTimeout(ctx, 30*time.Second)
		tunnel, err = client.CreateTunnel(managementContext, certPath, tunnelName)
		cancel()
		if err != nil {
			return result{}, err
		}
		created = true
	}
	credentialsPath := cloudflared.CredentialsPath(certPath, tunnel.ID)
	if existing != nil && existing.TunnelID == tunnel.ID && fileExists(existing.CredentialsFile) {
		credentialsPath = existing.CredentialsFile
	}
	if !fileExists(credentialsPath) {
		missing := failure.New("TUNNEL_CREDENTIALS_MISSING", fmt.Sprintf("the tunnel %q exists, but its credential file is not on this machine", tunnelName), failure.ExitConfig)
		missing.Hint = "Restore the credential file or remove the unusable tunnel in Cloudflare before retrying."
		return result{}, missing
	}

	cfg := &config.Config{
		Version: 1, Domain: domain, TunnelName: tunnelName, TunnelID: tunnel.ID,
		CredentialsFile: credentialsPath, MachineID: machineID, Mappings: []config.Mapping{},
	}
	if existing != nil {
		cfg.Mappings = existing.Mappings
		cfg.Preferences = existing.Preferences
	}
	if err := config.Save(paths, cfg); err != nil {
		return result{}, err
	}
	tunnelProgress.Stop()
	if !inv.Options.JSON {
		verb := "Using"
		if created {
			verb = "Created"
		}
		view.Success(verb + " this machine's permanent tunnel")
	}
	data := publicConfig(paths, cfg)
	data["authenticated"] = true
	data["tunnel_created"] = created
	return result{Data: data, Summary: "Ready. Permanent project URLs will use *." + domain + "."}, nil
}

func (app *App) add(ctx context.Context, inv cli.Invocation, view *ui.UI) (result, error) {
	example := "cfdev add my-app 3000"
	if inv.Options.Claim {
		example = "cfdev claim my-app 3000"
	}
	if err := cli.RequireArgs(inv, 2, example); err != nil {
		return result{}, err
	}
	port, err := strconv.Atoi(inv.Args[1])
	if err != nil || config.ValidatePort(port) != nil {
		return result{}, failure.WithHint(failure.New("INVALID_PORT", fmt.Sprintf("%q is not a valid port", inv.Args[1]), failure.ExitUsage), "Use a port from 1 to 65535, such as 3000.")
	}
	paths, cfg, client, api, certPath, err := app.ready()
	if err != nil {
		return result{}, err
	}
	subdomain, err := config.NormalizeProjectName(inv.Args[0], cfg.Domain)
	if err != nil {
		return result{}, err
	}
	existingIndex := -1
	for index, mapping := range cfg.Mappings {
		if mapping.Subdomain == subdomain {
			existingIndex = index
			if mapping.Port != port && !inv.Options.Force && !inv.Options.Claim {
				conflict := failure.New("MAPPING_EXISTS", fmt.Sprintf("%s already points to localhost:%d", subdomain, mapping.Port), failure.ExitConflict)
				conflict.Hint = fmt.Sprintf("Use a different name for another app, or `cfdev claim %s %d` to move this URL intentionally.", subdomain, port)
				return result{}, conflict
			}
			break
		}
	}
	hostname := subdomain + "." + cfg.Domain
	progress := view.Progress("Checking " + hostname + "…")
	defer progress.Stop()
	managementContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	dnsState, err := api.DNSState(managementContext, hostname, cfg.TunnelID)
	cancel()
	if err != nil {
		return result{}, err
	}
	claimable := inv.Options.Claim && dnsState.ForeignTunnel && !dnsState.NonTunnelConflict
	if dnsState.Conflicting && !inv.Options.Force && !claimable {
		conflict := failure.New("DNS_CONFLICT", hostname+" already has a different DNS record", failure.ExitConflict)
		if dnsState.ForeignTunnel && !dnsState.NonTunnelConflict {
			conflict.Hint = fmt.Sprintf("Run `cfdev claim %s %d` to move this project URL to this machine.", subdomain, port)
		} else {
			conflict.Hint = "Inspect the unrelated record in Cloudflare, or replace it intentionally with `--force`."
		}
		return result{}, conflict
	}
	dnsCreated := false
	if !dnsState.Owned || dnsState.Conflicting {
		message := "Claiming " + hostname + "…"
		if claimable {
			message = "Moving " + hostname + " to this machine…"
		}
		progress.Update(message)
		managementContext, cancel = context.WithTimeout(ctx, 30*time.Second)
		err = client.RouteDNS(managementContext, certPath, cfg.TunnelID, hostname, inv.Options.Force || claimable)
		cancel()
		if err != nil {
			return result{}, err
		}
		dnsCreated = true
	}
	if existingIndex >= 0 && cfg.Mappings[existingIndex].Port == port && dnsState.Owned && !dnsState.Conflicting {
		mapping := cfg.Mappings[existingIndex]
		data := mappingData(cfg, mapping, localReachable(port))
		data["dns_created"] = false
		data["tunnel_restarted"] = false
		data["changed"] = false
		return result{Data: data, Summary: fmt.Sprintf("https://%s already points to localhost:%d.", hostname, port)}, nil
	}
	progress.Update("Updating local routing…")
	mapping := config.Mapping{Subdomain: subdomain, Port: port, Protocol: "http", CreatedAt: time.Now().UTC()}
	identical := false
	if existingIndex >= 0 {
		mapping.CreatedAt = cfg.Mappings[existingIndex].CreatedAt
		identical = cfg.Mappings[existingIndex].Port == port
		cfg.Mappings[existingIndex] = mapping
	} else {
		cfg.Mappings = append(cfg.Mappings, mapping)
	}
	if err := config.Save(paths, cfg); err != nil {
		if dnsCreated && !dnsState.Conflicting {
			rollbackContext, rollbackCancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, _ = api.DeleteOwnedDNS(rollbackContext, hostname, cfg.TunnelID)
			rollbackCancel()
		}
		return result{}, err
	}
	manager := processmanager.Manager{Paths: paths, Client: client}
	progress.Update("Reloading the tunnel…")
	restarted, err := manager.RestartBackground(cfg)
	if err != nil {
		return result{}, err
	}
	reachable := localReachable(port)
	data := mappingData(cfg, mapping, reachable)
	data["dns_created"] = dnsCreated
	data["tunnel_restarted"] = restarted
	data["changed"] = !identical || dnsCreated
	data["claimed"] = claimable
	progress.Stop()
	summary := fmt.Sprintf("Added https://%s → localhost:%d", hostname, port)
	if claimable {
		summary = fmt.Sprintf("Claimed https://%s on this machine → localhost:%d", hostname, port)
	}
	if identical && !dnsCreated {
		summary = fmt.Sprintf("https://%s already points to localhost:%d.", hostname, port)
	}
	if !inv.Options.JSON && !reachable {
		view.Warning(fmt.Sprintf("Nothing is listening on localhost:%d yet.", port))
	}
	status := manager.Status()
	if !inv.Options.JSON && status.Running && status.Mode == "foreground" {
		view.Warning("Restart the foreground tunnel to load this mapping.")
	}
	return result{Data: data, Summary: summary}, nil
}

func (app *App) remove(ctx context.Context, inv cli.Invocation, view *ui.UI) (result, error) {
	if inv.Options.All {
		if len(inv.Args) != 0 {
			return result{}, usage("bulk removal accepts no subdomain", "Use `cfdev clear` or `cfdev remove --all`.")
		}
		return app.removeAll(ctx, inv, view)
	}
	if err := cli.RequireArgs(inv, 1, "cfdev remove my-app"); err != nil {
		return result{}, err
	}
	paths, cfg, client, api, _, err := app.ready()
	if err != nil {
		return result{}, err
	}
	subdomain, err := config.NormalizeProjectName(inv.Args[0], cfg.Domain)
	if err != nil {
		return result{}, err
	}
	hostname := subdomain + "." + cfg.Domain
	progress := view.Progress("Removing " + hostname + "…")
	defer progress.Stop()
	managementContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	dnsRemoved, dnsErr := api.DeleteOwnedDNS(managementContext, hostname, cfg.TunnelID)
	cancel()
	if dnsErr != nil && !inv.Options.Force {
		return result{}, dnsErr
	}
	warning := ""
	if dnsErr != nil {
		warning = "DNS cleanup failed; --force will still remove the local mapping."
	}
	removed := false
	next := make([]config.Mapping, 0, len(cfg.Mappings))
	for _, mapping := range cfg.Mappings {
		if mapping.Subdomain == subdomain {
			removed = true
			continue
		}
		next = append(next, mapping)
	}
	cfg.Mappings = next
	if removed {
		progress.Update("Updating local routing…")
		if err := config.Save(paths, cfg); err != nil {
			return result{}, err
		}
	}
	manager := processmanager.Manager{Paths: paths, Client: client}
	restarted := false
	if removed {
		progress.Update("Reloading the tunnel…")
		restarted, err = manager.RestartBackground(cfg)
		if err != nil {
			return result{}, err
		}
	}
	progress.Stop()
	if warning != "" && !inv.Options.JSON {
		view.Warning(warning)
	}
	status := manager.Status()
	if removed && !inv.Options.JSON && status.Running && status.Mode == "foreground" {
		view.Warning("Restart the foreground tunnel to unload this mapping.")
	}
	summary := "No cfdev mapping or DNS record exists for " + hostname + "."
	if removed || dnsRemoved {
		summary = "Removed " + hostname + "."
	}
	return result{Data: map[string]any{
		"subdomain": subdomain, "hostname": hostname, "mapping_removed": removed,
		"dns_removed": dnsRemoved, "tunnel_restarted": restarted,
	}, Summary: summary}, nil
}

func (app *App) removeAll(ctx context.Context, inv cli.Invocation, view *ui.UI) (result, error) {
	paths, cfg, client, api, _, err := app.ready()
	if err != nil {
		return result{}, err
	}
	if len(cfg.Mappings) == 0 {
		return result{Data: map[string]any{"removed": []string{}, "dns_removed": 0, "tunnel_stopped": false}, Summary: "No cfdev project hostnames exist."}, nil
	}
	if !inv.Options.Yes {
		if inv.Options.JSON {
			required := failure.New("CONFIRMATION_REQUIRED", fmt.Sprintf("removing all %d project hostnames requires confirmation", len(cfg.Mappings)), failure.ExitConfig)
			required.Hint = "Retry with `cfdev clear --yes --json`."
			required.Data = map[string]any{"retry_command": "cfdev clear --yes --json", "mapping_count": len(cfg.Mappings)}
			return result{}, required
		}
		approved, promptErr := app.confirm(fmt.Sprintf("Remove all %d cfdev project hostnames from %s?", len(cfg.Mappings), cfg.Domain), false)
		if promptErr != nil {
			return result{}, promptErr
		}
		if !approved {
			return result{Data: map[string]any{"removed": []string{}, "dns_removed": 0, "tunnel_stopped": false}, Summary: "Cancelled; no hostnames were removed."}, nil
		}
	}

	removed := make([]string, 0, len(cfg.Mappings))
	dnsRemoved := 0
	warnings := make([]string, 0)
	label := "project URLs"
	if len(cfg.Mappings) == 1 {
		label = "project URL"
	}
	progress := view.Progress(fmt.Sprintf("Removing %d %s…", len(cfg.Mappings), label))
	defer progress.Stop()
	for index, mapping := range cfg.Mappings {
		hostname := mapping.Subdomain + "." + cfg.Domain
		progress.Update(fmt.Sprintf("Removing %s (%d/%d)…", hostname, index+1, len(cfg.Mappings)))
		managementContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		deleted, deleteErr := api.DeleteOwnedDNS(managementContext, hostname, cfg.TunnelID)
		cancel()
		if deleteErr != nil && !inv.Options.Force {
			return result{}, deleteErr
		}
		if deleteErr != nil {
			warnings = append(warnings, "DNS cleanup failed for "+hostname+"; removing its local mapping because --force was supplied.")
		}
		if deleted {
			dnsRemoved++
		}
		removed = append(removed, hostname)
	}
	cfg.Mappings = nil
	progress.Update("Updating local routing…")
	if err := config.Save(paths, cfg); err != nil {
		return result{}, err
	}
	manager := processmanager.Manager{Paths: paths, Client: client}
	progress.Update("Stopping the tunnel…")
	stopped, err := manager.Stop()
	if err != nil {
		return result{}, err
	}
	progress.Stop()
	if !inv.Options.JSON {
		for _, warning := range warnings {
			view.Warning(warning)
		}
	}
	return result{Data: map[string]any{
		"removed": removed, "dns_removed": dnsRemoved, "tunnel_stopped": stopped,
	}, Summary: fmt.Sprintf("Removed %d %s from %s.", len(removed), label, cfg.Domain)}, nil
}

func (app *App) list(inv cli.Invocation, view *ui.UI, heading bool) (result, error) {
	if len(inv.Args) != 0 {
		return result{}, usage("`cfdev list` accepts no arguments", "Try `cfdev list`.")
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return result{}, err
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return result{}, err
	}
	client, err := cloudflared.Find(paths)
	if err != nil {
		return result{}, err
	}
	manager := processmanager.Manager{Paths: paths, Client: client}
	status := manager.Status()
	mappings := buildMappingViews(cfg)
	data := map[string]any{"domain": cfg.Domain, "tunnel": status, "mappings": mappings}
	if inv.Options.JSON {
		return result{Data: data, Summary: fmt.Sprintf("%d mapping(s)", len(mappings))}, nil
	}
	app.renderMappings(view, cfg, status, mappings, heading)
	return result{}, nil
}

func (app *App) up(ctx context.Context, inv cli.Invocation, view *ui.UI) (result, error) {
	if len(inv.Args) != 0 {
		return result{}, usage("`cfdev up` accepts no arguments", "Try `cfdev up -d`.")
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return result{}, err
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return result{}, err
	}
	if len(cfg.Mappings) == 0 {
		empty := failure.New("NO_MAPPINGS", "there are no project mappings to serve", failure.ExitConfig)
		empty.Hint = "Run `cfdev add <name> <port>` first."
		return result{}, empty
	}
	client, err := cloudflared.Find(paths)
	if err != nil {
		return result{}, err
	}
	if err := config.WriteIngress(paths, cfg); err != nil {
		return result{}, err
	}
	managementContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = client.ValidateIngress(managementContext, paths.Ingress)
	cancel()
	if err != nil {
		return result{}, err
	}
	if inv.Options.JSON && !inv.Options.Detach {
		return result{}, usage("`cfdev up --json` requires --detach", "Use `cfdev up -d --json` so the command can return structured output.")
	}
	manager := processmanager.Manager{Paths: paths, Client: client}
	if inv.Options.Detach {
		progress := view.Progress("Starting the tunnel in the background…")
		defer progress.Stop()
		transitioned := false
		if current := manager.Status(); current.Running && current.Mode == "foreground" {
			progress.Update("Moving the tunnel to the background…")
			if _, err := manager.Stop(); err != nil {
				return result{}, err
			}
			transitioned = true
		}
		status, alreadyRunning, err := manager.StartBackground(cfg)
		if err != nil {
			return result{}, err
		}
		progress.Stop()
		summary := fmt.Sprintf("Tunnel is running in the background (PID %d).", status.PID)
		if alreadyRunning {
			summary = fmt.Sprintf("Tunnel is already running (PID %d).", status.PID)
		}
		if transitioned {
			summary = fmt.Sprintf("Tunnel moved to the background (PID %d).", status.PID)
		}
		return result{Data: map[string]any{"tunnel": status, "already_running": alreadyRunning, "transitioned_from_foreground": transitioned, "domain": cfg.Domain}, Summary: summary}, nil
	}
	progress := view.Progress("Starting the tunnel…")
	defer progress.Stop()
	verbose := inv.Options.Verbose && !inv.Options.Quiet && !inv.Options.JSON
	status, alreadyRunning, err := manager.StartForeground(cfg, app.In, app.Out, app.Err, verbose, func(_ processmanager.Status) {
		progress.Stop()
		if !inv.Options.JSON {
			view.Success("Tunnel is running — press Ctrl+C to stop.")
		}
	})
	if err != nil {
		return result{}, err
	}
	progress.Stop()
	if alreadyRunning {
		return result{Data: map[string]any{"tunnel": status, "already_running": true}, Summary: fmt.Sprintf("Tunnel is already running (PID %d).", status.PID)}, nil
	}
	return result{Data: map[string]any{"tunnel": status, "already_running": false}, Summary: "Tunnel stopped."}, nil
}

func (app *App) down(inv cli.Invocation, view *ui.UI) (result, error) {
	if len(inv.Args) != 0 {
		return result{}, usage("`cfdev down` accepts no arguments", "Try `cfdev down`.")
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return result{}, err
	}
	if _, err := config.Load(paths); err != nil {
		return result{}, err
	}
	client, err := cloudflared.Find(paths)
	if err != nil {
		return result{}, err
	}
	manager := processmanager.Manager{Paths: paths, Client: client}
	progress := view.Progress("Stopping the tunnel…")
	defer progress.Stop()
	stopped, err := manager.Stop()
	if err != nil {
		return result{}, err
	}
	progress.Stop()
	summary := "Tunnel is already stopped."
	if stopped {
		summary = "Tunnel stopped."
	}
	return result{Data: map[string]any{"stopped": stopped}, Summary: summary}, nil
}

func (app *App) status(inv cli.Invocation, view *ui.UI) (result, error) {
	if len(inv.Args) != 0 {
		return result{}, usage("`cfdev status` accepts no arguments", "Try `cfdev status`.")
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return result{}, err
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return result{}, err
	}
	client, err := cloudflared.Find(paths)
	if err != nil {
		return result{}, err
	}
	tunnel := (processmanager.Manager{Paths: paths, Client: client}).Status()
	mappings := buildMappingViews(cfg)
	data := map[string]any{"domain": cfg.Domain, "tunnel": tunnel, "mappings": mappings}
	summary := "Tunnel is stopped."
	if tunnel.Running {
		summary = "Tunnel is running."
	}
	if inv.Options.JSON {
		return result{Data: data, Summary: summary}, nil
	}
	view.Heading("cfdev status")
	view.Line("")
	state := view.Dim("○ Stopped")
	if tunnel.Running {
		state = view.Green("● Running")
	}
	pid := ""
	if tunnel.PID > 0 {
		pid = fmt.Sprintf("  PID %d", tunnel.PID)
	}
	view.Line("  Tunnel  " + state + pid)
	view.Line("  Domain  " + cfg.Domain)
	listening := 0
	for _, mapping := range mappings {
		if mapping.LocalReachable {
			listening++
		}
	}
	view.Line(fmt.Sprintf("  Apps    %d/%d listening locally", listening, len(mappings)))
	return result{}, nil
}

func (app *App) open(inv cli.Invocation) (result, error) {
	if err := cli.RequireArgs(inv, 1, "cfdev open my-app"); err != nil {
		return result{}, err
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return result{}, err
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return result{}, err
	}
	subdomain, err := config.NormalizeProjectName(inv.Args[0], cfg.Domain)
	if err != nil {
		return result{}, err
	}
	found := false
	for _, mapping := range cfg.Mappings {
		if mapping.Subdomain == subdomain {
			found = true
			break
		}
	}
	if !found {
		missing := failure.New("MAPPING_NOT_FOUND", fmt.Sprintf("no mapping named %q exists", subdomain), failure.ExitConflict)
		missing.Hint = "Run `cfdev list` to see configured mappings."
		return result{}, missing
	}
	publicURL := "https://" + subdomain + "." + cfg.Domain
	if err := launchBrowser(publicURL); err != nil {
		return result{}, failure.Wrap("BROWSER_OPEN_FAILED", "could not open your browser", failure.ExitGeneral, err)
	}
	return result{Data: map[string]any{"subdomain": subdomain, "url": publicURL}, Summary: "Opened " + publicURL + "."}, nil
}

func (app *App) configCommand(inv cli.Invocation, view *ui.UI) (result, error) {
	if len(inv.Args) > 1 {
		return result{}, usage("`cfdev config` accepts `path` or `edit`", "Try `cfdev config path`.")
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return result{}, err
	}
	action := "show"
	if len(inv.Args) == 1 {
		action = inv.Args[0]
	}
	switch action {
	case "path":
		return result{Data: map[string]any{"config": paths.Config, "ingress": paths.Ingress, "log": paths.Log}, Summary: paths.Config}, nil
	case "show":
		cfg, err := config.Load(paths)
		if err != nil {
			return result{}, err
		}
		if inv.Options.JSON {
			return result{Data: publicConfig(paths, cfg), Summary: "Current cfdev config"}, nil
		}
		contents, _ := json.MarshalIndent(cfg, "", "  ")
		view.Line(string(contents))
		return result{}, nil
	case "edit":
		if _, err := config.Load(paths); err != nil {
			return result{}, err
		}
		editor := strings.TrimSpace(firstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR")))
		if editor == "" {
			if runtime.GOOS == "windows" {
				editor = "notepad"
			} else {
				editor = "vi"
			}
		}
		fields := strings.Fields(editor)
		command := exec.Command(fields[0], append(fields[1:], paths.Config)...)
		command.Stdin, command.Stdout, command.Stderr = app.In, app.Out, app.Err
		if err := command.Run(); err != nil {
			return result{}, failure.Wrap("EDITOR_FAILED", "the config editor did not complete", failure.ExitGeneral, err)
		}
		cfg, err := config.Load(paths)
		if err != nil {
			return result{}, err
		}
		if err := config.WriteIngress(paths, cfg); err != nil {
			return result{}, err
		}
		return result{Data: publicConfig(paths, cfg), Summary: "Config saved and tunnel rules regenerated."}, nil
	default:
		return result{}, usage("unknown config action: "+action, "Use `cfdev config`, `cfdev config path`, or `cfdev config edit`.")
	}
}

func (app *App) doctor(ctx context.Context, inv cli.Invocation, view *ui.UI) (result, error) {
	if len(inv.Args) != 0 {
		return result{}, usage("`cfdev doctor` accepts no arguments", "Try `cfdev doctor`.")
	}
	progress := view.Progress("Running diagnostics…")
	defer progress.Stop()
	paths, pathErr := config.ResolvePaths()
	checks := make([]doctorCheck, 0)
	if pathErr != nil {
		checks = append(checks, doctorCheck{Name: "paths", OK: false, Detail: pathErr.Error(), Level: "error"})
	}
	client, clientErr := cloudflared.Find(paths)
	if clientErr != nil {
		checks = append(checks, doctorCheck{Name: "cloudflared", OK: false, Detail: "not installed", Level: "error"})
	} else {
		versionContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		version, err := client.Version(versionContext)
		cancel()
		current := err == nil
		checkLevel := level(current)
		detail := firstNonEmpty(version, errorText(err))
		if current && cloudflaredOlderThanOneYear(version, time.Now()) {
			current = false
			checkLevel = "warning"
			detail += " (update recommended)"
		}
		checks = append(checks, doctorCheck{Name: "cloudflared", OK: current, Detail: detail, Level: checkLevel})
	}
	certPath := cloudflare.FindOriginCert()
	cert, certErr := cloudflare.ReadOriginCert(certPath)
	certDetail := certPath
	if certErr != nil {
		certDetail = errorText(certErr)
	}
	checks = append(checks, doctorCheck{Name: "browser_auth", OK: certErr == nil, Detail: certDetail, Level: level(certErr == nil)})
	cfg, cfgErr := config.Load(paths)
	cfgDetail := paths.Config
	if cfgErr != nil {
		cfgDetail = errorText(cfgErr)
	}
	checks = append(checks, doctorCheck{Name: "config", OK: cfgErr == nil, Detail: cfgDetail, Level: level(cfgErr == nil)})
	if cfgErr == nil {
		credentialOK := fileExists(cfg.CredentialsFile)
		checks = append(checks, doctorCheck{Name: "tunnel_credentials", OK: credentialOK, Detail: cfg.CredentialsFile, Level: level(credentialOK)})
		if err := config.WriteIngress(paths, cfg); err != nil {
			checks = append(checks, doctorCheck{Name: "ingress", OK: false, Detail: err.Error(), Level: "error"})
		} else if client != nil {
			validationContext, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := client.ValidateIngress(validationContext, paths.Ingress)
			cancel()
			checks = append(checks, doctorCheck{Name: "ingress", OK: err == nil, Detail: firstNonEmpty("valid", errorText(err)), Level: level(err == nil)})
		}
		if client != nil {
			status := (processmanager.Manager{Paths: paths, Client: client}).Status()
			detail := "stopped"
			if status.Running {
				detail = fmt.Sprintf("running (PID %d)", status.PID)
			}
			checks = append(checks, doctorCheck{Name: "process", OK: true, Detail: detail, Level: "info"})
		}
		if certErr == nil {
			api := cloudflare.NewAPI(cert)
			dnsOK := true
			dnsDetail := "all configured records point to this tunnel"
			for _, mapping := range cfg.Mappings {
				dnsContext, cancel := context.WithTimeout(ctx, 10*time.Second)
				state, err := api.DNSState(dnsContext, mapping.Subdomain+"."+cfg.Domain, cfg.TunnelID)
				cancel()
				if err != nil || !state.Owned {
					dnsOK = false
					dnsDetail = mapping.Subdomain + ": " + firstNonEmpty(errorText(err), "record missing or points elsewhere")
					break
				}
			}
			checks = append(checks, doctorCheck{Name: "dns", OK: dnsOK, Detail: dnsDetail, Level: level(dnsOK)})
		}
	}
	healthy := true
	for _, check := range checks {
		if !check.OK && check.Level == "error" {
			healthy = false
		}
	}
	progress.Stop()
	if !inv.Options.JSON {
		view.Heading("cfdev doctor")
		view.Line("")
		for _, check := range checks {
			mark := view.Green("✓")
			if check.Level == "warning" {
				mark = view.Yellow("!")
			} else if !check.OK {
				mark = "✗"
			}
			view.Line(fmt.Sprintf("  %s  %-20s %s", mark, check.Name, check.Detail))
		}
		view.Line("")
		if healthy {
			view.Success("Everything looks good.")
		} else {
			view.Warning("Fix the failed checks above, then run doctor again.")
		}
	}
	summary := "Everything looks good."
	exitCode := failure.ExitOK
	if !healthy {
		summary = "Some checks need attention."
		exitCode = failure.ExitGeneral
	}
	if !inv.Options.JSON {
		summary = ""
	}
	return result{Data: map[string]any{"checks": checks}, Summary: summary, ExitCode: exitCode}, nil
}

func (app *App) shortcut(ctx context.Context, inv cli.Invocation, view *ui.UI) (result, error) {
	if len(inv.Args) != 1 {
		return result{}, usage("the port shortcut accepts one port", "Try `cfdev 3000`.")
	}
	if inv.Options.JSON && !inv.Options.Detach {
		return result{}, usage("`cfdev <port> --json` requires --detach", "Use `cfdev 3000 -d --json`.")
	}
	subdomain := config.SuggestSubdomain(app.CWD)
	addInvocation := inv
	addInvocation.Command = "add"
	addInvocation.Options.Claim = true
	addInvocation.Args = []string{subdomain, inv.Args[0]}
	addResult, err := app.add(ctx, addInvocation, view)
	if err != nil {
		return result{}, err
	}
	if !inv.Options.JSON {
		view.Success(addResult.Summary)
	}
	upInvocation := inv
	upInvocation.Command = "up"
	upInvocation.Args = nil
	upResult, err := app.up(ctx, upInvocation, view)
	if err != nil {
		return result{}, err
	}
	return result{Data: map[string]any{"mapping": addResult.Data, "tunnel": upResult.Data}, Summary: upResult.Summary}, nil
}

func (app *App) dashboard(view *ui.UI) (result, error) {
	paths, err := config.ResolvePaths()
	if err != nil {
		return result{}, err
	}
	cfg, err := config.LoadOptional(paths)
	if err != nil {
		return result{}, err
	}
	if cfg == nil {
		if view.Options.JSON {
			return result{Data: map[string]any{"initialized": false}, Summary: "cfdev is not initialized"}, nil
		}
		view.Heading("cfdev " + Version)
		view.Line("")
		view.Line("  Permanent URLs for local projects on your Cloudflare domain.")
		view.Line("  One browser sign-in. No copied tokens. No tunnel configuration.")
		view.Line("")
		view.Info("Run `cfdev init` to get started.")
		return result{}, nil
	}
	inv := cli.Invocation{Command: "list", Options: view.Options}
	return app.list(inv, view, true)
}

func (app *App) upgrade(ctx context.Context, inv cli.Invocation, view *ui.UI) (result, error) {
	if len(inv.Args) != 0 {
		return result{}, usage("`cfdev upgrade` accepts no arguments", "Try `cfdev upgrade`.")
	}
	progress := view.Progress("Checking for a cfdev update…")
	defer progress.Stop()
	updateContext, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	updateResult, err := updater.Upgrade(updateContext, Version)
	if err != nil {
		return result{}, err
	}
	progress.Stop()
	summary := "cfdev " + updateResult.CurrentVersion + " is already current."
	if updateResult.Updated {
		summary = "Upgraded cfdev to " + updateResult.LatestVersion + "."
		if updateResult.Pending {
			summary = "Downloaded cfdev " + updateResult.LatestVersion + "; Windows will finish replacing the executable now."
		}
	}
	return result{Data: updateResult, Summary: summary}, nil
}

func (app *App) ready() (config.Paths, *config.Config, *cloudflared.Client, *cloudflare.API, string, error) {
	paths, err := config.ResolvePaths()
	if err != nil {
		return paths, nil, nil, nil, "", err
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return paths, nil, nil, nil, "", err
	}
	client, err := cloudflared.Find(paths)
	if err != nil {
		return paths, nil, nil, nil, "", err
	}
	certPath := cloudflare.FindOriginCert()
	cert, err := cloudflare.ReadOriginCert(certPath)
	if err != nil {
		return paths, nil, nil, nil, "", err
	}
	return paths, cfg, client, cloudflare.NewAPI(cert), certPath, nil
}

func (app *App) renderMappings(view *ui.UI, cfg *config.Config, tunnel processmanager.Status, mappings []mappingView, heading bool) {
	if heading {
		view.Heading("cfdev " + Version)
		view.Line("")
	}
	state := view.Dim("○ Stopped")
	if tunnel.Running {
		state = view.Green("● Running")
	}
	view.Line("  Tunnel  " + state + "    Domain  " + view.Bold(cfg.Domain))
	view.Line("")
	if len(mappings) == 0 {
		view.Muted("  No mappings yet. Run `cfdev add <name> <port>`.")
		return
	}
	width := 0
	for _, mapping := range mappings {
		if len(mapping.Hostname) > width {
			width = len(mapping.Hostname)
		}
	}
	for _, mapping := range mappings {
		dot := view.Dim("○")
		note := view.Dim("  app stopped")
		if mapping.LocalReachable {
			dot = view.Green("●")
			note = ""
		}
		view.Line(fmt.Sprintf("  %s  %-*s  →  localhost:%-5d%s", dot, width, mapping.Hostname, mapping.Port, note))
	}
	view.Line("")
	view.Muted("  add <name> <port>   open <name>   up -d   down")
}

func buildMappingViews(cfg *config.Config) []mappingView {
	mappings := make([]mappingView, 0, len(cfg.Mappings))
	for _, mapping := range cfg.Mappings {
		hostname := mapping.Subdomain + "." + cfg.Domain
		mappings = append(mappings, mappingView{
			Subdomain: mapping.Subdomain, Hostname: hostname, URL: "https://" + hostname,
			Port: mapping.Port, Protocol: mapping.Protocol,
			LocalURL:       fmt.Sprintf("%s://localhost:%d", mapping.Protocol, mapping.Port),
			LocalReachable: localReachable(mapping.Port),
		})
	}
	sort.SliceStable(mappings, func(i, j int) bool { return mappings[i].Subdomain < mappings[j].Subdomain })
	return mappings
}

func mappingData(cfg *config.Config, mapping config.Mapping, reachable bool) map[string]any {
	hostname := mapping.Subdomain + "." + cfg.Domain
	return map[string]any{
		"subdomain": mapping.Subdomain, "hostname": hostname, "url": "https://" + hostname,
		"port": mapping.Port, "protocol": mapping.Protocol,
		"local_url":       fmt.Sprintf("%s://localhost:%d", mapping.Protocol, mapping.Port),
		"local_reachable": reachable,
	}
}

func publicConfig(paths config.Paths, cfg *config.Config) map[string]any {
	return map[string]any{
		"version": cfg.Version, "domain": cfg.Domain, "machine_id": cfg.MachineID,
		"tunnel_name": cfg.TunnelName, "tunnel_id": cfg.TunnelID,
		"credentials_file": cfg.CredentialsFile, "mappings": cfg.Mappings,
		"preferences": cfg.Preferences, "config_file": paths.Config, "ingress_file": paths.Ingress,
	}
}

func localReachable(port int) bool {
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func launchBrowser(target string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("cmd", "/d", "/s", "/c", "start", "", target)
	case "darwin":
		command = exec.Command("open", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func (app *App) confirm(question string, defaultYes bool) (bool, error) {
	suffix := " [Y/n] "
	if !defaultYes {
		suffix = " [y/N] "
	}
	fmt.Fprint(app.Out, question+suffix)
	answer, err := app.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer == "" {
		return defaultYes, nil
	}
	return answer == "y" || answer == "yes", nil
}

func (app *App) prompt(question string) (string, error) {
	fmt.Fprint(app.Out, question+": ")
	answer, err := app.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", failure.New("INPUT_REQUIRED", question+" is required", failure.ExitConfig)
	}
	return answer, nil
}

func usage(message, hint string) error {
	err := failure.New("INVALID_USAGE", message, failure.ExitUsage)
	err.Hint = hint
	return err
}

func fileExists(target string) bool {
	info, err := os.Stat(target)
	return err == nil && !info.IsDir()
}

func friendlyCloudflaredVersion(raw string) string {
	fields := strings.Fields(raw)
	for index, field := range fields {
		if strings.EqualFold(field, "version") && index+1 < len(fields) {
			return "cloudflared " + fields[index+1]
		}
	}
	return "cloudflared"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	if typed, ok := err.(*failure.Error); ok {
		return typed.Message
	}
	return err.Error()
}

func level(ok bool) string {
	if ok {
		return "pass"
	}
	return "error"
}

var builtDatePattern = regexp.MustCompile(`(?i)built\s+(\d{4}-\d{2}-\d{2})`)

func cloudflaredOlderThanOneYear(version string, now time.Time) bool {
	match := builtDatePattern.FindStringSubmatch(version)
	if len(match) != 2 {
		return false
	}
	built, err := time.Parse("2006-01-02", match[1])
	if err != nil {
		return false
	}
	return built.Before(now.AddDate(-1, 0, 0))
}
