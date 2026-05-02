package retention

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"vigil/internal/config"
	"vigil/internal/event"
	"vigil/internal/index"
	"vigil/internal/query"
	"vigil/internal/store/raw"
	"vigil/internal/store/sqlite"
)

func TestRunOnceAtDryRunOnlyReports(t *testing.T) {
	dataDir := t.TempDir()
	ctx := context.Background()

	db, err := sqlite.Open(ctx, filepath.Join(dataDir, "index", "vigil.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	rawStore := raw.NewStore(filepath.Join(dataDir, "logs"), 1024)
	worker := index.NewWorker(db, rawStore)
	worker.Start()
	defer worker.Close()

	appendStoredEvent(t, rawStore, worker, "proj_1", "evt_old", time.Now().UTC().AddDate(0, 0, -14), "retention.old")
	waitForLogCount(t, db, 1)

	engine := New(config.RetentionConfig{
		Enabled:       true,
		Days:          7,
		SweepInterval: time.Hour,
		DryRun:        true,
	}, rawStore, worker, &sync.RWMutex{})

	runAt := time.Now().UTC()
	if err := engine.RunOnceAt(ctx, runAt); err != nil {
		t.Fatalf("run dry-run retention: %v", err)
	}

	status := engine.Status()
	if status.LastDeletedDayDirs != 1 {
		t.Fatalf("expected 1 deletable day dir, got %d", status.LastDeletedDayDirs)
	}

	logs, err := db.ListLogs(query.LogFilters{
		RangeFilters: query.RangeFilters{
			From:  runAt.AddDate(0, 0, -30),
			To:    runAt.Add(time.Hour),
			Page:  1,
			Limit: 50,
		},
	})
	if err != nil {
		t.Fatalf("list logs after dry-run: %v", err)
	}
	if logs.Total != 1 {
		t.Fatalf("expected dry-run to keep indexed event, got %d", logs.Total)
	}
}

func TestRunOnceAtPrunesOldRawAndRebuildsIndex(t *testing.T) {
	dataDir := t.TempDir()
	ctx := context.Background()

	db, err := sqlite.Open(ctx, filepath.Join(dataDir, "index", "vigil.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	rawStore := raw.NewStore(filepath.Join(dataDir, "logs"), 1024)
	worker := index.NewWorker(db, rawStore)
	worker.Start()
	defer worker.Close()

	oldTS := time.Now().UTC().AddDate(0, 0, -14)
	newTS := time.Now().UTC()
	appendStoredEvent(t, rawStore, worker, "proj_1", "evt_old", oldTS, "retention.old")
	appendStoredEvent(t, rawStore, worker, "proj_1", "evt_new", newTS, "retention.new")
	waitForLogCount(t, db, 2)

	engine := New(config.RetentionConfig{
		Enabled:       true,
		Days:          7,
		SweepInterval: time.Hour,
		DryRun:        false,
	}, rawStore, worker, &sync.RWMutex{})

	if err := engine.RunOnceAt(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("run retention: %v", err)
	}

	logs, err := db.ListLogs(query.LogFilters{
		RangeFilters: query.RangeFilters{
			From:  time.Now().UTC().AddDate(0, 0, -30),
			To:    time.Now().UTC().Add(time.Hour),
			Page:  1,
			Limit: 50,
		},
	})
	if err != nil {
		t.Fatalf("list logs after retention: %v", err)
	}
	if logs.Total != 1 {
		t.Fatalf("expected 1 retained event after rebuild, got %d", logs.Total)
	}
	if got := logs.Events[0].EventID; got != "evt_new" {
		t.Fatalf("expected newest event to remain, got %s", got)
	}

	status := engine.Status()
	if status.LastDeletedDayDirs != 1 {
		t.Fatalf("expected 1 deleted day dir, got %d", status.LastDeletedDayDirs)
	}
	if status.LastSuccessAt == "" {
		t.Fatal("expected retention success timestamp")
	}
}

func appendStoredEvent(t *testing.T, rawStore *raw.Store, worker *index.Worker, projectID, eventID string, ts time.Time, name string) {
	t.Helper()
	ev := &event.StoredEvent{
		EventID:    eventID,
		ReceivedAt: ts.Format(time.RFC3339Nano),
		Envelope: event.Envelope{
			SchemaVersion: 1,
			ProjectID:     projectID,
			Kind:          event.KindLog,
			TS:            ts.Format(time.RFC3339Nano),
			Source:        "test",
			Level:         "info",
			Name:          name,
			Attrs:         mustJSON(t, map[string]any{"message": name}),
			Body:          mustJSON(t, map[string]any{"message": name}),
		},
	}

	if _, err := rawStore.Append(ev); err != nil {
		t.Fatalf("append raw event: %v", err)
	}
	if !worker.Enqueue(ev) {
		t.Fatal("expected event enqueue to succeed")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return raw
}

func waitForLogCount(t *testing.T, store *sqlite.Store, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		logs, err := store.ListLogs(query.LogFilters{
			RangeFilters: query.RangeFilters{
				From:  time.Now().UTC().AddDate(0, 0, -30),
				To:    time.Now().UTC().Add(time.Hour),
				Page:  1,
				Limit: 50,
			},
		})
		if err == nil && logs.Total == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d indexed events", want)
}
