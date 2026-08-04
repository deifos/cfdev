# cfdev implementation draft

**Status:** v0.3.1 released with the foreground request feed
**Target:** v0.3.1
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
7. **Local traffic visibility** — the built-in inspector makes permanent URLs practical for webhook and OAuth debugging without sending captured traffic to another service.

We will not compete on having the largest tunnel-management feature set. Multi-account management, multi-tunnel dashboards, TCP routing, quick tunnels, and a fully interactive TUI remain outside v0.1.

## 1. The experience we are building

The normal first-time flow should be:

```text
$ cfdev setup

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

19:55:07  GET      /health         200  2ms   → localhost:3000
19:55:12  POST     /api/webhooks   204  18ms  → localhost:3000
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

On `cfdev setup`:

1. Use an existing `cloudflared` installation when available.
2. If it is missing, offer to install a managed copy under `~/.cfdev/bin`.
3. Never require a system-wide install or administrator access for the managed-copy path.
4. Verify a downloaded release before using it.

This makes the product feel self-contained while still relying on Cloudflare’s official binary.

## 4. Authentication and automatic setup

`cfdev setup` will:

1. Check or install `cloudflared`.
2. Detect an existing `~/.cloudflared/cert.pem` and reuse it when valid.
3. Otherwise run `cloudflared tunnel login`, which opens the browser automatically.
4. Wait for the browser authorization to complete.
5. Read the selected zone identifier from the certificate created by `cloudflared`.
6. Ask Cloudflare for that zone’s name using the scoped credential already issued by the browser flow.
7. Create or reuse one long-lived tunnel for this machine, internally named `cfdev-<machine>-<short-id>`.
8. Save the local cfdev configuration and generate the ingress configuration.

Fallback: if automatic domain discovery ever fails, show one short prompt for the domain. JSON mode returns `DOMAIN_REQUIRED` with an explicit retry command. A non-interactive user can run `cfdev setup example.com`; its result reports `domain_validated: false` until Cloudflare can be checked again. Existing-machine domain switches remain fail-closed.

### Human and agent authentication contract

- `cfdev setup` opens the browser, waits for authorization, and completes setup automatically.
- `cfdev setup --json` completes automatically when browser authentication already exists.
- If authentication is missing in JSON mode, it exits with code `2` and an `AUTH_REQUIRED` result that tells the agent to ask the human to run `cfdev setup` once.
- `--yes` skips safe confirmations, but never pretends that browser authentication can happen without a person.
- After that one-time browser action, all normal commands are fully unattended and agent-friendly.

Example authentication handoff:

```json
{
  "ok": false,
  "data": {
    "interactive_command": "cfdev setup"
  },
  "summary": "Cloudflare authentication is required",
  "error": {
    "code": "AUTH_REQUIRED",
    "message": "Run cfdev setup once to authenticate in your browser."
  }
}
```

Security boundaries:

- Never print, log, or copy the account token contained in `cert.pem`.
- Keep origin certificates and active tunnel credentials in Cloudflare’s normal protected directory. `domain` moves certificates only within that directory; confirmed `reset` preserves certificates and removes only the deleted tunnel's credential.
- Store only paths and non-secret identifiers in `~/.cfdev/config.json`.
- Write cfdev state files with user-only permissions where the operating system supports them.

## 5. Current command surface

| Command | Behavior |
| --- | --- |
| `cfdev setup [domain]` | Browser sign-in and automatic tunnel setup (`init` remains an alias) |
| `cfdev domain [domain]` | Show the current domain or validate authorization and switch within the current Cloudflare account; refuses while mappings exist |
| `cfdev reset` | Confirm, stop the connector, remove exact owned DNS and the machine tunnel, delete its credential, and forget local machine state |
| `cfdev <port>` | Derive a clean name from the current folder, add it, and run the tunnel |
| `cfdev add <name> <port>` | Create permanent DNS and map it to the local HTTP port |
| `cfdev claim <name> <port>` | Move an existing Cloudflare Tunnel hostname to this machine without changing its URL |
| `cfdev remove <name>` | Remove the URL by its short project name and delete its cfdev-owned DNS record |
| `cfdev remove --all` / `cfdev clear` | Confirm, remove every cfdev-owned project hostname, clear mappings, and stop the tunnel |
| `cfdev list` / `cfdev ls` | Show URLs, ports, tunnel state, and whether each local app is listening |
| `cfdev up` | Run the tunnel in the foreground with a compact live request feed |
| `cfdev up -d` | Run the tunnel in the background |
| `cfdev down` | Gracefully stop the cfdev-managed process |
| `cfdev status` | Show tunnel process state and local app health |
| `cfdev open <name>` | Open the permanent URL in the default browser |
| `cfdev inspect` | Open the loopback-only request inspector; `--capture-bodies` enables exact bodies for future traffic |
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
- `--capture-bodies` for prospective exact request/response body capture in the local inspector.
- `--help` for concise examples and command-specific help.

Every major command is idempotent. Repeating `setup`, `domain` for the active domain, `reset` for an already reset machine, an identical `add`, `remove` for an already absent cfdev-owned mapping, `up` for a running tunnel, or `down` for a stopped tunnel returns a successful description of the current state rather than creating duplicates or failing unnecessarily.

## 6. Configuration ownership

`cfdev` owns:

```text
~/.cfdev/
├── config.json             source of truth for cfdev
├── cloudflared.yml         generated; never hand-edited
├── machine-id              stable local identity for tunnel naming
├── process.json            background-process state
├── cloudflared.log         foreground and background tunnel diagnostics
├── inspector.json          protected inspector process/control state
├── inspector.log           inspector startup diagnostics (never traffic history)
└── bin/cloudflared         optional managed installation
```

Cloudflare owns:

```text
~/.cloudflared/
├── cert.pem                browser-login account certificate
├── cfdev-cert-<domain>.pem saved authorization for a previously active domain
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
- Each completed request prints one terminal-safe metadata line with local time, method, path without query parameters, status, duration, localhost target, and an optional replay marker. Headers and bodies remain in the browser inspector.
- Terminal and dashboard statuses use the same language: 2xx green, 3xx muted, and 4xx/5xx red.
- Raw `cloudflared` output is hidden by default, always retained in `cloudflared.log`, and streamed only with `--verbose` / `-v`.
- `cfdev up -d` starts a hidden/background process and records its PID and start time.
- If a foreground tunnel is already running, `cfdev up -d` stops that exact managed connector and replaces it with a background connector without requiring a manual terminal restart.
- Before stopping a saved PID, cfdev verifies that it still belongs to the expected `cloudflared` process.
- `cfdev status` checks the managed process and each configured localhost port without requiring network access.
- Tunnel logs are kept small through basic rotation.
- Normal ingress points to a persistent loopback gateway at `127.0.0.1:4041`; the gateway reloads mappings from config, so its in-memory history survives connector reloads.
- The dashboard/API binds to `127.0.0.1:4040`. If either inspector port cannot bind, tunnel startup writes direct localhost ingress and continues with a warning.
- The dashboard keeps tunnel/local-app failures visible even when history exists, presents response status, duration, headers, and body beside their request counterparts, and marks replays prominently.
- A default-on view filter hides common Next.js, Vite, and webpack development traffic without removing it from the bounded in-memory history.

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
  Run `cfdev setup` to install a managed copy.
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

