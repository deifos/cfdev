# cfdev implementation draft

**Status:** Implemented; pre-public v0.1 hardening  
**Target:** v0.1 MVP  
**cfdev gives local projects permanent URLs on your Cloudflare domain—with one browser sign-in and one command per project.**

The simplicity of ngrok, without changing URLs, copied tokens, or tunnel configuration.

## 0. How cfdev stands out

`cfdev` is the zero-ceremony development URL layer that happens to use Cloudflare. Users manage projects, hostnames, and ports—never tunnel UUIDs or hand-written ingress files.

Its focused advantages are:

1. **Browser authorization instead of copied tokens** — one normal Cloudflare sign-in is the only human handoff.
2. **One native cross-platform binary** — no Node, Bun, Python, or package-manager runtime, including on Windows.
3. **One efficient tunnel per machine** — all local project mappings share it without exposing tunnel concepts in the daily workflow.
4. **Permanent project identity** — a project keeps the same URL across restarts, making webhook and OAuth callback configuration dependable.
5. **Complete automation contract** — stable JSON, meaningful exit codes, idempotent operations, and no hidden prompts after initial authentication.
6. **Safe machine handoff** — a project URL can move between computers explicitly with `claim`, or automatically when its folder shortcut is run on the new active computer.

We will not compete on having the largest tunnel-management feature set. Multi-account management, multi-tunnel dashboards, TCP routing, quick tunnels, and a fully interactive TUI remain outside v0.1.

## 1. The experience we are building

The normal first-time flow should be:

```text
$ cfdev init

✓ cloudflared is ready
→ Opening Cloudflare in your browser…
✓ Signed in
✓ Using example.com
✓ Permanent tunnel created

Ready. Try: cfdev 3000
```

The browser opens automatically. The user signs into Cloudflare and selects a domain there. `cfdev` resumes on its own, discovers the selected domain, creates the long-lived tunnel, and writes the local configuration. There is no API-token copying and no YAML editing.

The daily flow should be:

```text
$ cd qtable
$ cfdev 3000

✓ Added https://qtable.example.com → localhost:3000
● Tunnel running — press Ctrl+C to stop
```

That URL stays the same across restarts. It can be saved in webhook providers, OAuth callbacks, mobile apps, and other integrations.

The explicit version remains available:

```text
cfdev add qtable 3000
cfdev up
```

## 2. Product principles

- One obvious path for the common case.
- Permanent URLs by default; temporary URLs are a later feature.
- Browser authentication by default; pasted API tokens are not part of the normal flow.
- No configuration file editing for routine use.
- Restrained, high-contrast output inspired by Basecamp and Charm tools.
- Useful human output in a terminal and stable JSON output for scripts and agents.
- Users think only about projects, hostnames, and ports; internal tunnel names stay an implementation detail.
- Small implementation and dependency surface.
- `cfdev` wraps Cloudflare’s official `cloudflared`; it does not reimplement the tunnel protocol.
- Commands are idempotent: repeating an operation that already reached its desired state is safe.

## 3. Proposed technical choice

### Recommendation: Go with the standard library

Build `cfdev` as a native Go executable and avoid third-party Go packages in the first version wherever practical.

Why:

- One fast binary with no Node, Bun, Python, or package-manager runtime required.
- Cross-platform releases for macOS, Windows, and Linux.
- Good process and signal handling for foreground/background tunnel management.
- Easy to produce the crisp, native CLI feel we want.
- A small standard-library parser is enough for this intentionally compact command surface.

The only external runtime component is `cloudflared`, because it provides the official Cloudflare Tunnel transport.

### Installation behavior

On `cfdev init`:

1. Use an existing `cloudflared` installation when available.
2. If it is missing, offer to install a managed copy under `~/.cfdev/bin`.
3. Never require a system-wide install or administrator access for the managed-copy path.
4. Verify a downloaded release before using it.

This makes the product feel self-contained while still relying on Cloudflare’s official binary.

## 4. Authentication and automatic setup

`cfdev init` will:

1. Check or install `cloudflared`.
2. Detect an existing `~/.cloudflared/cert.pem` and reuse it when valid.
3. Otherwise run `cloudflared tunnel login`, which opens the browser automatically.
4. Wait for the browser authorization to complete.
5. Read the selected zone identifier from the certificate created by `cloudflared`.
6. Ask Cloudflare for that zone’s name using the scoped credential already issued by the browser flow.
7. Create or reuse one long-lived tunnel for this machine, internally named `cfdev-<machine>-<short-id>`.
8. Save the local cfdev configuration and generate the ingress configuration.

