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

func TestIngestRedactsBeforeRawAppend(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), dir+"/index/vigil.db")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	projects := project.NewService(db)
	created, err := projects.CreateProject("redaction-demo")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	rawStore := raw.NewStore(dir+"/logs", 1024*1024)
	worker := index.NewWorker(db, rawStore)
	service := NewService(projects, rawStore, db, worker, nil, event.DefaultMaxPayload, nil)
	payload, err := json.Marshal(map[string]any{
		"schema_version": event.SchemaVersion,
		"project_id":     created.Project.ID,
		"kind":           "log",
		"ts":             time.Now().UTC().Format(time.RFC3339),
		"source":         "ingest-test",
		"level":          "error",
		"name":           "secret.event",
		"attrs": map[string]any{
			"route":         "/checkout",
			"authorization": "Bearer secret-token-value",
		},
		"body": map[string]any{
			"message":  "user@example.com failed checkout",
			"password": "hunter2",
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if _, err := service.Ingest("Bearer "+created.IngestKey, payload); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	var stored *event.StoredEvent
	if err := rawStore.Replay(context.Background(), func(ev *event.StoredEvent) error {
		stored = ev
		return nil
	}); err != nil {
		t.Fatalf("replay raw events: %v", err)
	}
	if stored == nil {
		t.Fatal("expected raw event")
	}

	var attrs map[string]any
	if err := json.Unmarshal(stored.Attrs, &attrs); err != nil {
		t.Fatalf("decode attrs: %v", err)
	}
	if attrs["route"] != "/checkout" {
		t.Fatalf("expected normal attr preserved, got %v", attrs)
	}
	if attrs["authorization"] != "[REDACTED]" {
		t.Fatalf("expected authorization redacted before raw append, got %v", attrs)
	}

	var body map[string]any
	if err := json.Unmarshal(stored.Body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["password"] != "[REDACTED]" {
		t.Fatalf("expected password redacted before raw append, got %v", body)
	}
	if body["message"] != "[REDACTED] failed checkout" {
		t.Fatalf("expected email redacted before raw append, got %v", body)
	}

	stats := service.Stats()
	if stats.Redaction.FieldsRedacted < 2 {
		t.Fatalf("expected redaction stats to count fields, got %+v", stats.Redaction)
	}
	if stats.Redaction.EmailsRedacted < 1 {
		t.Fatalf("expected redaction stats to count emails, got %+v", stats.Redaction)
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