## 15. v0.3 request inspector contract

The inspector is a focused local debugging tool, not an observability platform:

- Capture method, path, public hostname, actual localhost target, status, duration, and redacted headers for every completed request.
- Keep one live request list and detail pane with a text filter. Do not add charts, disk persistence, request editing, auth policies, or project-local config in v0.3.
- Keep bodies off by default. Enabling them affects only future traffic and preserves the exact bytes used for replay; formatting is a display-only operation.
- Keep at most 200 exchanges, at most 1 MiB from each request or response body, and at most 32 MiB of captured body bytes in total. Evict oldest entries first. A truncated body is inspectable but not replayable.
- Replace `Authorization`, `Proxy-Authorization`, `Cookie`, and `Set-Cookie` values before storing records. Preserve webhook signature headers. Omit redacted headers from replay and copy-as-curl.
- Replay once to the exact local target captured with the exchange and record it as a new entry with a replay marker. Never replay to the external sender.
- Preserve WebSocket upgrades and streams without wrapping their response transport. Record metadata only and disable replay.
- Keep the gateway alive while mappings and `cloudflared` restart so history survives. `cfdev reset` stops it and removes its protected state; ordinary `down` leaves it available for the next `up`.
- Show distinct no-traffic, tunnel-down, and local-app-down states. A failed local connection returns a small cfdev error page through the public URL.
- `cfdev inspect --json` must start or report the inspector without launching a browser. Mutable browser APIs require a same-origin JSON request; process shutdown/settings from the CLI require the random token in `inspector.json`.

The v0.3 verification gate includes transparent HTTP byte forwarding, a real WebSocket upgrade, streaming pass-through, exact binary body capture, size truncation, sensitive-header redaction, signature preservation, safe replay after a mapping changes, count/memory eviction, loopback daemon lifecycle, and history survival across live config changes.
