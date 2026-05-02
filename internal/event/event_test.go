package event

import (
	"testing"
	"time"
)

func TestParseAndNormalize(t *testing.T) {
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	payload := []byte(`{
		"schema_version": 1,
		"project_id": "proj_123",
		"kind": "log",
		"ts": "2026-05-02T11:59:00Z",
		"source": "test",
		"level": "INFO",
		"name": "request.completed",
		"attrs": {"total_tokens": 12},
		"body": {"message": "ok"}
	}`)

	ev, err := ParseAndNormalize(payload, "proj_123", now)
	if err != nil {
		t.Fatalf("ParseAndNormalize returned error: %v", err)
	}

	if ev.SchemaVersion != SchemaVersion {
		t.Fatalf("expected schema version %d, got %d", SchemaVersion, ev.SchemaVersion)
	}
	if ev.Level != "info" {
		t.Fatalf("expected lowercase level, got %q", ev.Level)
	}
	if ev.EventID == "" {
		t.Fatal("expected generated event id")
	}
	if ev.ReceivedAt == "" {
		t.Fatal("expected received_at to be set")
	}

	tokens, cost := ExtractUsageTotals(ev)
	if tokens != 12 {
		t.Fatalf("expected 12 tokens, got %v", tokens)
	}
	if cost != 0 {
		t.Fatalf("expected 0 cost, got %v", cost)
	}
}

func TestParseAndNormalizeRejectsProjectMismatch(t *testing.T) {
	payload := []byte(`{
		"schema_version": 1,
		"project_id": "proj_other",
		"kind": "log",
		"ts": "2026-05-02T11:59:00Z",
		"source": "test",
		"name": "request.completed"
	}`)

	_, err := ParseAndNormalize(payload, "proj_123", time.Now().UTC())
	if err == nil {
		t.Fatal("expected project mismatch error")
	}
}