Fallback: if automatic domain discovery ever fails, show one short prompt for the domain. A non-interactive user can always run `cfdev init example.com`.

### Human and agent authentication contract

- `cfdev init` opens the browser, waits for authorization, and completes setup automatically.
- `cfdev init --json` completes automatically when browser authentication already exists.
- If authentication is missing in JSON mode, it exits with code `2` and an `AUTH_REQUIRED` result that tells the agent to ask the human to run `cfdev init` once.
- `--yes` skips safe confirmations, but never pretends that browser authentication can happen without a person.
- After that one-time browser action, all normal commands are fully unattended and agent-friendly.

Example authentication handoff:

```json
{
  "ok": false,
  "data": {
    "interactive_command": "cfdev init"
  },
  "summary": "Cloudflare authentication is required",
  "error": {
    "code": "AUTH_REQUIRED",
    "message": "Run cfdev init once to authenticate in your browser."
  }
}
```

Security boundaries:

- Never print, log, or copy the account token contained in `cert.pem`.
- Keep the account certificate and tunnel credential in Cloudflare’s normal protected directory.
- Store only paths and non-secret identifiers in `~/.cfdev/config.json`.
- Write cfdev state files with user-only permissions where the operating system supports them.

## 5. v0.1 command surface

| Command | Behavior |
| --- | --- |
| `cfdev init [domain]` | Browser sign-in and automatic tunnel setup |
| `cfdev <port>` | Derive a clean name from the current folder, add it, and run the tunnel |
| `cfdev add <name> <port>` | Create permanent DNS and map it to the local HTTP port |
| `cfdev claim <name> <port>` | Move an existing Cloudflare Tunnel hostname to this machine without changing its URL |
| `cfdev remove <name>` | Remove the URL by its short project name and delete its cfdev-owned DNS record |
| `cfdev remove --all` / `cfdev clear` | Confirm, remove every cfdev-owned project hostname, clear mappings, and stop the tunnel |
| `cfdev list` / `cfdev ls` | Show URLs, ports, tunnel state, and whether each local app is listening |
| `cfdev up` | Run the tunnel in the foreground |
| `cfdev up -d` | Run the tunnel in the background |
| `cfdev down` | Gracefully stop the cfdev-managed process |
| `cfdev status` | Show tunnel process state and local app health |
| `cfdev open <name>` | Open the permanent URL in the default browser |
| `cfdev config` | Show config and generated-config paths |
| `cfdev doctor` | Diagnose installation, auth, credentials, ingress, DNS, and process state |
| `cfdev upgrade` | Install the latest compatible GitHub Release after SHA-256 verification |
| `cfdev version` | Print version information |

Common flags:

- `--json` for a stable `{ ok, data, summary, error }` envelope.
- `--quiet` / `-q` for errors only.
- `--verbose` / `-v` to stream detailed `cloudflared` output; it is hidden by default.
- `--force` for an intentional mapping or DNS replacement.
- `--all` for bulk mapping removal.
- `--yes` / `-y` for non-interactive confirmation.
- `--detach` / `-d` for background operation.
- `--help` for concise examples and command-specific help.

Every major command is idempotent. Repeating `init`, an identical `add`, `remove` for an already absent cfdev-owned mapping, `up` for a running tunnel, or `down` for a stopped tunnel returns a successful description of the current state rather than creating duplicates or failing unnecessarily.

## 6. Configuration ownership

`cfdev` owns:

```text
~/.cfdev/
├── config.json             source of truth for cfdev
├── cloudflared.yml         generated; never hand-edited
├── machine-id              stable local identity for tunnel naming
├── process.json            background-process state
├── cloudflared.log         foreground and background tunnel diagnostics
└── bin/cloudflared         optional managed installation
```

Cloudflare owns:

```text
~/.cloudflared/
├── cert.pem                browser-login account certificate
└── <tunnel-id>.json        credential for the single tunnel
```

Proposed `config.json`:

```json
{
  "version": 1,
  "domain": "example.com",
  "tunnel_name": "cfdev-vlads-macbook-a1b2c3",
  "tunnel_id": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
  "credentials_file": "/absolute/path/.cloudflared/xxxxxxxx.json",
  "machine_id": "vlads-macbook-a1b2c3",
  "mappings": [
    {
      "subdomain": "qtable",
      "port": 3000,
      "protocol": "http",
      "created_at": "2026-08-02T18:00:00Z"
    }
  ],
  "preferences": {
    "open_browser_on_add": false
  }
}
```

