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

## Retention behavior

Retention is project-first and day-based:

- raw events live under `vigil-data/logs/{project_id}/{YYYY-MM-DD}/`
- retention deletes whole day folders older than the configured UTC cutoff
- after a real delete, Vigil rebuilds `vigil-data/index/vigil.db` from the remaining raw NDJSON

If retention is disabled, Vigil keeps data forever.
