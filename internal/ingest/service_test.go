package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"vigil/internal/event"
	"vigil/internal/index"
	"vigil/internal/project"
	"vigil/internal/store/raw"
	"vigil/internal/store/sqlite"
)

func TestIngestReportsIndexedAsyncFalseWhenQueueDrops(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), dir+"/index/vigil.db")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	projects := project.NewService(db)
	created, err := projects.CreateProject("demo")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	rawStore := raw.NewStore(dir+"/logs", 1024*1024)
	worker := index.NewWorker(db, rawStore)
	capacity := worker.Status().QueueCapacity
	for i := 0; i < capacity; i++ {
		if ok := worker.Enqueue(testQueuedEvent(fmt.Sprintf("evt_fill_%03d", i))); !ok {
			t.Fatalf("expected enqueue %d to fill queue", i)
		}
	}

	service := NewService(projects, rawStore, db, worker, nil, event.DefaultMaxPayload, nil)
	payload, err := json.Marshal(map[string]any{
		"schema_version": event.SchemaVersion,
		"project_id":     created.Project.ID,
		"kind":           "log",
		"ts":             time.Now().UTC().Format(time.RFC3339),
		"source":         "ingest-test",
		"level":          "info",
		"name":           "queue.overflow",
		"attrs":          map[string]any{"queue": "full"},
		"body":           map[string]any{"message": "queued through rebuild fallback"},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	result, err := service.Ingest("Bearer "+created.IngestKey, payload)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if result.IndexedAsync {
		t.Fatal("expected indexed_async false when worker queue rejects event")
	}

	stats := service.Stats()
	if stats.TotalAccepted != 1 {
		t.Fatalf("expected one accepted ingest, got %d", stats.TotalAccepted)
	}
	if stats.RecentEvents != 1 {
		t.Fatalf("expected one recent ingest, got %d", stats.RecentEvents)
	}

	indexStatus := worker.Status()
	if indexStatus.EnqueueDrops != 1 {
		t.Fatalf("expected one enqueue drop, got %d", indexStatus.EnqueueDrops)
	}
	if !indexStatus.RebuildPending {
		t.Fatal("expected rebuild pending after enqueue drop")
	}
}

func testQueuedEvent(id string) *event.StoredEvent {
	now := time.Now().UTC()
	return &event.StoredEvent{
		EventID:    id,
		ReceivedAt: now.Format(time.RFC3339Nano),
		Envelope: event.Envelope{
			SchemaVersion: event.SchemaVersion,
			ProjectID:     "proj_1",
			Kind:          event.KindLog,
			TS:            now.Format(time.RFC3339Nano),
			Source:        "ingest-test",
			Level:         "info",
			Name:          "queued.event",
			Attrs:         []byte(`{}`),
			Body:          []byte(`null`),
		},
	}
}
