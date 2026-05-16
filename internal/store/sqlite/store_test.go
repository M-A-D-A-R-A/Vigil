package sqlite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"vigil/internal/event"
	"vigil/internal/query"
)

func TestUpsertEventsInsertsMultipleLogs(t *testing.T) {
	store := openTestStore(t)
	baseTS := time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)

	events := []*event.StoredEvent{
		testEvent("evt_1", event.KindLog, baseTS, map[string]string{"level": "info", "message": "checkout started"}),
		testEvent("evt_2", event.KindLog, baseTS.Add(time.Second), map[string]string{"level": "error", "message": "checkout failed"}),
		testEvent("evt_3", event.KindLog, baseTS.Add(2*time.Second), map[string]string{"level": "info", "message": "checkout recovered"}),
	}

	inserted, latest, err := store.UpsertEvents(events)
	if err != nil {
		t.Fatalf("upsert events: %v", err)
	}
	if inserted != 3 {
		t.Fatalf("expected 3 inserted events, got %d", inserted)
	}
	if latest != events[2].ReceivedAt {
		t.Fatalf("expected latest received_at %s, got %s", events[2].ReceivedAt, latest)
	}

	result, err := store.ListLogs(testLogFilters(baseTS, query.LogFilters{Query: "checkout"}))
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	if result.Total != 3 {
		t.Fatalf("expected 3 checkout logs, got %d", result.Total)
	}
}

func TestUpsertEventsSkipsDuplicates(t *testing.T) {
	store := openTestStore(t)
	baseTS := time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)
	ev := testEvent("evt_duplicate", event.KindLog, baseTS, map[string]string{
		"level":   "info",
		"message": "duplicate checkout marker",
	})

	inserted, _, err := store.UpsertEvents([]*event.StoredEvent{ev, ev})
	if err != nil {
		t.Fatalf("upsert duplicate events: %v", err)
	}
	if inserted != 1 {
		t.Fatalf("expected 1 inserted duplicate event, got %d", inserted)
	}

	logs, err := store.ListLogs(testLogFilters(baseTS, query.LogFilters{Query: "duplicate"}))
	if err != nil {
		t.Fatalf("list duplicate logs: %v", err)
	}
	if logs.Total != 1 {
		t.Fatalf("expected 1 duplicate log, got %d", logs.Total)
	}

	stats, err := store.GetStats(testRangeFilters(baseTS))
	if err != nil {
		t.Fatalf("get duplicate stats: %v", err)
	}
	if stats.TotalEvents != 1 {
		t.Fatalf("expected duplicate stats total 1, got %d", stats.TotalEvents)
	}
}

func TestUpsertEventsMixedBatchUpdatesReadModels(t *testing.T) {
	store := openTestStore(t)
	baseTS := time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)

	logEvent := testEvent("evt_log", event.KindLog, baseTS, map[string]string{
		"level":   "info",
		"message": "request complete",
	})
	traceEvent := testEvent("evt_trace", event.KindTrace, baseTS.Add(time.Second), map[string]string{
		"level":   "info",
		"message": "trace step",
	})
	traceEvent.TraceID = "trace_1"
	traceEvent.SpanID = "span_1"
	traceEvent.Attrs = []byte(`{"total_tokens":9,"cost_usd":0.25}`)
	metricEvent := testEvent("evt_metric", event.KindMetric, baseTS.Add(2*time.Second), map[string]string{
		"level":   "warn",
		"message": "queue depth",
	})
	metricEvent.Attrs = []byte(`{"value":7}`)

	inserted, _, err := store.UpsertEvents([]*event.StoredEvent{logEvent, traceEvent, metricEvent})
	if err != nil {
		t.Fatalf("upsert mixed events: %v", err)
	}
	if inserted != 3 {
		t.Fatalf("expected 3 inserted mixed events, got %d", inserted)
	}

	logs, err := store.ListLogs(testLogFilters(baseTS, query.LogFilters{}))
	if err != nil {
		t.Fatalf("list mixed logs: %v", err)
	}
	if logs.Total != 3 {
		t.Fatalf("expected 3 events in log list, got %d", logs.Total)
	}

	traces, err := store.ListTraces(testRangeFilters(baseTS))
	if err != nil {
		t.Fatalf("list traces: %v", err)
	}
	if traces.Total != 1 {
		t.Fatalf("expected 1 trace summary, got %d", traces.Total)
	}
	if len(traces.Traces) != 1 || traces.Traces[0].EventCount != 1 || len(traces.Traces[0].Events) != 1 {
		t.Fatalf("expected one trace with one event, got %+v", traces.Traces)
	}

	stats, err := store.GetStats(testRangeFilters(baseTS))
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}
	if stats.TotalEvents != 3 {
		t.Fatalf("expected stats total 3, got %d", stats.TotalEvents)
	}
	if stats.TokenTotal != 9 {
		t.Fatalf("expected token total 9, got %v", stats.TokenTotal)
	}
	if stats.CostTotal != 0.25 {
		t.Fatalf("expected cost total 0.25, got %v", stats.CostTotal)
	}
}

