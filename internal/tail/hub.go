package tail

import (
	"sync"

	"vigil/internal/event"
)

const defaultSubscriberBuffer = 128

type Matcher func(*event.StoredEvent) bool

type Hub struct {
	mu          sync.Mutex
	nextID      int
	subscribers map[int]*subscriber
	bufferSize  int
	stats       Stats
}

type Stats struct {
	ActiveSubscribers int    `json:"active_subscribers"`
	PublishedEvents   uint64 `json:"published_events"`
	DroppedEvents     uint64 `json:"dropped_tail_events"`
	Disconnects       uint64 `json:"disconnects"`
}

type Subscription struct {
	Events <-chan *event.StoredEvent

	hub  *Hub
	id   int
	once sync.Once
}

type subscriber struct {
	events  chan *event.StoredEvent
	matcher Matcher
}

func NewHub() *Hub {
	return &Hub{
		subscribers: make(map[int]*subscriber),
		bufferSize:  defaultSubscriberBuffer,
	}
}

func (h *Hub) Subscribe(matcher Matcher) *Subscription {
	if matcher == nil {
		matcher = func(*event.StoredEvent) bool { return true }
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.nextID++
	id := h.nextID
	sub := &subscriber{
		events:  make(chan *event.StoredEvent, h.bufferSize),
		matcher: matcher,
	}
	h.subscribers[id] = sub

	return &Subscription{
		Events: sub.events,
		hub:    h,
		id:     id,
	}
}

func (h *Hub) Publish(ev *event.StoredEvent) {
	if ev == nil || ev.Kind != event.KindLog {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.stats.PublishedEvents++
	for _, sub := range h.subscribers {
		if !sub.matcher(ev) {
			continue
		}
		select {
		case sub.events <- ev:
		default:
			h.stats.DroppedEvents++
		}
	}
}

func (h *Hub) Status() Stats {
	h.mu.Lock()
	defer h.mu.Unlock()

	status := h.stats
	status.ActiveSubscribers = len(h.subscribers)
	return status
}

func (s *Subscription) Close() {
	s.once.Do(func() {
		s.hub.mu.Lock()
		defer s.hub.mu.Unlock()

		sub, ok := s.hub.subscribers[s.id]
		if !ok {
			return
		}
		delete(s.hub.subscribers, s.id)
		close(sub.events)
		s.hub.stats.Disconnects++
	})
}
