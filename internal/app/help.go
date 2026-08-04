package app

import "fmt"

var Version = "0.3.1-dev"

func helpText(command string) string {
	commandHelp := map[string]string{
		"setup":   "cfdev setup [domain]\n\n  Open Cloudflare in your browser and create one permanent tunnel for this machine.",
		"init":    "cfdev init [domain]\n\n  Compatibility alias for `cfdev setup`.",
		"domain":  "cfdev domain [domain]\n\n  Show the active domain, or authenticate, validate, and switch to another domain. Clear project URLs before switching.",
		"reset":   "cfdev reset [--yes] [--force]\n\n  Stop and forget this machine. Removes cfdev-owned DNS, deletes this machine's tunnel and credential, and preserves the Cloudflare origin certificate and managed binary.",
		"add":     "cfdev add <name> <port> [--force]\n\n  Create a permanent hostname and map it to a local HTTP port. Use the short project name, such as `screenslick`.",
		"claim":   "cfdev claim <name> <port>\n\n  Move an existing cfdev project URL from another machine to this one. Use the short project name, such as `screenslick`.",
		"remove":  "cfdev remove <name> [--force]\ncfdev remove --all [--yes]\n\n  Remove one permanent project URL by its short name:\n\n    cfdev remove screenslick\n\n  A full configured hostname, such as `screenslick.example.com`, is also accepted. Bulk removal always requires confirmation.",
		"clear":   "cfdev clear [--yes]\n\n  Remove every cfdev project hostname after confirmation and stop the empty tunnel.",
		"list":    "cfdev list\n\n  Show permanent URLs, local targets, and whether each app is listening.",
		"up":      "cfdev up [--detach] [--verbose]\n\n  Start the tunnel in the foreground with a compact live request feed, or use -d for the background. Detailed cloudflared output is hidden unless --verbose is used.",
		"down":    "cfdev down\n\n  Stop the cloudflared process managed by cfdev.",
		"status":  "cfdev status\n\n  Show tunnel process state and local application health.",
		"open":    "cfdev open <name>\n\n  Open a configured permanent URL by its short project name, such as `cfdev open screenslick`.",
		"inspect": "cfdev inspect [--capture-bodies]\n\n  Open the loopback-only request inspector. Metadata is always captured; exact request and response bodies are opt-in for future traffic.",
		"config":  "cfdev config [path|edit]\n\n  Show the current configuration, its paths, or edit it.",
		"doctor":  "cfdev doctor\n\n  Check cloudflared, authentication, credentials, ingress, and process state.",
		"upgrade": "cfdev upgrade\n\n  Download the latest GitHub Release for this platform, verify its checksum, and replace cfdev.",
	}
	if text := commandHelp[command]; text != "" {
		return text
	}
	return fmt.Sprintf(`cfdev %s — permanent local URLs, minus the tunnel ceremony

Usage
  cfdev setup [domain]            Sign in and create this machine's tunnel
  cfdev domain [domain]           Show or safely switch the active domain
  cfdev reset                     Stop and forget this machine
  cfdev add <name> <port>         Add a permanent URL
  cfdev claim <name> <port>       Move a project URL to this machine
  cfdev remove <name>             Remove a URL by short name
  cfdev clear                     Remove all project URLs safely
  cfdev list                      List mappings and local health
  cfdev up [-d]                   Start the tunnel and foreground request feed
  cfdev down                      Stop the tunnel
  cfdev status                    Show tunnel and app health
  cfdev open <name>               Open a public URL
  cfdev inspect                   Inspect and replay local HTTP traffic
  cfdev config [path|edit]        Show or edit config
  cfdev doctor                    Diagnose setup problems
  cfdev upgrade                   Upgrade from the latest verified release
  cfdev <port>                    Add this folder and start the tunnel

Options
  --json                          Stable JSON for scripts and agents
  --quiet, -q                     Only print errors
  --verbose, -v                   Stream detailed cloudflared output
  --force, -f                     Proceed through accepted conflicts or cleanup failures
  --all                           Remove every configured mapping
  --yes, -y                       Accept safe setup confirmations
  --detach, -d                    Run cloudflared in the background
  --capture-bodies                Capture exact bodies for future inspected requests
  --help, -h                      Show help
  --version, -V                   Show the version

Examples
  cfdev setup
  cfdev domain example.com
  cfdev add qtable 3000
  cfdev inspect --capture-bodies
  cfdev remove qtable
  cfdev up -d
  cfdev 5173`, Version)
}
