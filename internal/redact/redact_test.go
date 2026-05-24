package redact

import (
	"encoding/json"
	"testing"
	"time"

	"vigil/internal/event"
)

func TestApplyEventRedactsNestedSecrets(t *testing.T) {
	redactor := New(DefaultPolicy())
	ev := testEvent(t, map[string]any{
		"route": "/checkout",
		"headers": map[string]any{
			"Authorization": "Bearer super-secret-token",
			"Cookie":        "session=abc",
		},
		"emails": []any{"user@example.com", "safe text"},
	}, map[string]any{
		"message":       "sent to user@example.com",
		"connectionURL": "postgres://user:pass@example.test/db",
		"password":      "hunter2",
	})

	result := redactor.ApplyEvent(ev)
	if result.Fields < 2 {
		t.Fatalf("expected field redactions, got %+v", result)
	}
	if result.Values < 1 {
		t.Fatalf("expected value redactions, got %+v", result)
	}
	if result.Emails < 1 {
		t.Fatalf("expected email redactions, got %+v", result)
	}

	attrs := decodeRaw(t, ev.Attrs)
	if attrs["route"] != "/checkout" {
		t.Fatalf("expected normal attrs preserved, got %v", attrs)
	}
	headers := attrs["headers"].(map[string]any)
	if headers["Authorization"] != Replacement {
		t.Fatalf("expected authorization redacted, got %v", headers)
	}
	if attrs["emails"].([]any)[0] != Replacement {
		t.Fatalf("expected email redacted in array, got %v", attrs)
	}

	body := decodeRaw(t, ev.Body)
	if body["password"] != Replacement {
		t.Fatalf("expected password redacted, got %v", body)
	}
	if body["message"] != "sent to "+Replacement {
		t.Fatalf("expected email redacted in message, got %v", body["message"])
	}
}

func TestApplyEventCanDisableRedaction(t *testing.T) {
	policy := DefaultPolicy()
	policy.Enabled = false
	redactor := New(policy)
	ev := testEvent(t, map[string]any{"token": "secret"}, map[string]any{"email": "user@example.com"})

	result := redactor.ApplyEvent(ev)
	if result != (Result{}) {
		t.Fatalf("expected no redaction result, got %+v", result)
	}
	attrs := decodeRaw(t, ev.Attrs)
	if attrs["token"] != "secret" {
		t.Fatalf("expected disabled redaction to preserve token, got %v", attrs)
	}
}

func TestApplyEventCanPreserveEmails(t *testing.T) {
	policy := DefaultPolicy()
	policy.RedactEmails = false
	redactor := New(policy)
	ev := testEvent(t, map[string]any{"email": "user@example.com"}, nil)

	result := redactor.ApplyEvent(ev)
	if result.Emails != 0 {
		t.Fatalf("expected no email redactions, got %+v", result)
	}
	attrs := decodeRaw(t, ev.Attrs)
	if attrs["email"] != "user@example.com" {
		t.Fatalf("expected email preserved, got %v", attrs)
	}
}

func testEvent(t *testing.T, attrs any, body any) *event.StoredEvent {
	t.Helper()
	rawAttrs, err := json.Marshal(attrs)
	if err != nil {
		t.Fatalf("marshal attrs: %v", err)
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return &event.StoredEvent{
		EventID:    "evt_1",
		ReceivedAt: now,
		Envelope: event.Envelope{
			SchemaVersion: event.SchemaVersion,
			ProjectID:     "proj_1",
			Kind:          event.KindLog,
			TS:            now,
			Source:        "test",
			Name:          "test.event",
			Attrs:         rawAttrs,
			Body:          rawBody,
		},
	}
}

func decodeRaw(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	return value
}
