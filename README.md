# cfdev

**cfdev gives local projects permanent URLs on your Cloudflare domain—with one browser sign-in and one command per project.**

The simplicity of ngrok, without changing URLs, copied tokens, or tunnel configuration.

```text
$ cd qtable
$ cfdev 3000

✓  Added https://qtable.example.com → localhost:3000
→  Starting the tunnel…
✓  Tunnel is running — press Ctrl+C to stop.
```

The public URL stays the same every time. Put it in webhook providers, OAuth callbacks, mobile apps, or anywhere else that needs to reach a service running on your machine.

## Why cfdev

- **Permanent project URLs.** Folder-aware shortcuts give each project a stable hostname on your domain.
- **Browser sign-in.** cfdev uses Cloudflare's normal browser authorization instead of asking you to create and paste an API token.
- **One tunnel per machine.** Every project mapping shares one efficient connector; tunnel names and UUIDs stay out of your workflow.
- **Native and cross-platform.** One small Go binary for Windows, macOS, and Linux, with no language runtime or package dependencies.
- **Agent-ready.** Every major command has stable JSON, meaningful exit codes, no surprise prompts, and retry-safe behavior.
- **Official transport.** cfdev manages Cloudflare's `cloudflared` binary rather than reimplementing the tunnel protocol.

## Install

cfdev is currently pre-release, so the commands below become available when the first GitHub Release is published. Each installer selects the correct architecture, verifies the downloaded binary against the release SHA-256 manifest, installs without administrator access, and adds `cfdev` to the user `PATH`.

Windows:

```powershell
irm https://github.com/deifos/cfdev/releases/latest/download/install.ps1 | iex
```

macOS or Linux:

```bash
curl -fsSL https://github.com/deifos/cfdev/releases/latest/download/install.sh | sh
```

Every version tag builds native binaries for Windows, macOS, and Linux on AMD64 and ARM64. The release also includes `checksums.txt`, build attestations, and generated Homebrew, Winget, and Scoop definitions. Catalog commands such as `brew install deifos/tap/cfdev`, `winget install Deifos.cfdev`, and a Scoop bucket install will be enabled after their repositories accept the first public release.

Until then, build from source with Go 1.24 or newer:

```bash
go build -trimpath -o cfdev ./cmd/cfdev
```

## Quick start

Run the one-time setup:

```bash
cfdev init
```

cfdev will:

1. Find your existing `cloudflared` installation or offer to download a checksum-verified managed copy.
2. Open Cloudflare in your browser if authentication is needed.
3. Discover the domain you authorized.
4. Create one internally named `cfdev-<machine>-<short-id>` tunnel.
5. Generate and validate the local configuration.

From any project directory:

```bash
cfdev 3000
```

The folder name becomes the subdomain. For example, running this inside `qtable-frontend` produces `https://qtable.<your-domain>`.

## Multiple computers

Run cfdev on the computer that hosts the local app. A Mac can open an app exposed from Windows without installing cfdev; install cfdev on the Mac only when the app itself runs locally there.

Each computer runs `cfdev init` once and creates its own machine tunnel. Authenticate normally in the browser on each machine—never copy `cert.pem` or tunnel credential files between computers.

A permanent hostname can be handed from one machine to another without changing its URL. For example, to move `screenslick.<your-domain>` from Windows to a Mac where the app uses port 3005, run on the Mac:

```bash
cfdev claim screenslick 3005
cfdev up -d
```

`claim` repoints that exact hostname only when its existing DNS target is another Cloudflare Tunnel. It refuses to overwrite unrelated DNS; `--force` remains the explicit escape hatch for a conflict you have inspected.

The folder shortcut makes normal switching automatic. In a `screenslick` checkout on either initialized machine, this command claims the project URL for the computer where it is run, updates its local port, and ensures the shared tunnel is running:

```bash
cfdev 3005 -d
```

The old computer can keep serving its other mappings, but it no longer receives traffic for `screenslick`. Running the shortcut there later moves the URL back. One hostname has one active machine tunnel at a time; cfdev does not send the same hostname to two computers simultaneously.

## Commands

```text
cfdev init [domain]             Sign in and create this machine's tunnel
cfdev <port>                    Add the current project and start the tunnel
cfdev add <name> <port>         Add a permanent URL
cfdev claim <name> <port>       Move a project URL to this machine
cfdev remove <name>             Remove a URL by its short project name
cfdev remove --all              Remove all project URLs after confirmation
cfdev clear                     Alias for remove --all
cfdev list                      List mappings and local health
cfdev up                        Run the tunnel in the foreground
cfdev up -d                     Run the tunnel in the background
cfdev down                      Stop the managed tunnel process
cfdev status                    Show tunnel and local app health
cfdev open <name>               Open a permanent URL
cfdev config [path|edit]        Show or edit local configuration
cfdev doctor                    Diagnose setup and routing problems
cfdev upgrade                   Install the latest verified GitHub Release
cfdev version                   Print the version
```

Common flags:

