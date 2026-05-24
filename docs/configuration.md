# Configuration

## Environment variables

- `VIGIL_ADDR` - HTTP listen address, defaults to `:8080`
- `VIGIL_DATA_DIR` - runtime data directory, defaults to `./vigil-data`
- `VIGIL_MAX_EVENT_BYTES` - max accepted ingest payload size, defaults to `1048576`
- `VIGIL_SEGMENT_MAX_BYTES` - raw segment rollover size, defaults to `10485760`
- `VIGIL_RETENTION_ENABLED` - turn on raw-data retention sweeps, defaults to `false`
- `VIGIL_RETENTION_DAYS` - keep this many UTC day folders when retention is enabled, defaults to `30`
- `VIGIL_RETENTION_SWEEP_INTERVAL` - how often retention runs, defaults to `1h`
- `VIGIL_RETENTION_DRY_RUN` - report what would be deleted without removing data, defaults to `false`
- `VIGIL_REDACTION_ENABLED` - redact obvious secrets before raw append, defaults to `true`
- `VIGIL_REDACTION_EMAILS` - redact email addresses as PII, defaults to `true`
- `VIGIL_REDACTION_MAX_DEPTH` - max JSON nesting depth to redact, defaults to `8`
- `VIGIL_REDACTION_MAX_STRING_LENGTH` - max characters inspected per string, defaults to `8192`
- `VIGIL_CONFIG_PATH` - optional `vigil` CLI config file path override

## Redaction behavior

Redaction runs before Vigil writes raw NDJSON, so obvious secrets do not become part of the source of truth. The first pass redacts sensitive key names such as `password`, `token`, `secret`, `api_key`, `authorization`, `cookie`, `access_token`, and `refresh_token`; it also redacts common secret-looking values such as bearer tokens, JWTs, provider-style API keys, connection strings with credentials, high-entropy strings, and emails when email redaction is enabled.

Normal fields are preserved. Redaction can be disabled for local-only debugging with `VIGIL_REDACTION_ENABLED=false`, and email redaction can be disabled separately with `VIGIL_REDACTION_EMAILS=false`.

## Retention behavior

Retention is project-first and day-based:

- raw events live under `vigil-data/logs/{project_id}/{YYYY-MM-DD}/`
- retention deletes whole day folders older than the configured UTC cutoff
- after a real delete, Vigil rebuilds `vigil-data/index/vigil.db` from the remaining raw NDJSON

If retention is disabled, Vigil keeps data forever.

## CLI config

`vigil` stores the active server, active project, and last generated ingest key in a local JSON config file. By default it uses the platform config directory, for example `~/Library/Application Support/vigil/config.json` on macOS.

Use `VIGIL_CONFIG_PATH` for project-local or test-specific config:

```sh
VIGIL_CONFIG_PATH=.vigil.json vigil init -project my-app
```

`vigil init` defaults to `http://localhost:8080` when `-server` is omitted, then reuses the saved server URL on later commands. It also writes this managed SDK block into the current project’s `.env`, preserving the rest of the file:

```env
# BEGIN VIGIL
VIGIL_BASE_URL=http://localhost:8080
VIGIL_PROJECT_ID=proj_...
VIGIL_INGEST_KEY=vigil_...
# END VIGIL
```

Use `vigil init -env-file PATH` to write a different app env file. Server runtime settings such as retention and data directories stay in the server environment, not in the app `.env`.