The generated Cloudflare config always ends with a catch-all 404 rule and is validated with `cloudflared` before a tunnel starts.

The machine-specific internal name includes a short generated identifier and is saved locally. This prevents computers with the same hostname from accidentally sharing connector traffic or colliding over locally managed ingress rules. Users still see the product simply as `cfdev`; the internal tunnel name is an implementation detail.

## 7. Mapping and DNS behavior

`cfdev add qtable 3000` will:

1. Validate the name and port locally.
2. Reject an existing local mapping unless it is identical or `--force` is supplied.
3. Ask `cloudflared` to create the CNAME for `qtable.<domain>`.
4. Save the mapping and atomically regenerate the ingress file.
5. Check whether `localhost:3000` is listening and show a warning if it is not.
6. If cfdev is already running in background mode, restart it automatically so the mapping is live.
7. If it is running in another foreground terminal, explain that it must be restarted rather than unexpectedly killing it.

`cfdev remove qtable` is the primary removal flow: the user types only the short project name shown by `cfdev list`. As a forgiving fallback, `cfdev remove qtable.<domain>` normalizes the configured full hostname to the same short name. A hostname on another domain is rejected with a corrective hint, and entering a hostname without a command suggests the corresponding short `remove` and `open` forms.

Removal deletes only a DNS record that matches both the exact hostname and this tunnel’s `<id>.cfargotunnel.com` target. Accepting the full configured hostname does not broaden ownership and will not delete an unrelated record with the same hostname.

`cfdev claim qtable 3000` is the safe computer-to-computer handoff. It automatically overwrites DNS only when the conflicting record is a CNAME to another `*.cfargotunnel.com` target. Any unrelated DNS record still requires a separately intentional `--force` operation.

The folder shortcut sets the active machine naturally: running `cfdev 3000` inside the same project folder on another initialized computer performs the safe claim, updates that machine's local mapping, and starts its tunnel. Running it on the original machine later moves the URL back. The source machine keeps its other mappings, and one hostname targets exactly one machine tunnel at a time.

`cfdev clear` and `cfdev remove --all` remove all locally configured project hostnames only after confirmation. JSON automation must supply `--yes`; DNS deletion remains ownership-scoped for every mapping. The commands never delete the Cloudflare zone, browser certificate, tunnel credential, or unrelated DNS.

## 8. Process behavior

- `cfdev up` stays attached to the terminal, shows concise cfdev status, and responds cleanly to Ctrl+C.
- Raw `cloudflared` output is hidden by default, always retained in `cloudflared.log`, and streamed only with `--verbose` / `-v`.
- `cfdev up -d` starts a hidden/background process and records its PID and start time.
- If a foreground tunnel is already running, `cfdev up -d` stops that exact managed connector and replaces it with a background connector without requiring a manual terminal restart.
- Before stopping a saved PID, cfdev verifies that it still belongs to the expected `cloudflared` process.
- `cfdev status` checks the managed process and each configured localhost port without requiring network access.
- Tunnel logs are kept small through basic rotation.

## 9. Distribution and upgrades

- A semver tag (`vX.Y.Z`) runs tests, vet, formatting checks, and six native builds: Windows, macOS, and Linux on AMD64 and ARM64.
- Release builds contain no third-party runtime dependencies and embed the tag as their version.
- GitHub Releases publish the six raw binaries, `checksums.txt`, install scripts, build attestations, and generated Homebrew, Winget, and Scoop definitions.
- The Windows installer uses a user-local directory and updates the user `PATH`. The macOS/Linux installer uses `~/.local/bin` by default. Both select the platform automatically and verify SHA-256 before installing.
- `cfdev upgrade` checks the latest GitHub Release, downloads only the matching binary, verifies size and checksum, and replaces the executable. Windows completes its replacement through a short detached helper after the running process exits.
- Homebrew, Winget, and Scoop installations are upgraded through their package manager; `cfdev upgrade` detects those paths and prints the correct command instead of modifying managed files.
- Central package catalogs and a Homebrew tap/Scoop bucket are enabled after the first public release; the repository generates exact, submission-ready manifests so release hashes are not maintained by hand.

## 10. CLI visual direction

Example dashboard (`cfdev` with no arguments):

