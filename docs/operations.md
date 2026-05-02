# Operations

## Runtime data layout

```text
vigil-data/
├── logs/
│   └── {project_id}/
│       └── YYYY-MM-DD/
│           ├── 0001.ndjson
│           ├── 0002.ndjson
│           └── ...
├── index/
│   └── vigil.db
```

## Durability boundary

An ingest request is successful only after:

1. the bearer key is authenticated
2. the event payload validates
3. the event is normalized
4. the raw NDJSON append succeeds

Search, traces, and stats update asynchronously after that.

## Rebuild model

The SQLite read models can be rebuilt from raw NDJSON events when the index worker falls behind or needs to rescan.

## Retention sweeps

Retention is off by default. When enabled:

1. Vigil computes the UTC day cutoff from `VIGIL_RETENTION_DAYS`
2. expired raw day folders are deleted under each project
3. the SQLite read model is cleared
4. the remaining raw NDJSON is replayed into the index again

Use `VIGIL_RETENTION_DRY_RUN=true` first if you want to verify the cutoff and deletion counts before allowing actual removal.
