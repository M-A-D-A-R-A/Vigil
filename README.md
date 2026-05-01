# Vigil

Vigil is a local-first observability box for side projects.

This repository is a single monorepo for:

- the Go backend
- the React/Vite UI
- tests
- user-facing docs

## Quick start

1. Install Go and Bun.
2. Run `bun install` in `ui/`.
3. Run `make ui-build`.
4. Run `make run`.

The app starts on `http://localhost:8080`.

## Useful commands

- `make run` - run the Go server locally
- `make ui-dev` - start the Vite dev server
- `make ui-build` - build the frontend into `web/dist`
- `make build` - build the Go binary into `bin/vigil`
- `make size` - print Go binary size, UI bundle size, and combined shipped size
- `make size-release` - print stripped release binary size, UI bundle size, and combined release size
- `make build-linux-arm64` - build a Raspberry Pi 4 friendly Linux ARM64 release binary

## Repository layout

- `cmd/vigil` - binary entrypoint
- `internal/` - backend application code
- `ui/` - React + TypeScript + Vite frontend
- `web/dist` - built frontend assets
- `docs/` - usage and operations docs
- `test/` - integration, e2e, and fixtures
