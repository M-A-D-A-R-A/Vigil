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
)

type Result struct {
	EventID      string `json:"event_id"`
	ReceivedAt   string `json:"received_at"`
	IndexedAsync bool   `json:"indexed_async"`
}

type Service struct {
	projects *project.Service
	raw      *raw.Store
	db       *sqlite.Store
	worker   *index.Worker
	maxBytes int
	gate     *sync.RWMutex
}

func NewService(projects *project.Service, rawStore *raw.Store, db *sqlite.Store, worker *index.Worker, maxBytes int, gate *sync.RWMutex) *Service {
	if maxBytes <= 0 {
		maxBytes = event.DefaultMaxPayload
	}
	if gate == nil {
		gate = &sync.RWMutex{}
	}
	return &Service{
		projects: projects,
		raw:      rawStore,
		db:       db,
		worker:   worker,
		maxBytes: maxBytes,
		gate:     gate,
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

	if _, err := s.raw.Append(ev); err != nil {
		return nil, err
	}

	if err := s.db.MarkLatestIngested(ev.ReceivedAt); err != nil {
		return nil, err
	}

	s.worker.Enqueue(ev)

	return &Result{
		EventID:      ev.EventID,
		ReceivedAt:   ev.ReceivedAt,
		IndexedAsync: true,
	}, nil
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
