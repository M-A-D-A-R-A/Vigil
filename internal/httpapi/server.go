package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"vigil/internal/config"
	"vigil/internal/ingest"
	"vigil/internal/project"
	"vigil/internal/query"
	"vigil/internal/retention"
	"vigil/internal/store/sqlite"
	webassets "vigil/web"
)

type Server struct {
	cfg       config.Config
	projects  *project.Service
	ingest    *ingest.Service
	store     *sqlite.Store
	retention *retention.Engine
	staticFS  fs.FS
}

func New(cfg config.Config, projects *project.Service, ingestService *ingest.Service, store *sqlite.Store, retentionEngine *retention.Engine) (*Server, error) {
	staticFS, err := fs.Sub(webassets.Assets, "dist")
	if err != nil {
		return nil, fmt.Errorf("load embedded UI: %w", err)
	}

	return &Server{
		cfg:       cfg,
		projects:  projects,
		ingest:    ingestService,
		store:     store,
		retention: retentionEngine,
		staticFS:  staticFS,
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/projects", s.handleCreateProject)
	mux.HandleFunc("GET /api/projects", s.handleListProjects)
	mux.HandleFunc("POST /api/projects/{id}/keys/regenerate", s.handleRegenerateProjectKey)
	mux.HandleFunc("POST /api/ingest", s.handleIngest)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
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

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(r.Body, int64(s.cfg.MaxEventBytes)+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("read payload: %w", err))
		return
	}

	result, err := s.ingest.Ingest(r.Header.Get("Authorization"), payload)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(strings.ToLower(err.Error()), "authorization") || strings.Contains(strings.ToLower(err.Error()), "ingest key") {
			status = http.StatusUnauthorized
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
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

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{
		"error": err.Error(),
	})
}

func writeQueryError(w http.ResponseWriter, err *query.QueryError) {
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"error":       "invalid query",
		"query_error": err,
	})
}
