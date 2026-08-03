# AGENTS.md

This file defines the working agreement for coding agents in the cfdev repository. Follow it for every change unless the user gives more specific instructions.

## Product contract

cfdev gives local projects permanent URLs on a user's Cloudflare domain with one browser sign-in and one command per project. Preserve these principles:

- Keep everyday commands short, clear, and pleasant.
- Keep the Go binary dependency-free unless a dependency has a compelling, documented benefit.
- Hide tunnel ceremony by default; detailed `cloudflared` output belongs behind `--verbose` and in the diagnostic log.
- Treat Windows, macOS, and Linux as first-class platforms.
- Keep interactive use friendly and automation predictable.

## Before changing code

1. Read the relevant implementation, tests, README sections, `DRAFT.md`, `SECURITY.md`, and `skills/cfdev/SKILL.md` before changing behavior.
2. Inspect the working tree and preserve unrelated or user-authored changes.
3. Trace the complete state transition for authentication, DNS, tunnel processes, config files, installers, or upgrades. Account for partial failure and retry behavior.
4. Prefer focused changes. Do not refactor unrelated code while implementing a feature or fix.

## Implementation requirements

- Use Go 1.24 or newer and prefer the standard library.
- Do not add a module dependency without explaining why the standard library is insufficient and what maintenance or supply-chain cost it introduces.
- Keep command execution structured with argument slices. Never build shell command strings from user input.
- Keep local writes atomic where practical. Sensitive files must retain user-only permissions on platforms that support them.
- Preserve stable JSON output, documented exit codes, quiet-mode behavior, and concise default output.
- Show progress animation by default only for an interactive terminal. JSON, quiet, redirected, and non-interactive output must remain plain and deterministic.
- Make operations idempotent when possible. A safe retry should converge on the intended state without duplicating tunnels, DNS, or config.
- Return actionable partial-success information when an operation changed state but could not finish a later step.

## Safety boundaries

- Never print, log, copy, commit, or expose origin-certificate tokens or tunnel credential contents.
- Delete DNS only when both the hostname and exact `<tunnel-id>.cfargotunnel.com` target prove ownership.
- Automatic `claim` may replace only another Cloudflare Tunnel CNAME. Unrelated DNS requires an explicit, inspected `--force` action.
- Destructive bulk removal and machine reset require confirmation. JSON automation must require an explicit confirmation flag.
- Stop or restart only the connector process proven to be managed by cfdev. Never stop a local application.
- Protect certificate changes, config changes, and multi-step remote cleanup with rollback where possible. If rollback is incomplete, report exactly what changed and how to recover.
- Installer and upgrade checksum verification must fail closed. Treat update-source environment overrides as trusted-operator inputs.
- Do not weaken signing, notarization, provenance, checksum, or pinned-action protections in release workflows.
- Keep the request inspector and gateway loopback-only and history memory-only. Never persist captured traffic, widen replay beyond the original configured localhost target, or retain redacted authorization/cookie values.
- Preserve transparent transport for streaming and upgraded connections. Disable body capture and replay when exact bytes cannot be retained without changing application behavior.

## Tests are required

Every behavior change or bug fix must add or update tests. Cover the public contract, not only helper functions.

Include the relevant cases:

- success, idempotent retry, invalid input, and expected failure;
- JSON and interactive behavior when they differ;
- rollback and partial-success paths for multi-step operations;
- exact DNS ownership and `claim` / `--force` conflict matrices;
- missing, expired, mismatched, and previously saved authorization states;
- foreground and background process transitions;
- transparent HTTP, streaming, and WebSocket proxying; exact-byte body capture, truncation, redaction, local-only replay, bounded eviction, and inspector process survival across mapping/tunnel reloads;
- Windows-specific path/process behavior and Unix permission assertions;
- bad checksums and update-source overrides for installer or upgrade changes.

Use temporary directories and local test servers. Tests must not require a real Cloudflare account, alter the user's cfdev state, or make destructive network calls.

## Documentation synchronization

Documentation is part of the change. Update only the files affected by the behavior, but do not leave them inconsistent.

- `CHANGELOG.md`: add every notable user-facing, security, release, or contributor-facing change under `Unreleased`.
- `README.md`: update commands, flags, examples, installation, platform behavior, configuration, security guidance, or user workflows when they change.
- `skills/cfdev/SKILL.md`: update whenever an agent should invoke cfdev differently, a command contract changes, a new safety condition appears, or troubleshooting guidance changes.
- `skills/cfdev/agents/openai.yaml`: update when the skill description, trigger surface, or default prompt changes.
- `DRAFT.md`: keep product decisions, architecture, command behavior, and acceptance criteria aligned with the implementation.
- `SECURITY.md`: update when credential handling, DNS ownership, destructive behavior, update integrity, release signing, or the trust model changes.
- CLI help and examples: update alongside command names, arguments, flags, aliases, and recovery hints.
- Installer and package files: update all affected platforms together when release assets, install locations, package metadata, or upgrade behavior changes.

An internal refactor with no observable contract change does not need artificial README or changelog edits.

## Verification before handoff

Run the checks relevant to the change. The normal full gate is:

```text
gofmt -w <changed-go-files>
go test ./...
go vet ./...
git diff --check
```

For installer or packaging changes, also validate the affected scripts and render package definitions. For release-workflow changes, verify YAML, action permissions, secret handling, platform matrices, artifact names, and the publish gate.

Before finishing:

1. Review the final diff for accidental secrets, personal domains, generated binaries, and unrelated edits.
2. Confirm tests exercise the failure mode that motivated the change.
3. Confirm README, help, changelog, draft, security guidance, and the repository skill agree where applicable.
4. State what was changed, what was verified, and any remaining risk or untested external dependency.

## Git and releases

- Do not commit generated binaries or local cfdev/Cloudflare state.
- Do not create a tag, GitHub Release, package publication, or announcement unless the user explicitly requests it.
- Keep commits focused and describe user-facing and security impact clearly.
- Release tags must come from the intended `main` commit after CI is green.
- A release is not complete until expected artifacts and checksums exist and the real installers have been smoke-tested.
