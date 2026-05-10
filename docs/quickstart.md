# Quickstart

1. Install Go and Bun.
2. From the repo root, run `make ui-install`.
3. Run `make test`.
4. Run `make ui-build`.
5. Run `make run`.
6. Open `http://localhost:8080`.
7. Create a project in the UI.
8. Copy the one-time ingest key.
9. Paste the generated `curl` example to send your first event.

CLI setup path:

1. Terminal 1: `make run`
2. Terminal 2: `make build`
3. Run `bin/vigil init -project my-app`
4. Run the printed ingest command
5. Use `bin/vigil status` to confirm the active project

Install path:

```sh
curl -fsSL https://raw.githubusercontent.com/M-A-D-A-R-A/Vigil/main/scripts/install.sh | sh
vigil serve
vigil init -project my-app
```

Optional retention setup:

1. Export `VIGIL_RETENTION_ENABLED=true`
2. Export `VIGIL_RETENTION_DAYS=30`
3. Export `VIGIL_RETENTION_SWEEP_INTERVAL=1h`
4. Start with `VIGIL_RETENTION_DRY_RUN=true` if you want to inspect the sweep behavior first

For local frontend development without rebuilding the embedded UI:

1. Terminal 1: `make run`
2. Terminal 2: `make ui-dev`
3. Open `http://localhost:5173`
