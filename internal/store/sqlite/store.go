package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"vigil/internal/event"
	"vigil/internal/query"
)

type Store struct {
	db *sql.DB
}

type ProjectRecord struct {
	ID        string
	Name      string
	Status    string
	CreatedAt string
	UpdatedAt string
}

type SyncStatus struct {
	LatestIngestedAt string `json:"latest_ingested_at"`
	LatestIndexedAt  string `json:"latest_indexed_at"`
	LastRebuildAt    string `json:"last_rebuild_at"`
	LastError        string `json:"last_error,omitempty"`
	Stale            bool   `json:"stale"`
}

type ResultWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type LogList struct {
	Events     []event.StoredEvent `json:"events"`
	Page       int                 `json:"page"`
	Limit      int                 `json:"limit"`
	Total      int                 `json:"total"`
	Warnings   []ResultWarning     `json:"warnings,omitempty"`
	SyncStatus SyncStatus          `json:"sync"`
}

type TraceEvent struct {
	EventID string          `json:"event_id"`
	TS      string          `json:"ts"`
	Name    string          `json:"name"`
	Level   string          `json:"level,omitempty"`
	Source  string          `json:"source"`
	SpanID  string          `json:"span_id,omitempty"`
	Attrs   json.RawMessage `json:"attrs"`
	Body    json.RawMessage `json:"body"`
}

type TraceTimeline struct {
	TraceID    string       `json:"trace_id"`
	ProjectID  string       `json:"project_id"`
	Name       string       `json:"name"`
	StartedAt  string       `json:"started_at"`
	EndedAt    string       `json:"ended_at"`
	EventCount int          `json:"event_count"`
	Events     []TraceEvent `json:"events"`
}

type TraceList struct {
	Traces     []TraceTimeline `json:"traces"`
	Page       int             `json:"page"`
	Limit      int             `json:"limit"`
	Total      int             `json:"total"`
	Warnings   []ResultWarning `json:"warnings,omitempty"`
	SyncStatus SyncStatus      `json:"sync"`
}

type CountByValue struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type DailyVolume struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

type StatsSummary struct {
	TotalEvents int            `json:"total_events"`
	ByKind      []CountByValue `json:"by_kind"`
	ByLevel     []CountByValue `json:"by_level"`
	TokenTotal  float64        `json:"token_total"`
	CostTotal   float64        `json:"cost_total"`
	Volume      []DailyVolume  `json:"volume"`
	SyncStatus  SyncStatus     `json:"sync"`
}

func Open(ctx context.Context, dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Store{db: db}
	if err := store.init(ctx); err != nil {
		db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) init(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA busy_timeout = 5000;`,
		`CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL,
			ingest_key_hash TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS events (
			event_id TEXT PRIMARY KEY,
			schema_version INTEGER NOT NULL,
			project_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			ts TEXT NOT NULL,
			received_at TEXT NOT NULL,
			source TEXT NOT NULL,
			trace_id TEXT NOT NULL DEFAULT '',
			span_id TEXT NOT NULL DEFAULT '',
			level TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL,
			attrs_json TEXT NOT NULL,
			body_json TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_events_project_ts ON events(project_id, ts DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_events_trace_ts ON events(trace_id, ts ASC);`,
		`CREATE INDEX IF NOT EXISTS idx_events_kind_ts ON events(kind, ts DESC);`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS events_fts USING fts5(
			event_id UNINDEXED,
			name,
			source,
			level,
			attrs_text,
			body_text
		);`,
		`CREATE TABLE IF NOT EXISTS trace_summaries (
			trace_id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			name TEXT NOT NULL,
			started_at TEXT NOT NULL,
			ended_at TEXT NOT NULL,
			event_count INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS stats_daily (
			day TEXT NOT NULL,
			project_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			level TEXT NOT NULL,
			event_count INTEGER NOT NULL DEFAULT 0,
			token_total REAL NOT NULL DEFAULT 0,
			cost_total REAL NOT NULL DEFAULT 0,
			PRIMARY KEY (day, project_id, kind, level)
		);`,
		`CREATE TABLE IF NOT EXISTS worker_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			latest_ingested_at TEXT NOT NULL DEFAULT '',
			latest_indexed_at TEXT NOT NULL DEFAULT '',
			last_rebuild_at TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		);`,
		`INSERT INTO worker_state(id) VALUES(1) ON CONFLICT(id) DO NOTHING;`,
	}

	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("init sqlite schema: %w", err)
		}
	}

	return nil
}

