package index

import (
	"context"
	"fmt"
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

	baseTS := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	eventCount := batchSize + 5
	for i := 0; i < eventCount; i++ {
		ev := &event.StoredEvent{
			EventID:    fmt.Sprintf("evt_rebuild_%03d", i),
			ReceivedAt: baseTS.Add(time.Duration(i) * time.Millisecond).Format(time.RFC3339Nano),
			Envelope: event.Envelope{
				SchemaVersion: 1,
				ProjectID:     "proj_1",
				Kind:          event.KindTrace,
				TS:            baseTS.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano),
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
	}

	worker.ScheduleRebuild()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		result, err := db.ListLogs(queryLogFilters("proj_1"))
		if err == nil && result.Total == eventCount {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	result, err := db.ListLogs(queryLogFilters("proj_1"))
	if err != nil {
		t.Fatalf("list logs after rebuild: %v", err)
	}
	if result.Total != eventCount {
		t.Fatalf("expected %d indexed events after rebuild, got %d", eventCount, result.Total)
	}
}

func TestWorkerIndexesQueuedEventsInBatches(t *testing.T) {
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

	baseTS := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		ok := worker.Enqueue(&event.StoredEvent{
			EventID:    fmt.Sprintf("evt_queue_%03d", i),
			ReceivedAt: baseTS.Add(time.Duration(i) * time.Millisecond).Format(time.RFC3339Nano),
			Envelope: event.Envelope{
				SchemaVersion: 1,
				ProjectID:     "proj_1",
				Kind:          event.KindLog,
				TS:            baseTS.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano),
				Source:        "test",
				Level:         "info",
				Name:          "queued.event",
				Attrs:         []byte(`{"batch":true}`),
				Body:          []byte(`{"message":"queued"}`),
			},
		})
		if !ok {
			t.Fatal("expected queue enqueue to succeed")
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		result, err := db.ListLogs(queryLogFilters("proj_1"))
		if err == nil && result.Total == 20 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	result, err := db.ListLogs(queryLogFilters("proj_1"))
	if err != nil {
		t.Fatalf("list logs after queue batch: %v", err)
	}
	if result.Total != 20 {
		t.Fatalf("expected 20 indexed queued events, got %d", result.Total)
	}
}

func TestWorkerStatusTracksQueueDropsAndRebuildPending(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), dir+"/index/vigil.db")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	rawStore := raw.NewStore(dir+"/logs", 1024*1024)
	worker := NewWorker(db, rawStore)

	baseTS := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	capacity := worker.Status().QueueCapacity
	for i := 0; i < capacity; i++ {
		if ok := worker.Enqueue(testStoredEvent(fmt.Sprintf("evt_queue_full_%03d", i), baseTS)); !ok {
			t.Fatalf("expected enqueue %d to fit in queue", i)
		}
	}

	if ok := worker.Enqueue(testStoredEvent("evt_queue_drop", baseTS)); ok {
		t.Fatal("expected enqueue to fail after filling queue")
	}

	status := worker.Status()
	if status.QueueDepth != capacity {
		t.Fatalf("expected queue depth %d, got %d", capacity, status.QueueDepth)
	}
	if status.EnqueueDrops != 1 {
		t.Fatalf("expected one enqueue drop, got %d", status.EnqueueDrops)
	}
	if !status.RebuildPending {
		t.Fatal("expected rebuild to be pending after enqueue drop")
	}
	if status.RebuildRunning {
		t.Fatal("did not expect rebuild running before worker start")
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

func testStoredEvent(id string, ts time.Time) *event.StoredEvent {
	return &event.StoredEvent{
		EventID:    id,
		ReceivedAt: ts.Format(time.RFC3339Nano),
		Envelope: event.Envelope{
			SchemaVersion: event.SchemaVersion,
			ProjectID:     "proj_1",
			Kind:          event.KindLog,
			TS:            ts.Format(time.RFC3339Nano),
			Source:        "test",
			Level:         "info",
			Name:          "queued.event",
			Attrs:         []byte(`{"queued":true}`),
			Body:          []byte(`{"message":"queued"}`),
		},
	}
}
