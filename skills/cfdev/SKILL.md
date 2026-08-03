---
name: cfdev
description: Operate cfdev to give local apps permanent URLs on a Cloudflare domain. Use when Codex needs to install, initialize or set up, reset, or upgrade cfdev; authenticate, validate, show, or switch the active domain; expose localhost ports; add, claim, list, clear, or remove permanent project subdomains; move a URL between machines; manage the shared tunnel; automate cfdev with JSON; or diagnose authentication, DNS, routing, configuration, and local port-health problems.
---

# Use cfdev

Use one shared Cloudflare tunnel to route permanent project hostnames to local HTTP ports. Keep each local app running; cfdev supplies public access and does not replace the app process.

## Locate and inspect

Prefer `cfdev` on `PATH`. In the source checkout, resolve the repository root and use its platform binary, such as `.\dist\cfdev.exe`; do not assume a fixed clone path or rebuild unless the user asks for development work.

Inspect before mutating:

```text
cfdev list --json
cfdev status --json
```

Use `cfdev doctor` when authentication, credentials, DNS, ingress, or process health is uncertain.

## Install and initialize

Run cfdev only on a computer hosting an app. Prefer a published installer or package-manager build because release downloads are checksum-verified. Use `darwin-arm64` for Apple Silicon and `darwin-amd64` for Intel when selecting a raw Mac binary. If no release exists, report that honestly; use a source build only with the user's authorization and an available Go toolchain.

Run `cfdev setup` once on each computer and let the human complete Cloudflare browser authorization. In automation, use `cfdev setup --json`. If it returns `AUTH_REQUIRED` with exit code `2`, ask the human to run interactive `cfdev setup`; never bypass browser authentication. If it returns `DOMAIN_REQUIRED`, obtain the intended domain and use the supplied explicit retry command. A successful explicit fallback with `domain_validated:false` means setup continued through a discovery outage; report that state and validate with `cfdev doctor` once Cloudflare is reachable. `cfdev init` is a compatibility alias.

Never print, copy, commit, or transfer origin certificates or tunnel credential JSON files. `cfdev domain` may safely move its own saved origin certificates within `~/.cloudflared`; `cfdev reset` preserves origin certificates and deletes only the credential for the exact tunnel it successfully unregisters.

## Switch domains or forget a machine

Use `cfdev domain` to inspect the active domain. Before `cfdev domain <domain>`, remove all mappings with `cfdev clear`; the switch deliberately refuses to abandon live hostnames. A switch validates browser authorization and stays within the same Cloudflare account. For another account, run `cfdev reset` first, then `cfdev setup <domain>`.

`cfdev reset` is destructive and requires explicit user approval. It stops the exact managed connector, deletes exact cfdev-owned DNS and the machine tunnel, removes that tunnel's credential and local machine identity, and preserves the origin certificate and managed binary. For approved non-interactive use, inspect state first and pass `--yes --json`; use `--force` only when the user accepts that failed remote cleanup may leave Cloudflare resources behind.

## Expose apps

From a project directory, prefer:

```text
cfdev <port> -d
```

The shortcut derives the hostname from the folder, safely claims that hostname if it currently targets another Cloudflare Tunnel, updates this machine's mapping, and starts the shared tunnel in the background.

For an explicit hostname:

```text
cfdev add <subdomain> <port>
cfdev up -d
```

One tunnel serves every configured mapping. Keep all corresponding app processes running. Use a different subdomain for an additional app. Do not repoint one name to another port unless the user intends that change.

## Move a URL between machines

Keep the URL unchanged with a safe claim on the destination computer:

```text
cfdev claim screenslick 3005
cfdev up -d
```

`claim` overwrites only a conflicting CNAME that targets another `*.cfargotunnel.com` tunnel. It does not affect the source machine's other hostnames and refuses unrelated DNS records.

For the usual same-folder workflow, `cfdev <port> -d` performs this claim automatically. Running it later on the original computer moves the URL back. One hostname targets one active machine tunnel at a time; do not imply simultaneous multi-machine routing.

Reserve `--force` for a conflict the user has explicitly inspected and chosen to replace. Do not use it for a normal machine handoff.

## Manage the tunnel

Background mode reloads mappings automatically after `add`, `claim`, or `remove`. If a foreground connector is running, execute this from another terminal:

```text
cfdev up -d
```

cfdev safely transitions the exact managed foreground connector into the background. This does not stop local apps.

Use `cfdev down` to stop only cfdev's connector. Default output is concise; use `--verbose` only for raw `cloudflared` diagnostics, which are also stored in `~/.cfdev/cloudflared.log`.

## Remove hostnames

Remove one mapping:

```text
cfdev remove <name>
```

Prefer the short project name shown by `cfdev list`, for example `cfdev remove screenslick`; do not make the user type the domain. A pasted full hostname on the configured domain, such as `cfdev remove screenslick.example.com`, is accepted as the same mapping. For a hostname on another domain, stop and inspect `cfdev list` rather than forcing removal.

Remove every locally configured project hostname and stop the now-empty tunnel:

```text
cfdev clear
# equivalent: cfdev remove --all
```

Bulk removal requires confirmation. For approved non-interactive cleanup, first inspect `cfdev list --json`, then use `cfdev clear --yes --json`. Both single and bulk removal delete only exact DNS records owned by this machine tunnel; they never delete the zone, tunnel, certificate, credentials, local apps, or unrelated DNS.

## Upgrade

For a direct installer or raw release installation, run:

```text
cfdev upgrade
```

cfdev selects the current platform release and verifies its SHA-256 checksum before replacing itself. Package-managed installations intentionally refuse self-replacement and provide the matching command, such as `brew upgrade cfdev`, `winget upgrade Deifos.cfdev`, or `scoop update cfdev`. Follow that instruction.

## Diagnose routing

For an unreachable hostname:

1. Run `cfdev list` and confirm hostname, port, and local reachability.
2. Confirm the app responds on `localhost:<port>`.
3. Run `cfdev up -d` if a changed mapping was loaded by an old foreground connector.
4. Run `cfdev doctor`.
5. Inspect `~/.cfdev/cloudflared.log` or reproduce with `cfdev up --verbose`.

A Cloudflare 404 immediately after a mapping change usually means an older foreground connector still has the prior ingress configuration.

## Automate safely

Use `--json` for one stable machine-readable object and `-d` for non-blocking startup. Treat exit codes as stable:

- `0`: success or desired state already exists
- `1`: general or Cloudflare operation failure
- `2`: authentication, configuration, or confirmation required
- `3`: invalid usage
- `4`: missing or unsupported dependency
- `5`: mapping, DNS, or process conflict

Prefer idempotent retries. Ask before destructive bulk removal or intentional `--force`; do not expose a local service unless the user placed it in scope.