func TestListLogsStructuredQueryCoreAndJSONFields(t *testing.T) {
	store := openTestStore(t)
	baseTS := time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)

	events := []*event.StoredEvent{
		testEvent("evt_api_ok", event.KindLog, baseTS, map[string]string{"level": "info", "message": "login ok"}),
		testEvent("evt_api_error", event.KindLog, baseTS.Add(time.Second), map[string]string{"level": "error", "message": "login failed"}),
		testEvent("evt_worker_error", event.KindLog, baseTS.Add(2*time.Second), map[string]string{"level": "error", "message": "cleanup failed"}),
	}
	events[0].Source = "api"
	events[0].Name = "request.completed"
	events[0].Attrs = []byte(`{"route":"/login","status":200,"cached":true}`)
	events[1].Source = "api"
	events[1].Name = "request.failed"
	events[1].TraceID = "trace_login"
	events[1].Attrs = []byte(`{"route":"/login","status":503,"cached":false}`)
	events[2].Source = "worker"
	events[2].Name = "job.failed"
	events[2].Attrs = []byte(`{"job":"cleanup","status":500}`)

	if _, _, err := store.UpsertEvents(events); err != nil {
		t.Fatalf("upsert events: %v", err)
	}

	cases := []struct {
		name  string
		query string
		total int
	}{
		{name: "core fields", query: `level = "error" && source = "api"`, total: 1},
		{name: "json string and number", query: `attrs.route = "/login" && attrs.status >= 500`, total: 1},
		{name: "json bool", query: `attrs.cached = true`, total: 1},
		{name: "contains", query: `body.message ~= "failed"`, total: 2},
		{name: "trace id", query: `trace_id = "trace_login"`, total: 1},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			logs, err := store.ListLogs(testStructuredLogFilters(t, baseTS, tt.query, query.LogFilters{}))
			if err != nil {
				t.Fatalf("list logs: %v", err)
			}
			if logs.Total != tt.total {
				t.Fatalf("expected total %d, got %d", tt.total, logs.Total)
			}
		})
	}
}

func TestListLogsStructuredQueryTimestampAndCombinedFilters(t *testing.T) {
	store := openTestStore(t)
	baseTS := time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)

	events := []*event.StoredEvent{
		testEvent("evt_old", event.KindLog, baseTS, map[string]string{"level": "info", "message": "checkout old"}),
		testEvent("evt_new", event.KindLog, baseTS.Add(time.Hour), map[string]string{"level": "error", "message": "checkout new"}),
	}
	if _, _, err := store.UpsertEvents(events); err != nil {
		t.Fatalf("upsert events: %v", err)
	}

	logs, err := store.ListLogs(testStructuredLogFilters(t, baseTS, `timestamp > "2026-05-08T10:30:00Z"`, query.LogFilters{
		Query: "checkout",
		Kind:  "log",
	}))
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	if logs.Total != 1 {
		t.Fatalf("expected one combined structured/FTS match, got %d", logs.Total)
	}
	if logs.Events[0].EventID != "evt_new" {
		t.Fatalf("expected evt_new, got %s", logs.Events[0].EventID)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), t.TempDir()+"/index/vigil.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})
	if _, err := store.CreateProject("proj_1", "test project", "hash", time.Now().UTC()); err != nil {
		t.Fatalf("create project: %v", err)
	}
	return store
}

func testEvent(id string, kind event.Kind, ts time.Time, values map[string]string) *event.StoredEvent {
	level := values["level"]
	message := values["message"]
	return &event.StoredEvent{
		EventID:    id,
		ReceivedAt: ts.Add(time.Millisecond).UTC().Format(time.RFC3339Nano),
		Envelope: event.Envelope{
			SchemaVersion: event.SchemaVersion,
			ProjectID:     "proj_1",
			Kind:          kind,
			TS:            ts.UTC().Format(time.RFC3339Nano),
			Source:        "sqlite-test",
			Level:         level,
			Name:          fmt.Sprintf("%s.event", kind),
			Attrs:         []byte(`{"route":"/checkout"}`),
			Body:          []byte(fmt.Sprintf(`{"message":%q}`, message)),
		},
	}
}

func testLogFilters(baseTS time.Time, overrides query.LogFilters) query.LogFilters {
	filters := overrides
	filters.RangeFilters = testRangeFilters(baseTS)
	return filters
}

func testStructuredLogFilters(t *testing.T, baseTS time.Time, raw string, overrides query.LogFilters) query.LogFilters {
	t.Helper()
	filters := testLogFilters(baseTS, overrides)
	structured, err := query.ParseStructuredQuery(raw, baseTS.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("parse structured query: %v", err)
	}
	filters.StructuredQuery = raw
	filters.Structured = structured
	return filters
}

func testRangeFilters(baseTS time.Time) query.RangeFilters {
	return query.RangeFilters{
		ProjectID: "proj_1",
		From:      baseTS.Add(-time.Hour),
		To:        baseTS.Add(time.Hour),
		Page:      1,
		Limit:     100,
	}
}
