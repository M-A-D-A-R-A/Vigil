package tail

import (
	"fmt"
	"testing"
	"time"

	"vigil/internal/event"
)

func TestHubPublishesMatchingLogEventsAndTracksDisconnects(t *testing.T) {
	hub := NewHub()
	sub := hub.Subscribe(func(ev *event.StoredEvent) bool {
		return ev.Level == "error"
	})

	hub.Publish(testLogEvent("evt_info", "info"))
	hub.Publish(testLogEvent("evt_error", "error"))

	select {
	case ev := <-sub.Events:
		if ev.EventID != "evt_error" {
			t.Fatalf("expected evt_error, got %s", ev.EventID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for matching event")
	}

	status := hub.Status()
	if status.ActiveSubscribers != 1 {
		t.Fatalf("expected one active subscriber, got %d", status.ActiveSubscribers)
	}
	if status.PublishedEvents != 2 {
		t.Fatalf("expected two published log events, got %d", status.PublishedEvents)
	}

	sub.Close()
	status = hub.Status()
	if status.ActiveSubscribers != 0 {
		t.Fatalf("expected zero active subscribers, got %d", status.ActiveSubscribers)
	}
	if status.Disconnects != 1 {
		t.Fatalf("expected one disconnect, got %d", status.Disconnects)
	}
}

func TestHubTracksDroppedTailEvents(t *testing.T) {
	hub := NewHub()
	sub := hub.Subscribe(nil)
	defer sub.Close()

	for i := 0; i < defaultSubscriberBuffer+1; i++ {
		hub.Publish(testLogEvent(fmt.Sprintf("evt_%03d", i), "info"))
	}

	status := hub.Status()
	if status.DroppedEvents != 1 {
		t.Fatalf("expected one dropped event, got %d", status.DroppedEvents)
	}
}

func testLogEvent(id string, level string) *event.StoredEvent {
	now := time.Now().UTC()
	return &event.StoredEvent{
		EventID:    id,
		ReceivedAt: now.Format(time.RFC3339Nano),
		Envelope: event.Envelope{
			SchemaVersion: event.SchemaVersion,
			ProjectID:     "proj_1",
			Kind:          event.KindLog,
			TS:            now.Format(time.RFC3339Nano),
			Source:        "tail-test",
			Level:         level,
			Name:          "tail.event",
			Attrs:         []byte(`{}`),
			Body:          []byte(`null`),
		},
	}
}
