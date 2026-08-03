# Security Policy

## Supported versions

Security fixes are applied to the latest released version of cfdev on the default branch and the newest GitHub Release tag.

## What cfdev handles

cfdev manages local Cloudflare Tunnel state on the machine where it runs:

- Browser-login origin certificates under `~/.cloudflared/` (or the equivalent on your OS)
- Tunnel credential JSON files under that same directory
- Local non-secret state under `~/.cfdev/`
- DNS records on the Cloudflare zone authorized during `cfdev setup`

cfdev never asks you to paste an API token. Do not copy, commit, or transfer origin certificates or tunnel credential files between machines or into git.

`cfdev domain` may move origin certificates within the protected Cloudflare directory so a user can switch back to a previously authorized domain without duplicating the secret. `cfdev reset` preserves origin certificates and deletes a tunnel credential only after Cloudflare confirms deletion of that exact tunnel.

## Release integrity

cfdev installers verify downloaded binaries against the SHA-256 checksums published with each GitHub Release. This detects corruption or a binary that does not match that release metadata, but it is not independent proof of authorship: anyone able to replace both a release asset and its published checksum could make them agree. GitHub's build provenance attestations provide an additional verification signal for release binaries.

## Reporting a vulnerability

Please report security issues privately. Prefer one of:

1. [GitHub Security Advisories](https://github.com/deifos/cfdev/security/advisories/new) for this repository
2. Email the maintainer listed on the GitHub profile for [deifos/cfdev](https://github.com/deifos/cfdev)

Include the affected version or commit, reproduction steps, impact, and any suggested fix. Do not open a public issue for vulnerabilities that could let an attacker overwrite DNS, replace binaries, or exfiltrate credentials.

You should receive an acknowledgment within a few days. We will coordinate a fix and disclosure timeline before any public write-up.

## Scope notes

In scope:

- Credential handling and secret leakage in logs or JSON output
- Unsafe DNS overwrite or delete behavior beyond documented `--force` / claim semantics
- Installer or `cfdev upgrade` checksum bypass or supply-chain issues in release assets
- Command injection, path traversal, or privilege escalation in cfdev itself

Out of scope:

- Exposing a local development app that intentionally listens on a mapped port (that is the product)
- Compromised Cloudflare account credentials or browser sessions
- Issues solely in upstream `cloudflared` or the Cloudflare API (report those upstream)
- Social engineering that tricks an operator into running `--force` after inspecting a conflict

## Hardening tips for operators

- Prefer checksum-verified installers or package managers over ad-hoc binaries
- Treat `CFDEV_UPDATE_URL`, `CFDEV_RELEASES_URL`, `CFDEV_CLOUDFLARED`, and `CFDEV_API_URL` as trusted-operator overrides
- Use `cfdev claim` for machine handoff; reserve `--force` for conflicts you have inspected
- Keep local apps unbound from the public internet except through the tunnel mappings you create
