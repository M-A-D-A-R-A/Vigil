package ingest

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"vigil/internal/event"
	"vigil/internal/index"
	"vigil/internal/project"
	"vigil/internal/store/raw"
	"vigil/internal/store/sqlite"
	"vigil/internal/tail"
)

type Result struct {
	EventID      string `json:"event_id"`
	ReceivedAt   string `json:"received_at"`
	IndexedAsync bool   `json:"indexed_async"`
}

type BatchResult struct {
	Accepted     int      `json:"accepted"`
	Results      []Result `json:"results"`
	IndexedAsync bool     `json:"indexed_async"`
}

type Stats struct {
	TotalAccepted     uint64  `json:"total_accepted"`
	RateWindowSeconds int     `json:"rate_window_seconds"`
	RecentEvents      int     `json:"recent_events"`
	EventsPerSecond   float64 `json:"events_per_second"`
	EventsPerMinute   float64 `json:"events_per_minute"`
}

type Service struct {
	projects       *project.Service
	raw            *raw.Store
	db             *sqlite.Store
	worker         *index.Worker
	tail           *tail.Hub
	maxBytes       int
	gate           *sync.RWMutex
	statsMu        sync.Mutex
	totalAccepted  uint64
	recentIngests  []time.Time
	rateWindowSize time.Duration
}

func NewService(projects *project.Service, rawStore *raw.Store, db *sqlite.Store, worker *index.Worker, tailHub *tail.Hub, maxBytes int, gate *sync.RWMutex) *Service {
	if maxBytes <= 0 {
		maxBytes = event.DefaultMaxPayload
	}
	if gate == nil {
		gate = &sync.RWMutex{}
	}
	return &Service{
		projects:       projects,
		raw:            rawStore,
		db:             db,
		worker:         worker,
		tail:           tailHub,
		maxBytes:       maxBytes,
		gate:           gate,
		rateWindowSize: time.Minute,
	}
}

func (s *Service) Ingest(authorization string, payload []byte) (*Result, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()

	if len(payload) > s.maxBytes {
		return nil, fmt.Errorf("payload too large")
	}

	token, err := parseBearerToken(authorization)
	if err != nil {
		return nil, err
	}

	projectRecord, err := s.projects.AuthenticateBearerToken(token)
	if err != nil {
		return nil, err
	}

	ev, err := event.ParseAndNormalize(payload, projectRecord.ID, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	return s.acceptEvent(ev)
}

func (s *Service) IngestForProject(projectID string, payload []byte) (*Result, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()

	if len(payload) > s.maxBytes {
		return nil, fmt.Errorf("payload too large")
	}

	ev, err := event.ParseAndNormalizeForProject(payload, projectID, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	return s.acceptEvent(ev)
}

func (s *Service) IngestEnvelopes(authorization string, envelopes []event.Envelope) (*BatchResult, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()

	token, err := parseBearerToken(authorization)
	if err != nil {
		return nil, err
	}

	projectRecord, err := s.projects.AuthenticateBearerToken(token)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	events := make([]*event.StoredEvent, 0, len(envelopes))
	for _, env := range envelopes {
		env.SchemaVersion = event.SchemaVersion
		env.ProjectID = projectRecord.ID

		ev, err := event.NormalizeEnvelope(env, projectRecord.ID, now)
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}

	result := &BatchResult{
		Results:      make([]Result, 0, len(events)),
		IndexedAsync: true,
	}
	latestReceivedAt := ""
	for _, ev := range events {
		if _, err := s.raw.Append(ev); err != nil {
			return nil, err
		}
		if s.tail != nil {
			s.tail.Publish(ev)
		}
		if ev.ReceivedAt > latestReceivedAt {
			latestReceivedAt = ev.ReceivedAt
		}
		s.recordAccepted(time.Now().UTC())
		indexedAsync := s.worker.Enqueue(ev)
		if !indexedAsync {
			result.IndexedAsync = false
		}
		result.Results = append(result.Results, Result{
			EventID:      ev.EventID,
			ReceivedAt:   ev.ReceivedAt,
			IndexedAsync: indexedAsync,
		})
	}

	if latestReceivedAt != "" {
		if err := s.db.MarkLatestIngested(latestReceivedAt); err != nil {
			return nil, err
		}
	}

	result.Accepted = len(result.Results)
	return result, nil
}

func (s *Service) Stats() Stats {
	now := time.Now().UTC()
	s.statsMu.Lock()
	defer s.statsMu.Unlock()

	s.pruneRecentLocked(now)
	recent := len(s.recentIngests)
	windowSeconds := int(s.rateWindowSize.Seconds())
	if windowSeconds <= 0 {
		windowSeconds = 60
	}
	eventsPerSecond := float64(recent) / float64(windowSeconds)

	return Stats{
		TotalAccepted:     s.totalAccepted,
		RateWindowSeconds: windowSeconds,
		RecentEvents:      recent,
		EventsPerSecond:   eventsPerSecond,
		EventsPerMinute:   eventsPerSecond * 60,
	}
}

func (s *Service) recordAccepted(now time.Time) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()

	s.totalAccepted++
	s.recentIngests = append(s.recentIngests, now.UTC())
	s.pruneRecentLocked(now.UTC())
}

func (s *Service) acceptEvent(ev *event.StoredEvent) (*Result, error) {
	if _, err := s.raw.Append(ev); err != nil {
		return nil, err
	}
	if s.tail != nil {
		s.tail.Publish(ev)
	}

	if err := s.db.MarkLatestIngested(ev.ReceivedAt); err != nil {
		return nil, err
	}

	s.recordAccepted(time.Now().UTC())
	indexedAsync := s.worker.Enqueue(ev)

	return &Result{
		EventID:      ev.EventID,
		ReceivedAt:   ev.ReceivedAt,
		IndexedAsync: indexedAsync,
	}, nil
}

func (s *Service) pruneRecentLocked(now time.Time) {
	cutoff := now.Add(-s.rateWindowSize)
	keepFrom := 0
	for keepFrom < len(s.recentIngests) && s.recentIngests[keepFrom].Before(cutoff) {
		keepFrom++
	}
	if keepFrom == 0 {
		return
	}
	copy(s.recentIngests, s.recentIngests[keepFrom:])
	s.recentIngests = s.recentIngests[:len(s.recentIngests)-keepFrom]
}

func parseBearerToken(header string) (string, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", fmt.Errorf("authorization header is required")
	}
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return "", fmt.Errorf("authorization must use Bearer token")
	}
	token := strings.TrimSpace(header[7:])
	if token == "" {
		return "", fmt.Errorf("authorization token is required")
	}
	return token, nil
}