func (s *Store) CreateProject(id, name, keyHash string, now time.Time) (ProjectRecord, error) {
	record := ProjectRecord{
		ID:        id,
		Name:      name,
		Status:    "active",
		CreatedAt: now.UTC().Format(time.RFC3339Nano),
		UpdatedAt: now.UTC().Format(time.RFC3339Nano),
	}
	_, err := s.db.Exec(
		`INSERT INTO projects(id, name, status, ingest_key_hash, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		record.ID,
		record.Name,
		record.Status,
		keyHash,
		record.CreatedAt,
		record.UpdatedAt,
	)
	if err != nil {
		return ProjectRecord{}, fmt.Errorf("create project: %w", err)
	}
	return record, nil
}

func (s *Store) ListProjects() ([]ProjectRecord, error) {
	rows, err := s.db.Query(`SELECT id, name, status, created_at, updated_at FROM projects ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var records []ProjectRecord
	for rows.Next() {
		var record ProjectRecord
		if err := rows.Scan(&record.ID, &record.Name, &record.Status, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) UpdateProjectKey(projectID, keyHash string, now time.Time) (ProjectRecord, error) {
	updatedAt := now.UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(
		`UPDATE projects SET ingest_key_hash = ?, updated_at = ? WHERE id = ?`,
		keyHash,
		updatedAt,
		projectID,
	); err != nil {
		return ProjectRecord{}, fmt.Errorf("update project key: %w", err)
	}

	row := s.db.QueryRow(`SELECT id, name, status, created_at, updated_at FROM projects WHERE id = ?`, projectID)
	var record ProjectRecord
	if err := row.Scan(&record.ID, &record.Name, &record.Status, &record.CreatedAt, &record.UpdatedAt); err != nil {
		return ProjectRecord{}, fmt.Errorf("load regenerated project: %w", err)
	}
	return record, nil
}

func (s *Store) GetProjectByKeyHash(keyHash string) (*ProjectRecord, error) {
	row := s.db.QueryRow(
		`SELECT id, name, status, created_at, updated_at FROM projects WHERE ingest_key_hash = ?`,
		keyHash,
	)

	var record ProjectRecord
	if err := row.Scan(&record.ID, &record.Name, &record.Status, &record.CreatedAt, &record.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("invalid ingest key")
		}
		return nil, fmt.Errorf("lookup project by key: %w", err)
	}
	return &record, nil
}

func (s *Store) MarkLatestIngested(receivedAt string) error {
	_, err := s.db.Exec(
		`UPDATE worker_state
		 SET latest_ingested_at = CASE
		 	WHEN latest_ingested_at = '' OR latest_ingested_at < ? THEN ?
		 	ELSE latest_ingested_at
		 END,
		 updated_at = ?
		 WHERE id = 1`,
		receivedAt,
		receivedAt,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) UpsertEvent(ev *event.StoredEvent) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	result, err := tx.Exec(
		`INSERT OR IGNORE INTO events(
			event_id, schema_version, project_id, kind, ts, received_at, source, trace_id, span_id, level, name, attrs_json, body_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.EventID,
		ev.SchemaVersion,
		ev.ProjectID,
		string(ev.Kind),
		ev.TS,
		ev.ReceivedAt,
		ev.Source,
		ev.TraceID,
		ev.SpanID,
		ev.Level,
		ev.Name,
		string(ev.Attrs),
		string(ev.Body),
	)
	if err != nil {
		return false, fmt.Errorf("insert event: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return false, tx.Commit()
	}

	if _, err := tx.Exec(
		`INSERT INTO events_fts(event_id, name, source, level, attrs_text, body_text)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		ev.EventID,
		ev.Name,
		ev.Source,
		ev.Level,
		event.SearchText(ev.Attrs),
		event.SearchText(ev.Body),
	); err != nil {
		return false, fmt.Errorf("insert fts row: %w", err)
	}

	if ev.TraceID != "" {
		if _, err := tx.Exec(
			`INSERT INTO trace_summaries(trace_id, project_id, name, started_at, ended_at, event_count)
			 VALUES (?, ?, ?, ?, ?, 1)
			 ON CONFLICT(trace_id) DO UPDATE SET
			 	project_id = excluded.project_id,
			 	name = CASE
			 		WHEN trace_summaries.name = '' THEN excluded.name
			 		ELSE trace_summaries.name
			 	END,
			 	started_at = MIN(trace_summaries.started_at, excluded.started_at),
			 	ended_at = MAX(trace_summaries.ended_at, excluded.ended_at),
			 	event_count = trace_summaries.event_count + 1`,
			ev.TraceID,
			ev.ProjectID,
			ev.Name,
			ev.TS,
			ev.TS,
		); err != nil {
			return false, fmt.Errorf("upsert trace summary: %w", err)
		}
	}

	tokens, cost := event.ExtractUsageTotals(ev)
	if _, err := tx.Exec(
		`INSERT INTO stats_daily(day, project_id, kind, level, event_count, token_total, cost_total)
		 VALUES (?, ?, ?, ?, 1, ?, ?)
		 ON CONFLICT(day, project_id, kind, level) DO UPDATE SET
		 	event_count = stats_daily.event_count + 1,
		 	token_total = stats_daily.token_total + excluded.token_total,
		 	cost_total = stats_daily.cost_total + excluded.cost_total`,
		event.TimestampDay(ev.TS),
		ev.ProjectID,
		string(ev.Kind),
		ev.Level,
		tokens,
		cost,
	); err != nil {
		return false, fmt.Errorf("upsert daily stats: %w", err)
	}

	if _, err := tx.Exec(
		`UPDATE worker_state
		 SET latest_indexed_at = CASE
		 	WHEN latest_indexed_at = '' OR latest_indexed_at < ? THEN ?
		 	ELSE latest_indexed_at
		 END,
		 last_error = '',
		 updated_at = ?
		 WHERE id = 1`,
		ev.ReceivedAt,
		ev.ReceivedAt,
		time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return false, fmt.Errorf("update worker state: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) MarkRebuildSuccess(now time.Time) error {
	_, err := s.db.Exec(
		`UPDATE worker_state SET last_rebuild_at = ?, last_error = '', updated_at = ? WHERE id = 1`,
		now.UTC().Format(time.RFC3339Nano),
		now.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) ResetReadModels(now time.Time) error {
	updatedAt := now.UTC().Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	statements := []string{
		`DELETE FROM events;`,
		`DELETE FROM events_fts;`,
		`DELETE FROM trace_summaries;`,
		`DELETE FROM stats_daily;`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("reset read models: %w", err)
		}
	}

	if _, err := tx.Exec(
		`UPDATE worker_state
		 SET latest_indexed_at = '',
		     last_error = '',
		     updated_at = ?
		 WHERE id = 1`,
		updatedAt,
	); err != nil {
		return fmt.Errorf("reset worker state: %w", err)
	}

	return tx.Commit()
}

func (s *Store) ReplaceWorkerStateAfterReset(latestReceivedAt string, now time.Time) error {
	latestReceivedAt = strings.TrimSpace(latestReceivedAt)
	updatedAt := now.UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(
		`UPDATE worker_state
		 SET latest_ingested_at = ?,
		     latest_indexed_at = ?,
		     last_rebuild_at = ?,
		     last_error = '',
		     updated_at = ?
		 WHERE id = 1`,
		latestReceivedAt,
		latestReceivedAt,
		updatedAt,
		updatedAt,
	)
	return err
}

func (s *Store) MarkWorkerError(err error) error {
	if err == nil {
		return nil
	}
	_, execErr := s.db.Exec(
		`UPDATE worker_state SET last_error = ?, updated_at = ? WHERE id = 1`,
		err.Error(),
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return execErr
}

func (s *Store) GetSyncStatus() (SyncStatus, error) {
	row := s.db.QueryRow(
		`SELECT latest_ingested_at, latest_indexed_at, last_rebuild_at, last_error FROM worker_state WHERE id = 1`,
	)
	var status SyncStatus
	if err := row.Scan(&status.LatestIngestedAt, &status.LatestIndexedAt, &status.LastRebuildAt, &status.LastError); err != nil {
		return SyncStatus{}, err
	}
	status.Stale = status.LatestIngestedAt != "" && status.LatestIndexedAt < status.LatestIngestedAt
	return status, nil
}

func (s *Store) ListLogs(filters query.LogFilters) (*LogList, error) {
	args := []any{filters.From.Format(time.RFC3339Nano), filters.To.Format(time.RFC3339Nano)}
	where := []string{"e.ts >= ?", "e.ts <= ?"}
	join := ""

	if filters.ProjectID != "" {
		where = append(where, "e.project_id = ?")
		args = append(args, filters.ProjectID)
	}
	if filters.Kind != "" {
		where = append(where, "e.kind = ?")
		args = append(args, filters.Kind)
	}
	if filters.Level != "" {
		where = append(where, "e.level = ?")
		args = append(args, filters.Level)
	}
	if filters.Name != "" {
		where = append(where, "e.name = ?")
		args = append(args, filters.Name)
	}
	if filters.Query != "" {
		join = "JOIN events_fts f ON f.event_id = e.event_id"
		where = append(where, "f.events_fts MATCH ?")
		args = append(args, filters.Query)
	}

	clause := strings.Join(where, " AND ")
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM events e %s WHERE %s`, join, clause)
	var total int
	if err := s.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count logs: %w", err)
	}

	offset := (filters.Page - 1) * filters.Limit
	listArgs := append(append([]any{}, args...), filters.Limit, offset)
	listQuery := fmt.Sprintf(
		`SELECT e.event_id, e.received_at, e.schema_version, e.project_id, e.kind, e.ts, e.source, e.trace_id, e.span_id, e.level, e.name, e.attrs_json, e.body_json
		 FROM events e %s
		 WHERE %s
		 ORDER BY e.ts DESC, e.received_at DESC
		 LIMIT ? OFFSET ?`,
		join,
		clause,
	)

	rows, err := s.db.Query(listQuery, listArgs...)
	if err != nil {
		return nil, fmt.Errorf("list logs: %w", err)
	}

	events := []event.StoredEvent{}
	for rows.Next() {
		var ev event.StoredEvent
		var kind string
		var attrs, body string
		if err := rows.Scan(
			&ev.EventID,
			&ev.ReceivedAt,
			&ev.SchemaVersion,
			&ev.ProjectID,
			&kind,
			&ev.TS,
			&ev.Source,
			&ev.TraceID,
			&ev.SpanID,
			&ev.Level,
			&ev.Name,
			&attrs,
			&body,
		); err != nil {
			return nil, err
		}
		ev.Kind = event.Kind(kind)
		ev.Attrs = json.RawMessage(attrs)
		ev.Body = json.RawMessage(body)
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	status, err := s.GetSyncStatus()
	if err != nil {
		return nil, err
	}

	return &LogList{
		Events:     events,
		Page:       filters.Page,
		Limit:      filters.Limit,
		Total:      total,
		Warnings:   resultWarnings(filters.RangeFilters, total),
		SyncStatus: status,
	}, nil
}

func (s *Store) ListTraces(filters query.RangeFilters) (*TraceList, error) {
	args := []any{filters.From.Format(time.RFC3339Nano), filters.To.Format(time.RFC3339Nano)}
	where := []string{"ended_at >= ?", "started_at <= ?"}
	if filters.ProjectID != "" {
		where = append(where, "project_id = ?")
		args = append(args, filters.ProjectID)
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRow(
		fmt.Sprintf(`SELECT COUNT(*) FROM trace_summaries WHERE %s`, clause),
		args...,
	).Scan(&total); err != nil {
		return nil, fmt.Errorf("count traces: %w", err)
	}

	offset := (filters.Page - 1) * filters.Limit
	rows, err := s.db.Query(
		fmt.Sprintf(`SELECT trace_id, project_id, name, started_at, ended_at, event_count FROM trace_summaries WHERE %s ORDER BY started_at DESC LIMIT ? OFFSET ?`, clause),
		append(args, filters.Limit, offset)...,
	)
	if err != nil {
		return nil, fmt.Errorf("list traces: %w", err)
	}

	traces := []TraceTimeline{}
	for rows.Next() {
		var timeline TraceTimeline
		if err := rows.Scan(&timeline.TraceID, &timeline.ProjectID, &timeline.Name, &timeline.StartedAt, &timeline.EndedAt, &timeline.EventCount); err != nil {
			return nil, err
		}
		traces = append(traces, timeline)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	for i := range traces {
		eventRows, err := s.db.Query(
			`SELECT event_id, ts, name, level, source, span_id, attrs_json, body_json
			 FROM events
			 WHERE trace_id = ?
			 ORDER BY ts ASC, received_at ASC`,
			traces[i].TraceID,
		)
		if err != nil {
			return nil, err
		}

		for eventRows.Next() {
			var traceEvent TraceEvent
			var attrs, body string
			if err := eventRows.Scan(&traceEvent.EventID, &traceEvent.TS, &traceEvent.Name, &traceEvent.Level, &traceEvent.Source, &traceEvent.SpanID, &attrs, &body); err != nil {
				eventRows.Close()
				return nil, err
			}
			traceEvent.Attrs = json.RawMessage(attrs)
			traceEvent.Body = json.RawMessage(body)
			traces[i].Events = append(traces[i].Events, traceEvent)
		}
		if err := eventRows.Err(); err != nil {
			eventRows.Close()
			return nil, err
		}
		if err := eventRows.Close(); err != nil {
			return nil, err
		}
	}

	status, err := s.GetSyncStatus()
	if err != nil {
		return nil, err
	}

	return &TraceList{
		Traces:     traces,
		Page:       filters.Page,
		Limit:      filters.Limit,
		Total:      total,
		Warnings:   resultWarnings(filters, total),
		SyncStatus: status,
	}, nil
}

func (s *Store) GetStats(filters query.RangeFilters) (*StatsSummary, error) {
	args := []any{filters.From.Format("2006-01-02"), filters.To.Format("2006-01-02")}
	where := []string{"day >= ?", "day <= ?"}
	if filters.ProjectID != "" {
		where = append(where, "project_id = ?")
		args = append(args, filters.ProjectID)
	}
	clause := strings.Join(where, " AND ")

	summary := &StatsSummary{}

	rows, err := s.db.Query(
		fmt.Sprintf(`SELECT kind, SUM(event_count) FROM stats_daily WHERE %s GROUP BY kind ORDER BY SUM(event_count) DESC`, clause),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("stats by kind: %w", err)
	}
	for rows.Next() {
		var label string
		var count int
		if err := rows.Scan(&label, &count); err != nil {
			rows.Close()
			return nil, err
		}
		summary.ByKind = append(summary.ByKind, CountByValue{Label: label, Count: count})
		summary.TotalEvents += count
	}
	rows.Close()

	rows, err = s.db.Query(
		fmt.Sprintf(`SELECT level, SUM(event_count) FROM stats_daily WHERE %s AND level <> '' GROUP BY level ORDER BY SUM(event_count) DESC`, clause),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("stats by level: %w", err)
	}
	for rows.Next() {
		var label string
		var count int
		if err := rows.Scan(&label, &count); err != nil {
			rows.Close()
			return nil, err
		}
		summary.ByLevel = append(summary.ByLevel, CountByValue{Label: label, Count: count})
	}
	rows.Close()

	if err := s.db.QueryRow(
		fmt.Sprintf(`SELECT COALESCE(SUM(token_total), 0), COALESCE(SUM(cost_total), 0) FROM stats_daily WHERE %s`, clause),
		args...,
	).Scan(&summary.TokenTotal, &summary.CostTotal); err != nil {
		return nil, fmt.Errorf("stats totals: %w", err)
	}

	rows, err = s.db.Query(
		fmt.Sprintf(`SELECT day, SUM(event_count) FROM stats_daily WHERE %s GROUP BY day ORDER BY day ASC`, clause),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("stats volume: %w", err)
	}
	for rows.Next() {
		var day string
		var count int
		if err := rows.Scan(&day, &count); err != nil {
			rows.Close()
			return nil, err
		}
		summary.Volume = append(summary.Volume, DailyVolume{Day: day, Count: count})
	}
	rows.Close()

	status, err := s.GetSyncStatus()
	if err != nil {
		return nil, err
	}
	summary.SyncStatus = status
	return summary, nil
}

func resultWarnings(filters query.RangeFilters, total int) []ResultWarning {
	warnings := []ResultWarning{}
	if filters.LimitCapped {
		warnings = append(warnings, ResultWarning{
			Code:    "LIMIT_CAPPED",
			Message: fmt.Sprintf("Requested limit %d exceeded the max page size of %d, so Vigil used %d.", filters.RequestedLimit, query.MaxPageSize, filters.Limit),
		})
	}

	if filters.Page*filters.Limit < total {
		warnings = append(warnings, ResultWarning{
			Code:    "MORE_RESULTS_AVAILABLE",
			Message: fmt.Sprintf("Showing page %d of a larger result set. %d total records match this filter.", filters.Page, total),
		})
	}

	return warnings
}
