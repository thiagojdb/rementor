# Contributing

Rementor is a local developer tool with a small control-plane surface. Changes
should preserve loopback-only defaults, validate all values before generating
nginx configuration, and keep the Protocol Buffers contract as the source of
truth for RPC clients.

## Development Setup

Prerequisites:

- Go 1.25
- Node.js 22
- Buf
- `protoc-gen-go` and `protoc-gen-connect-go`

```bash
make setup
make generate
make dev
```

## Required Checks

Run these before opening a pull request:

```bash
buf lint
make generate
git diff --exit-code
go vet ./...
go test -race ./...
cd web/frontend
npm ci
npm audit
npm run typecheck
npm run build
cd ../..
git diff --exit-code
```

Generated Go and TypeScript RPC code and the embedded frontend bundle are
committed so a clean Go checkout can build without Node.js.

## Pull Requests

- Keep changes focused and explain user-visible behavior.
- Add regression tests for routing, persistence, validation, or RPC changes.
- Use neutral mock domains and data in tests and documentation.
- Never commit credentials, private URLs, local databases, logs, or machine
  specific agent configuration.
