package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"vigil/internal/config"
	"vigil/internal/event"
	"vigil/internal/index"
	"vigil/internal/ingest"
	"vigil/internal/otlp"
	"vigil/internal/project"
	"vigil/internal/query"
	"vigil/internal/retention"
	"vigil/internal/store/sqlite"
	"vigil/internal/tail"
	webassets "vigil/web"
)

type Server struct {
	cfg       config.Config
	projects  *project.Service
	ingest    *ingest.Service
	indexer   *index.Worker
	tail      *tail.Hub
	store     *sqlite.Store
	retention *retention.Engine
	browserRL *browserRateLimiter
	staticFS  fs.FS
}

func New(cfg config.Config, projects *project.Service, ingestService *ingest.Service, indexer *index.Worker, tailHub *tail.Hub, store *sqlite.Store, retentionEngine *retention.Engine) (*Server, error) {
	staticFS, err := fs.Sub(webassets.Assets, "dist")
	if err != nil {
		return nil, fmt.Errorf("load embedded UI: %w", err)
	}

	return &Server{
		cfg:       cfg,
		projects:  projects,
		ingest:    ingestService,
		indexer:   indexer,
		tail:      tailHub,
		store:     store,
		retention: retentionEngine,
		browserRL: newBrowserRateLimiter(120, time.Minute),
		staticFS:  staticFS,
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/projects", s.handleCreateProject)
	mux.HandleFunc("GET /api/projects", s.handleListProjects)
	mux.HandleFunc("POST /api/projects/{id}/keys/regenerate", s.handleRegenerateProjectKey)
	mux.HandleFunc("GET /api/projects/{id}/browser-keys", s.handleListBrowserKeys)
	mux.HandleFunc("POST /api/projects/{id}/browser-keys", s.handleCreateBrowserKey)
	mux.HandleFunc("POST /api/projects/{id}/browser-keys/{key_id}/rotate", s.handleRotateBrowserKey)
	mux.HandleFunc("POST /api/projects/{id}/browser-keys/{key_id}/revoke", s.handleRevokeBrowserKey)
	mux.HandleFunc("POST /api/ingest", s.handleIngest)
	mux.HandleFunc("OPTIONS /api/browser/ingest", s.handleBrowserIngestOptions)
	mux.HandleFunc("POST /api/browser/ingest", s.handleBrowserIngest)
	mux.HandleFunc("POST /v1/logs", s.handleOTLPLogs)
	mux.HandleFunc("POST /v1/traces", s.handleOTLPTraces)
	mux.HandleFunc("POST /v1/metrics", s.handleOTLPMetrics)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("GET /api/logs/tail", s.handleLogsTail)
	mux.HandleFunc("GET /api/traces", s.handleTraces)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.Handle("/", s.handleSPA())
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status, err := s.store.GetSyncStatus()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"app":       "vigil",
		"sync":      status,
		"ingest":    s.ingest.Stats(),
		"index":     s.indexer.Status(),
		"tail":      s.tail.Status(),
		"retention": s.retention.Status(),
	})
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	type request struct {
		Name string `json:"name"`
	}
	var payload request
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON payload"))
		return
	}

	result, err := s.projects.CreateProject(payload.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.projects.ListProjects()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

func (s *Server) handleRegenerateProjectKey(w http.ResponseWriter, r *http.Request) {
	result, err := s.projects.RegenerateKey(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleCreateBrowserKey(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	type request struct {
		Name           string   `json:"name"`
		AllowedOrigins []string `json:"allowed_origins"`
	}
	var payload request
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON payload"))
		return
	}

	result, err := s.projects.CreateBrowserKey(r.PathValue("id"), payload.Name, payload.AllowedOrigins)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleListBrowserKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.projects.ListBrowserKeys(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"browser_keys": keys})
}

func (s *Server) handleRotateBrowserKey(w http.ResponseWriter, r *http.Request) {
	result, err := s.projects.RotateBrowserKey(r.PathValue("id"), r.PathValue("key_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleRevokeBrowserKey(w http.ResponseWriter, r *http.Request) {
	key, err := s.projects.RevokeBrowserKey(r.PathValue("id"), r.PathValue("key_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key})
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(r.Body, int64(s.cfg.MaxEventBytes)+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("read payload: %w", err))
		return
	}

	result, err := s.ingest.Ingest(r.Header.Get("Authorization"), payload)
	if err != nil {
		s.writeIngestError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) handleBrowserIngestOptions(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if !s.writeBrowserCORSForKnownOrigin(w, origin) {
		writeError(w, http.StatusForbidden, fmt.Errorf("origin is not allowed"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleBrowserIngest(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	origin := strings.TrimSpace(r.Header.Get("Origin"))
	auth, err := s.authenticateBrowserRequest(r, origin)
	if err != nil {
		s.writeBrowserIngestError(w, err)
		return
	}
	s.writeBrowserCORSHeaders(w, origin)

	if !s.browserRL.Allow(auth.Key.ID, origin, clientIP(r), time.Now().UTC()) {
		writeError(w, http.StatusTooManyRequests, fmt.Errorf("browser ingest rate limit exceeded"))
		return
	}

	payload, err := io.ReadAll(io.LimitReader(r.Body, int64(s.cfg.MaxEventBytes)+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("read payload: %w", err))
		return
	}
	if len(payload) > s.cfg.MaxEventBytes {
		writeError(w, http.StatusBadRequest, fmt.Errorf("payload too large"))
		return
	}

	result, err := s.ingest.IngestForProject(auth.Project.ID, payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) authenticateBrowserRequest(r *http.Request, origin string) (*project.BrowserKeyAuthResult, error) {
	token, err := bearerToken(r.Header.Get("Authorization"))
	if err != nil {
		return nil, err
	}
	return s.projects.AuthenticateBrowserToken(token, origin)
}

func (s *Server) handleOTLPLogs(w http.ResponseWriter, r *http.Request) {
	// ExportLogsServiceRequest and LogsData share the same field layout for resource_logs.
	// Decoding into LogsData keeps the receiver HTTP-only and avoids gRPC gateway deps.
	var req logsv1.LogsData
	if !s.readOTLPRequest(w, r, &req) {
		return
	}
	envelopes, err := otlp.LogsToEnvelopes(&req, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, err := s.ingest.IngestEnvelopes(r.Header.Get("Authorization"), envelopes); err != nil {
		s.writeIngestError(w, err)
		return
	}
	writeOTLPProto(w, nil)
}

func (s *Server) handleOTLPTraces(w http.ResponseWriter, r *http.Request) {
	// ExportTraceServiceRequest and TracesData share the same field layout for resource_spans.
	// Decoding into TracesData keeps the receiver HTTP-only and avoids gRPC gateway deps.
	var req tracev1.TracesData
	if !s.readOTLPRequest(w, r, &req) {
		return
	}
	envelopes, err := otlp.TracesToEnvelopes(&req, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, err := s.ingest.IngestEnvelopes(r.Header.Get("Authorization"), envelopes); err != nil {
		s.writeIngestError(w, err)
		return
	}
	writeOTLPProto(w, nil)
}

func (s *Server) handleOTLPMetrics(w http.ResponseWriter, r *http.Request) {
	// ExportMetricsServiceRequest and MetricsData share the same field layout for resource_metrics.
	// Decoding into MetricsData keeps the receiver HTTP-only and avoids gRPC gateway deps.
	var req metricsv1.MetricsData
	if !s.readOTLPRequest(w, r, &req) {
		return
	}
	envelopes, err := otlp.MetricsToEnvelopes(&req, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, err := s.ingest.IngestEnvelopes(r.Header.Get("Authorization"), envelopes); err != nil {
		s.writeIngestError(w, err)
		return
	}
	writeOTLPProto(w, nil)
}

func (s *Server) readOTLPRequest(w http.ResponseWriter, r *http.Request, msg proto.Message) bool {
	defer r.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(r.Body, int64(s.cfg.MaxEventBytes)+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("read payload: %w", err))
		return false
	}
	if len(payload) > s.cfg.MaxEventBytes {
		writeError(w, http.StatusBadRequest, fmt.Errorf("payload too large"))
		return false
	}
	if err := proto.Unmarshal(payload, msg); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid OTLP protobuf payload: %w", err))
		return false
	}
	return true
}

func (s *Server) writeIngestError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if isAuthError(err) {
		status = http.StatusUnauthorized
	}
	writeError(w, status, err)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	filters, err := query.ParseLogFilters(r.URL.Query(), time.Now().UTC())
	if err != nil {
		var queryErr *query.QueryError
		if errors.As(err, &queryErr) {
			writeQueryError(w, queryErr)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.store.ListLogs(filters)
	if err != nil {
		var queryErr *query.QueryError
		if errors.As(err, &queryErr) {
			writeQueryError(w, queryErr)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleLogsTail(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming is not supported"))
		return
	}

	filters, err := query.ParseLogFilters(r.URL.Query(), time.Now().UTC())
	if err != nil {
		var queryErr *query.QueryError
		if errors.As(err, &queryErr) {
			writeQueryError(w, queryErr)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(r.URL.Query().Get("to")) == "" {
		filters.To = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	subscription := s.tail.Subscribe(func(ev *event.StoredEvent) bool {
		return query.MatchLogEvent(filters, ev)
	})
	defer subscription.Close()

	cursor := strings.TrimSpace(r.URL.Query().Get("after"))
	if cursor == "" {
		cursor = strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	}
	if cursor != "" {
		events, err := s.store.ListLogsAfter(filters, cursor, query.MaxPageSize)
		if err != nil {
			_ = writeSSE(w, "error", "", map[string]string{"error": err.Error()})
			flusher.Flush()
			return
		}
		for i := range events {
			if err := writeSSE(w, "log", events[i].EventID, events[i]); err != nil {
				return
			}
		}
		flusher.Flush()
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-subscription.Events:
			if !ok {
				return
			}
			if err := writeSSE(w, "log", ev.EventID, ev); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if err := writeSSE(w, "ping", "", map[string]string{"status": "ok"}); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSE(w io.Writer, eventName string, id string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if id != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", id); err != nil {
			return err
		}
	}
	if eventName != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", eventName); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	return nil
}

func (s *Server) handleTraces(w http.ResponseWriter, r *http.Request) {
	filters, err := query.ParseRangeFilters(r.URL.Query(), time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.store.ListTraces(filters)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	filters, err := query.ParseRangeFilters(r.URL.Query(), time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.store.GetStats(filters)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleSPA() http.Handler {
	fileServer := http.FileServerFS(s.staticFS)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		if _, err := fs.Stat(s.staticFS, path); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}

		r.URL.Path = "/index.html"
		fileServer.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeOTLPProto(w http.ResponseWriter, payload proto.Message) {
	var raw []byte
	if payload != nil {
		var err error
		raw, err = proto.Marshal(payload)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("marshal OTLP response: %w", err))
			return
		}
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{
		"error": err.Error(),
	})
}

func isAuthError(err error) bool {
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "authorization") || strings.Contains(lower, "ingest key")
}

func (s *Server) writeBrowserIngestError(w http.ResponseWriter, err error) {
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "origin"):
		writeError(w, http.StatusForbidden, err)
	case strings.Contains(lower, "browser ingest key"), strings.Contains(lower, "authorization"):
		writeError(w, http.StatusUnauthorized, err)
	default:
		writeError(w, http.StatusBadRequest, err)
	}
}

func (s *Server) writeBrowserCORSForKnownOrigin(w http.ResponseWriter, origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return false
	}
	allowed, err := s.projects.HasAllowedBrowserOrigin(origin)
	if err != nil || !allowed {
		return false
	}
	s.writeBrowserCORSHeaders(w, origin)
	return true
}

func (s *Server) writeBrowserCORSHeaders(w http.ResponseWriter, origin string) {
	w.Header().Set("Access-Control-Allow-Origin", strings.TrimSpace(origin))
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.Header().Set("Vary", "Origin")
}

func bearerToken(header string) (string, error) {
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

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type browserRateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]browserRateBucket
}

type browserRateBucket struct {
	start time.Time
	count int
}

func newBrowserRateLimiter(limit int, window time.Duration) *browserRateLimiter {
	if limit <= 0 {
		limit = 120
	}
	if window <= 0 {
		window = time.Minute
	}
	return &browserRateLimiter{
		limit:   limit,
		window:  window,
		buckets: map[string]browserRateBucket{},
	}
}

func (l *browserRateLimiter) Allow(keyID, origin, ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucketKey := keyID + "\x00" + origin + "\x00" + ip
	bucket := l.buckets[bucketKey]
	if bucket.start.IsZero() || now.Sub(bucket.start) >= l.window {
		l.buckets[bucketKey] = browserRateBucket{start: now, count: 1}
		l.pruneLocked(now)
		return true
	}
	if bucket.count >= l.limit {
		return false
	}
	bucket.count++
	l.buckets[bucketKey] = bucket
	return true
}

func (l *browserRateLimiter) pruneLocked(now time.Time) {
	cutoff := now.Add(-2 * l.window)
	for key, bucket := range l.buckets {
		if bucket.start.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
}

func writeQueryError(w http.ResponseWriter, err *query.QueryError) {
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"error":       "invalid query",
		"query_error": err,
	})
}
