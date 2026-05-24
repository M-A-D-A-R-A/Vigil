package query

import (
	"testing"
	"time"

	"vigil/internal/event"
)

func TestMatchLogEventStructuredFilters(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	parsed, err := ParseStructuredQuery(`level = "error" && attrs.route = "/login" && attrs.status >= 500`, now)
	if err != nil {
		t.Fatalf("parse structured query: %v", err)
	}

	filters := LogFilters{
		RangeFilters: RangeFilters{
			ProjectID: "proj_1",
			From:      now.Add(-time.Hour),
			To:        now.Add(time.Hour),
		},
		Structured: parsed,
	}

	ev := &event.StoredEvent{
		EventID:    "evt_1",
		ReceivedAt: now.Format(time.RFC3339Nano),
		Envelope: event.Envelope{
			SchemaVersion: event.SchemaVersion,
			ProjectID:     "proj_1",
			Kind:          event.KindLog,
			TS:            now.Format(time.RFC3339Nano),
			Source:        "api",
			Level:         "error",
			Name:          "request.failed",
			Attrs:         []byte(`{"route":"/login","status":503}`),
			Body:          []byte(`{"message":"failed"}`),
		},
	}

	if !MatchLogEvent(filters, ev) {
		t.Fatal("expected event to match structured filters")
	}

	ev.Attrs = []byte(`{"route":"/health","status":200}`)
	if MatchLogEvent(filters, ev) {
		t.Fatal("did not expect event to match after attrs changed")
	}
}

func TestMatchLogEventRejectsNonLogEvents(t *testing.T) {
	now := time.Now().UTC()
	filters := LogFilters{
		RangeFilters: RangeFilters{
			From: now.Add(-time.Hour),
			To:   now.Add(time.Hour),
		},
	}
	ev := &event.StoredEvent{
		EventID:    "evt_trace",
		ReceivedAt: now.Format(time.RFC3339Nano),
		Envelope: event.Envelope{
			SchemaVersion: event.SchemaVersion,
			ProjectID:     "proj_1",
			Kind:          event.KindTrace,
			TS:            now.Format(time.RFC3339Nano),
			Source:        "api",
			Name:          "trace.step",
			Attrs:         []byte(`{}`),
			Body:          []byte(`null`),
		},
	}
	if MatchLogEvent(filters, ev) {
		t.Fatal("did not expect trace event to match log tail filters")
	}
}

func TestMatchLogEventTraceContextFilters(t *testing.T) {
	now := time.Now().UTC()
	filters := LogFilters{
		RangeFilters: RangeFilters{
			From: now.Add(-time.Hour),
			To:   now.Add(time.Hour),
		},
		TraceID:      "trace_1",
		SpanID:       "span_child",
		ParentSpanID: "span_root",
	}
	ev := &event.StoredEvent{
		EventID:    "evt_log",
		ReceivedAt: now.Format(time.RFC3339Nano),
		Envelope: event.Envelope{
			SchemaVersion: event.SchemaVersion,
			ProjectID:     "proj_1",
			Kind:          event.KindLog,
			TS:            now.Format(time.RFC3339Nano),
			Source:        "api",
			TraceID:       "trace_1",
			SpanID:        "span_child",
			ParentSpanID:  "span_root",
			Level:         "info",
			Name:          "request.completed",
			Attrs:         []byte(`{}`),
			Body:          []byte(`null`),
		},
	}
	if !MatchLogEvent(filters, ev) {
		t.Fatal("expected event to match trace context filters")
	}
	ev.ParentSpanID = "other_parent"
	if MatchLogEvent(filters, ev) {
		t.Fatal("did not expect event to match after parent span changed")
	}
}
