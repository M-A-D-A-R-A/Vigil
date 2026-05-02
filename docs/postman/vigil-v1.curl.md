# Vigil V1 cURL Reference

Set these once before using the commands:

```bash
export VIGIL_BASE_URL="http://localhost:8080"
export VIGIL_PROJECT_NAME="curl-demo"
export VIGIL_PROJECT_ID="proj_replace_me"
export VIGIL_INGEST_KEY="vigil_replace_me"
export VIGIL_FROM="$(date -u -v-5M +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || python3 - <<'PY'
from datetime import datetime, timedelta, timezone
print((datetime.now(timezone.utc) - timedelta(minutes=5)).strftime("%Y-%m-%dT%H:%M:%SZ"))
PY
)"
export VIGIL_TO="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
```

## Health

```bash
curl "$VIGIL_BASE_URL/api/health"
```

## Create project

```bash
curl -X POST "$VIGIL_BASE_URL/api/projects" \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"$VIGIL_PROJECT_NAME\"
  }"
```

## List projects

```bash
curl "$VIGIL_BASE_URL/api/projects"
```

## Regenerate project key

```bash
curl -X POST "$VIGIL_BASE_URL/api/projects/$VIGIL_PROJECT_ID/keys/regenerate"
```

## Ingest log event

```bash
curl -X POST "$VIGIL_BASE_URL/api/ingest" \
  -H "Authorization: Bearer $VIGIL_INGEST_KEY" \
  -H "Content-Type: application/json" \
  -d "{
    \"schema_version\": 1,
    \"project_id\": \"$VIGIL_PROJECT_ID\",
    \"kind\": \"log\",
    \"ts\": \"$(date -u +"%Y-%m-%dT%H:%M:%SZ")\",
    \"source\": \"curl\",
    \"level\": \"info\",
    \"name\": \"hello.vigil.log\",
    \"attrs\": {
      \"route\": \"/health\",
      \"region\": \"local\"
    },
    \"body\": {
      \"message\": \"hello from curl log\"
    }
  }"
```

## Ingest trace event

```bash
curl -X POST "$VIGIL_BASE_URL/api/ingest" \
  -H "Authorization: Bearer $VIGIL_INGEST_KEY" \
  -H "Content-Type: application/json" \
  -d "{
    \"schema_version\": 1,
    \"project_id\": \"$VIGIL_PROJECT_ID\",
    \"kind\": \"trace\",
    \"ts\": \"$(date -u +"%Y-%m-%dT%H:%M:%SZ")\",
    \"source\": \"curl\",
    \"trace_id\": \"trace-demo-001\",
    \"span_id\": \"span-demo-001\",
    \"level\": \"info\",
    \"name\": \"request.completed\",
    \"attrs\": {
      \"method\": \"GET\",
      \"path\": \"/api/demo\",
      \"total_tokens\": 42,
      \"cost_usd\": 0.0021
    },
    \"body\": {
      \"message\": \"trace event from curl\"
    }
  }"
```

## Ingest metric event

```bash
curl -X POST "$VIGIL_BASE_URL/api/ingest" \
  -H "Authorization: Bearer $VIGIL_INGEST_KEY" \
  -H "Content-Type: application/json" \
  -d "{
    \"schema_version\": 1,
    \"project_id\": \"$VIGIL_PROJECT_ID\",
    \"kind\": \"metric\",
    \"ts\": \"$(date -u +"%Y-%m-%dT%H:%M:%SZ")\",
    \"source\": \"curl\",
    \"name\": \"queue.depth\",
    \"attrs\": {
      \"value\": 7,
      \"unit\": \"count\",
      \"queue\": \"jobs\"
    },
    \"body\": {
      \"message\": \"metric event from curl\"
    }
  }"
```

## Query logs

```bash
curl "$VIGIL_BASE_URL/api/logs?project_id=$VIGIL_PROJECT_ID&from=$VIGIL_FROM&to=$VIGIL_TO&page=1&limit=50"
```

## Query traces

```bash
curl "$VIGIL_BASE_URL/api/traces?project_id=$VIGIL_PROJECT_ID&from=$VIGIL_FROM&to=$VIGIL_TO&page=1&limit=20"
```

## Query stats

```bash
curl "$VIGIL_BASE_URL/api/stats?project_id=$VIGIL_PROJECT_ID&from=$VIGIL_FROM&to=$VIGIL_TO&page=1&limit=30"
```
