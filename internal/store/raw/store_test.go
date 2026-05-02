package raw

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"vigil/internal/event"
)

func TestAppendAndReplayWithSegmentRollover(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir, 220)

	ev1 := &event.StoredEvent{
		EventID:    "evt_1",
		ReceivedAt: "2026-05-02T10:00:00Z",
		Envelope: event.Envelope{
			SchemaVersion: 1,
			ProjectID:     "proj_1",
			Kind:          event.KindLog,
			TS:            "2026-05-02T10:00:00Z",
			Source:        "test",
			Name:          "event.one",
			Attrs:         []byte(`{"message":"first"}`),
			Body:          []byte(`{"message":"first"}`),
		},
	}
	ev2 := &event.StoredEvent{
		EventID:    "evt_2",
		ReceivedAt: "2026-05-02T10:00:01Z",
		Envelope: event.Envelope{
			SchemaVersion: 1,
			ProjectID:     "proj_1",
			Kind:          event.KindLog,
			TS:            "2026-05-02T10:00:01Z",
			Source:        "test",
			Name:          "event.two",
			Attrs:         []byte(`{"message":"second"}`),
			Body:          []byte(`{"message":"second"}`),
		},
	}

	if _, err := store.Append(ev1); err != nil {
		t.Fatalf("append ev1: %v", err)
	}
	if _, err := store.Append(ev2); err != nil {
		t.Fatalf("append ev2: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "proj_1", "2026-05-02", "*.ndjson"))
	if err != nil {
		t.Fatalf("glob raw files: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 segment files, got %d", len(matches))
	}

	seen := []string{}
	if err := store.Replay(context.Background(), func(ev *event.StoredEvent) error {
		seen = append(seen, ev.EventID)
		return nil
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}

	if len(seen) != 2 {
		t.Fatalf("expected 2 replayed events, got %d", len(seen))
	}

	if _, err := os.Stat(matches[0]); err != nil {
		t.Fatalf("expected first segment file to exist: %v", err)
	}
}

func TestPruneBeforeDay(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir, 1024)

	appendEvent := func(eventID, ts string) {
		t.Helper()
		ev := &event.StoredEvent{
			EventID:    eventID,
			ReceivedAt: ts,
			Envelope: event.Envelope{
				SchemaVersion: 1,
				ProjectID:     "proj_1",
				Kind:          event.KindLog,
				TS:            ts,
				Source:        "test",
				Name:          "event." + eventID,
				Attrs:         []byte(`{"message":"hello"}`),
				Body:          []byte(`{"message":"hello"}`),
			},
		}
		if _, err := store.Append(ev); err != nil {
			t.Fatalf("append %s: %v", eventID, err)
		}
	}

	appendEvent("old", "2026-04-25T10:00:00Z")
	appendEvent("new", "2026-05-02T10:00:00Z")

	summary, err := store.PruneBeforeDay("2026-05-01", true)
	if err != nil {
		t.Fatalf("dry-run prune: %v", err)
	}
	if summary.DeletedDayDirs != 1 {
		t.Fatalf("expected 1 day dir in dry-run summary, got %d", summary.DeletedDayDirs)
	}
	if _, err := os.Stat(filepath.Join(dir, "proj_1", "2026-04-25")); err != nil {
		t.Fatalf("expected old dir to remain after dry-run: %v", err)
	}

	summary, err = store.PruneBeforeDay("2026-05-01", false)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if summary.DeletedDayDirs != 1 {
		t.Fatalf("expected 1 day dir removed, got %d", summary.DeletedDayDirs)
	}
	if _, err := os.Stat(filepath.Join(dir, "proj_1", "2026-04-25")); !os.IsNotExist(err) {
		t.Fatalf("expected old dir to be removed, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "proj_1", "2026-05-02")); err != nil {
		t.Fatalf("expected new dir to remain: %v", err)
	}
}
