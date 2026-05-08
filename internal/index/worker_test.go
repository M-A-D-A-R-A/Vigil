package index

import (
	"context"
	"testing"
	"time"

	"vigil/internal/event"
	"vigil/internal/query"
	"vigil/internal/store/raw"
	"vigil/internal/store/sqlite"
)

func TestWorkerRebuildIndexesRawEvents(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), dir+"/index/vigil.db")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if _, err := db.CreateProject("proj_1", "demo", "hash", time.Now().UTC()); err != nil {
		t.Fatalf("create project: %v", err)
	}

	rawStore := raw.NewStore(dir+"/logs", 1024*1024)
	worker := NewWorker(db, rawStore)
	worker.Start()
	defer worker.Close()

	ev := &event.StoredEvent{
		EventID:    "evt_rebuild",
		ReceivedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Envelope: event.Envelope{
			SchemaVersion: 1,
			ProjectID:     "proj_1",
			Kind:          event.KindTrace,
			TS:            "2026-05-02T10:00:00Z",
			Source:        "test",
			TraceID:       "trace-1",
			Name:          "trace.step",
			Attrs:         []byte(`{"total_tokens": 9}`),
			Body:          []byte(`{"message":"hi"}`),
		},
	}
	if _, err := rawStore.Append(ev); err != nil {
		t.Fatalf("append raw event: %v", err)
	}

	worker.ScheduleRebuild()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		result, err := db.ListLogs(queryLogFilters("proj_1"))
		if err == nil && result.Total == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	result, err := db.ListLogs(queryLogFilters("proj_1"))
	if err != nil {
		t.Fatalf("list logs after rebuild: %v", err)
	}
	if result.Total != 1 {
		t.Fatalf("expected 1 indexed event after rebuild, got %d", result.Total)
	}
}

func queryLogFilters(projectID string) query.LogFilters {
	ts := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	return query.LogFilters{
		RangeFilters: query.RangeFilters{
			ProjectID: projectID,
			From:      ts.Add(-time.Hour),
			To:        ts.Add(time.Hour),
			Page:      1,
			Limit:     10,
		},
	}
}