```text
cfdev 0.1

  Tunnel  ● Running       Domain  example.com

  ●  qtable.example.com       →  localhost:3000
  ●  screenslick.example.com  →  localhost:5173
  ○  family.example.com       →  localhost:4000  app stopped

  add <name> <port>   open <name>   up -d   down
```

The first release will provide this polished dashboard, but not a full interactive TUI. Keyboard navigation and in-dashboard editing can follow after the core tunnel lifecycle is reliable.

Color rules:

- Green only for healthy/successful state.
- Yellow only for actionable warnings.
- Red only for failures.
- Muted gray for secondary context.
- No color when output is piped or `NO_COLOR` is set.

## 11. Error and automation contract

Errors should state what happened and the next useful action:

```text
✗ cloudflared is not installed.
  Run `cfdev init` to install a managed copy.
```

Proposed exit codes:

- `0` success
- `1` general or Cloudflare operation failure
- `2` authentication/configuration failure
- `3` invalid command usage
- `4` missing dependency
- `5` mapping, DNS, or process conflict

JSON example:

```json
{
  "ok": true,
  "data": {
    "url": "https://qtable.example.com",
    "local_url": "http://localhost:3000"
  },
  "summary": "Added qtable → localhost:3000"
}
```

## 12. Implementation sequence

### Milestone 1 — reliable setup

- Native CLI scaffold and help.
- `cloudflared` discovery/managed installation.
- Browser login and automatic domain discovery.
- Create/reuse tunnel.
- Atomic config and ingress generation.
- `doctor` for setup failures.

### Milestone 2 — permanent mappings

- `add`, `remove`, `list`, and folder-name shortcut.
- Safe DNS creation/replacement/deletion.
- Local-port health checks.
- JSON output and documented exit codes.

### Milestone 3 — tunnel lifecycle and polish

- Foreground/background `up`, `down`, and `status`.
- Browser `open`.
- Dashboard, color system, log rotation, and cross-platform packaging.
- End-to-end test pass on Windows, macOS, and Linux.

## 13. Test gates before calling v0.1 complete

- Fresh machine with neither cfdev nor cloudflared configured.
- Existing cloudflared browser login is reused without opening another browser.
- Browser login correctly resumes after authorization.
- Domain discovery works after selecting a Cloudflare zone.
- Existing tunnel is reused only when its local credential is available.
- Two or more mappings route to different local ports through one tunnel.
- Duplicate DNS and local mapping conflicts are safe and actionable.
- `claim` moves a hostname between machine tunnels but refuses unrelated DNS.
- The folder shortcut reclaims a project URL on the computer where it is run.
- Adding a mapping updates a running background tunnel.
- `up -d` safely transitions an existing foreground tunnel into the background.
- Removing a mapping cannot delete unrelated DNS.
- `clear` requires confirmation and removes only all locally configured, tunnel-owned hostnames.
- Foreground Ctrl+C and background shutdown leave no stale process state.
- `--json` never contains progress text or terminal color codes.
- Config writes survive interruption without leaving partial JSON or YAML.
- Helpful behavior for missing/old cloudflared, expired auth, unavailable network, and a local port with nothing listening.
- The missing-auth JSON path returns `AUTH_REQUIRED` without opening a browser or blocking an agent.
- Repeating every major command is safe and produces the intended final state without duplicate resources.
- Every release target cross-compiles, installers parse, package manifests render without placeholders, and upgrade downloads fail closed on checksum mismatch.

## 14. Review decisions

Unless changed during review, implementation will use these defaults:

1. **Go native binary**, with standard-library-first implementation.
2. **Managed cloudflared installation** when no existing binary is found, after a one-line confirmation.
3. One tunnel per machine, internally named **`cfdev-<machine>-<short-id>`**.
4. `cfdev <port>` uses the cleaned current-folder name and starts in the foreground.
5. Adding to a background tunnel restarts it automatically; `up -d` is the explicit safe transition from foreground to background.
6. Removing a mapping also safely removes its matching DNS record.
7. v0.1 ships a polished dashboard; the fully interactive TUI follows later.
8. Initial browser authentication is the only human handoff; JSON mode reports `AUTH_REQUIRED` honestly when it is missing.
9. All major commands are idempotent and safe for agents to retry.
10. Machine switching uses safe Cloudflare-Tunnel-only claims; unrelated DNS always requires explicit force.
11. GitHub Releases are the source of truth for installers, in-app upgrades, checksums, attestations, and generated package manifests.

Implementation follows the three milestones above, with each milestone kept independently testable and reviewable.