```text
--json                          Stable JSON for scripts and agents
--quiet, -q                     Only print errors
--verbose, -v                   Stream detailed cloudflared output
--force, -f                     Replace conflicting local or DNS state
--all                           Remove every configured mapping
--yes, -y                       Accept safe setup confirmations
--detach, -d                    Run cloudflared in the background
--help, -h                      Show help
--version, -V                   Show the version
```

Foreground tunnels show only cfdev's concise status messages by default. Detailed `cloudflared` output is always written to `~/.cfdev/cloudflared.log` and can also be streamed to the terminal with `--verbose` or `-v`.

Running `cfdev up -d` while cfdev is attached in another terminal safely moves the same tunnel into the background; there is no longer a manual stop-and-restart step.

## Removing URLs

Use the short project name shown by `cfdev list`—you never need to type the whole domain:

```bash
cfdev remove screenslick
```

For convenience, pasting the full configured hostname also works:

```bash
cfdev remove screenslick.example.com
```

Both commands remove the same mapping. If a hostname is entered without an action, such as `cfdev screenslick.example.com`, cfdev suggests the short `remove` and `open` commands instead of reporting only an unknown command. Use `cfdev clear` for confirmed bulk removal.

Running `cfdev` without arguments shows a compact dashboard:

```text
cfdev 0.1

  Tunnel  ● Running    Domain  example.com

  ●  qtable.example.com       →  localhost:3000
  ●  screenslick.example.com  →  localhost:5173
  ○  family.example.com       →  localhost:4000  app stopped
```

## Agents and automation

Once a person has authenticated in the browser, the complete daily workflow is non-interactive:

```bash
cfdev add qtable 3000 --json
cfdev up -d --json
cfdev status --json
cfdev list --json
cfdev claim qtable 3000 --json
```

If an agent runs `cfdev init --json` before browser authentication exists, cfdev exits immediately with code `2`:

```json
{
  "ok": false,
  "data": {
    "interactive_command": "cfdev init"
  },
  "summary": "Cloudflare browser authentication is required",
  "error": {
    "code": "AUTH_REQUIRED",
    "message": "Cloudflare browser authentication is required",
    "hint": "Run `cfdev init` once to authenticate in your browser."
  }
}
```

The agent should ask the person to run that command once, then retry. `--yes` can approve installing a managed `cloudflared`, but it never bypasses browser authentication.

JSON mode writes exactly one JSON object to stdout with no colors, progress messages, or spinners:

```json
{
  "ok": true,
  "data": {
    "url": "https://qtable.example.com",
    "local_url": "http://localhost:3000"
  },
  "summary": "Added https://qtable.example.com → localhost:3000"
}
```

Exit codes:

| Code | Meaning |
| ---: | --- |
| `0` | Success or desired state already exists |
| `1` | General or Cloudflare operation failure |
| `2` | Authentication or configuration required |
| `3` | Invalid command usage |
| `4` | Missing or unsupported dependency |
| `5` | Mapping, DNS, or process conflict |

Commands are idempotent. Agents can safely retry `init`, an identical `add`, removal of an already absent cfdev mapping, `up` for a running tunnel, and `down` for a stopped tunnel.

Bulk deletion deliberately requires confirmation. For unattended cleanup, inspect `cfdev list --json`, then use `cfdev clear --yes --json` or `cfdev remove --all --yes --json`.

## Configuration and security

cfdev owns local state under `~/.cfdev`:

```text
~/.cfdev/
├── config.json
├── cloudflared.yml
├── machine-id
├── process.json
├── cloudflared.log        tunnel diagnostics
└── bin/cloudflared       optional managed copy
```

Cloudflare authentication remains in its normal location:

```text
~/.cloudflared/
├── cert.pem
└── <tunnel-id>.json
```

cfdev never prints or copies the account token contained in `cert.pem`. Local state stores only non-secret identifiers and the credential file's path. Files are written with user-only permissions on operating systems that support them.

DNS removal is deliberately narrow: cfdev deletes a record only when both the hostname and its `<tunnel-id>.cfargotunnel.com` target match. Accepting a full hostname does not broaden that ownership check. Automatic `claim` is similarly narrow: it replaces another tunnel CNAME but never an unrelated DNS record.

## Development

```bash
go test ./...
go vet ./...
go build -trimpath -o dist/cfdev ./cmd/cfdev
```

To publish a release after the repository is public and CI is green:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The release workflow tests and cross-builds all six supported targets, creates checksums and build attestations, renders package-manager definitions with exact hashes, and publishes one GitHub Release. `cfdev upgrade` uses that release metadata and refuses to install a binary that does not match its checksum. Package-managed installations direct users back to their package manager instead.

The implementation uses only the Go standard library. The approved product and technical decisions are recorded in [DRAFT.md](DRAFT.md).

## Agent skill

The reusable Codex skill lives in [`skills/cfdev`](skills/cfdev). Agents can read it directly from the repository, or the folder can be installed under `~/.codex/skills/cfdev` for automatic discovery.

## Security

cfdev manages Cloudflare browser credentials and DNS for your zone. See [SECURITY.md](SECURITY.md) for reporting vulnerabilities and operator guidance. Never copy or commit `cert.pem` or tunnel credential files.

## License

MIT. See [LICENSE](LICENSE).
