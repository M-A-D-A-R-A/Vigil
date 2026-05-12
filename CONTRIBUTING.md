# Contributing

Thanks for taking a look at Vigil. The project is early alpha, so small, focused contributions are the most useful right now.

## Development Setup

Install Go and Bun, then run:

```sh
make ui-install
make ui-build
make test
make test-python
```

Start the server locally:

```sh
make run
```

For frontend development:

```sh
make run
make ui-dev
```

The Go server listens on `http://localhost:8080`. The Vite dev server listens on `http://localhost:5173`.

## Before Opening A PR

Run the relevant checks:

```sh
make test
make test-python
make ui-build
```

For user-facing UI or first-run flow changes, also run:

```sh
make smoke
```

## Contribution Style

- Keep changes focused and easy to review.
- Prefer existing project patterns over new abstractions.
- Keep the raw NDJSON event log as the source of truth.
- Treat SQLite as a derived read model that can be rebuilt.
- Add tests when changing ingest, storage, query behavior, retention, or CLI behavior.
- Avoid introducing heavyweight services unless they are optional integrations.

## Issues

Use GitHub Issues for concrete work items. Good issue titles are specific and implementation-shaped, for example:

- `Add SSE live tail endpoint`
- `Add query parser spans for invalid filters`
- `Add TypeScript SDK env-based client`

Use the roadmap for direction, but prefer Issues for active planning and discussion.

## Contact

For security reports, use the process in [SECURITY.md](SECURITY.md). For other maintainer contact, email `andoriyanishant@gmail.com`.
