# Changelog

All notable changes to cfdev are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.1] - 2026-08-03

### Added

- Stream a compact, color-coded request feed during foreground `cfdev up`, showing completion time, method, path, status, duration, localhost target, and replay markers while keeping the browser inspector for full details.
- Add a default-on dashboard toggle that hides common Next.js, Vite, and webpack development noise without deleting those requests from in-memory history.

### Changed

- Use one status-color language across the terminal and dashboard: 2xx green, 3xx muted, and 4xx/5xx red.
- Keep tunnel and local-app warnings visible alongside existing history, make response status and duration explicit in request details, and give replayed requests prominent list and detail markers.

### Security

- Omit query parameters, headers, and bodies from the foreground request feed and sanitize terminal control characters before displaying request metadata.

## [0.3.0] - 2026-08-03

### Added

- Add a repository-level `AGENTS.md` working agreement covering implementation quality, safety boundaries, required tests, documentation synchronization, verification, and release discipline for coding agents.
- Add the loopback-only `cfdev inspect` dashboard with a live request list, filtering, headers, local targets, status and timing, copy-as-curl, and one-shot replay to the original localhost target.
- Add prospective `--capture-bodies` support that preserves exact request and response bytes for webhook debugging.

### Changed

- Route tunnel traffic through a persistent local gateway so request history survives mapping changes and `cloudflared` restarts, while falling back to direct localhost routing if the gateway cannot start.
- Return a clear cfdev error page when a mapped local app is not accepting connections, and report separate empty, tunnel-down, and app-down states in the inspector.

### Security

- Bind the inspector and gateway only to `127.0.0.1`, keep history in memory, cap history at 200 requests / 1 MiB per body / 32 MiB total body storage, and evict oldest entries first.
- Redact authorization and cookie headers from display and omit them from replay and generated curl commands while preserving webhook signature headers.
- Disable body capture and replay for streaming and upgraded connections, and replay only to the exact local target captured with the original request.

## [0.2.0] - 2026-08-03

### Added

- Add `cfdev setup [domain]` as the primary onboarding command while retaining `cfdev init` as a compatibility alias.
- Add `cfdev domain [domain]` to show, authenticate, validate, and safely switch the active Cloudflare domain.
- Add `cfdev reset` to stop and unregister this machine, remove its exact owned DNS records, delete its Cloudflare tunnel and credential, and forget local state after confirmation.
- Build, Developer ID sign, and notarize Intel and Apple silicon macOS release artifacts on native GitHub-hosted macOS runners.

### Changed

- Validate an explicit setup domain against the zone selected during Cloudflare browser authorization.
- Preserve per-domain origin authorization with protected same-directory moves so users can safely switch back without copying certificate tokens.
- Refuse domain switches that would abandon configured project hostnames or cross Cloudflare account boundaries.
- Preserve origin certificates and the managed `cloudflared` binary during machine reset; `--force` permits local cleanup when remote cleanup cannot complete and reports the remaining Cloudflare resources.
- Roll back an authorization change when setup fails, restore removed DNS after an interrupted reset, and restore the previous config if ingress generation fails.
- Report a completed domain switch as actionable partial success when its previously running tunnel cannot restart.

### Security

- Restrict reset cleanup to DNS records that still point to this machine's exact tunnel and delete a tunnel credential only after Cloudflare confirms deletion of that tunnel.
- Compare Cloudflare account IDs from protected origin certificates when validating an already-authorized domain switch.
- Store macOS signing certificates and App Store Connect notarization credentials only in GitHub Actions secrets.

## [0.1.1] - 2026-08-02

### Added

- Add compact interactive progress indicators with plain redirected output and animation-free JSON and quiet modes.
- Stream detailed `cloudflared` diagnostics on demand with `--verbose` while retaining the local diagnostic log.

### Changed

- Keep normal foreground tunnel output concise and improve setup and tunnel-management progress feedback.

## [0.1.0] - 2026-08-02

### Added

- Initial public release of the cross-platform Go CLI for permanent Cloudflare-backed local project URLs.
- Add browser-based setup, one tunnel per machine, project add/claim/remove/list workflows, foreground and background tunnel management, health diagnostics, JSON automation, and self-upgrade support.
- Add checksum-verified installers and release artifacts for Windows, macOS, and Linux on AMD64 and ARM64.

### Security

- Limit automatic DNS replacement and deletion to records that match cfdev's documented ownership rules.
- Verify managed `cloudflared` downloads and cfdev release upgrades against published SHA-256 manifests.

[Unreleased]: https://github.com/deifos/cfdev/compare/v0.3.1...HEAD
[0.3.1]: https://github.com/deifos/cfdev/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/deifos/cfdev/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/deifos/cfdev/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/deifos/cfdev/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/deifos/cfdev/releases/tag/v0.1.0
