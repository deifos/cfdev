# Contributing

Thanks for helping with cfdev.

## Development

You need Go 1.24 or newer.

```bash
go test ./...
go vet ./...
gofmt -w .
go build -trimpath -o dist/cfdev ./cmd/cfdev
```

Keep the dependency set empty unless there is a strong reason to add a module. Prefer small, focused pull requests with tests for parsing, config, DNS ownership, and upgrade checksum behavior.

## Pull requests

1. Describe the user-facing change and any security impact.
2. Include or update tests when behavior changes.
3. Add user-facing changes under `Unreleased` in `CHANGELOG.md`.
4. Keep docs and the agent skill in `skills/cfdev` aligned with command behavior.
5. Do not commit secrets, origin certificates, tunnel credentials, or `dist/` binaries.

## Security issues

Report vulnerabilities privately through [SECURITY.md](SECURITY.md). Do not open a public issue for credential, DNS, or supply-chain problems.
