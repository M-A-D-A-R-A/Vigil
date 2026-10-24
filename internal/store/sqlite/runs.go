package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"vigil/internal/event"
	"vigil/internal/query"
)

type RunSummary struct {
	RunID          string  `json:"run_id"`
	ProjectID      string  `json:"project_id"`
	Source         string  `json:"source"`
	Name           string  `json:"name"`
	Status         string  `json:"status"`
	Repo           string  `json:"repo,omitempty"`
	Branch         string  `json:"branch,omitempty"`
	Worktree       string  `json:"worktree,omitempty"`
	Model          string  `json:"model,omitempty"`
	TraceID        string  `json:"trace_id,omitempty"`
	PromptSummary  string  `json:"prompt_summary,omitempty"`
	FailureClass   string  `json:"failure_class,omitempty"`
	OutcomeSummary string  `json:"outcome_summary,omitempty"`
	StartedAt      string  `json:"started_at"`
	EndedAt        string  `json:"ended_at"`
	TotalTokens    float64 `json:"total_tokens"`
	TotalCostUSD   float64 `json:"total_cost_usd"`
	StepCount      int     `json:"step_count"`
	ErrorCount     int     `json:"error_count"`
}

type RunStep struct {
	StepID     string          `json:"step_id"`
	RunID      string          `json:"run_id"`
	TraceID    string          `json:"trace_id,omitempty"`
	SpanID     string          `json:"span_id,omitempty"`
	TS         string          `json:"ts"`
	StepType   string          `json:"step_type"`
	Name       string          `json:"name"`
	Status     string          `json:"status,omitempty"`
	DurationMS float64         `json:"duration_ms"`
	Tokens     float64         `json:"tokens"`
	CostUSD    float64         `json:"cost_usd"`
	Attrs      json.RawMessage `json:"attrs"`
	Body       json.RawMessage `json:"body"`
}

type RunList struct {
	Runs       []RunSummary `json:"runs"`
	Page       int          `json:"page"`
	Limit      int          `json:"limit"`
	Total      int          `json:"total"`
	SyncStatus SyncStatus   `json:"sync"`
}

type RunDetail struct {
	Run   RunSummary `json:"run"`
	Steps []RunStep  `json:"steps"`
}

type runMeta struct {
	status       string
	repo         string
	branch       string
	worktree     string
	model        string
	prompt       string
	failureClass string
	outcome      string
	durationMS   float64
}

func runMetaFromEvent(ev *event.StoredEvent) runMeta {
	attrs := decodeRawMap(ev.Attrs)
	nested := map[string]any{}
	if run, ok := attrs["run"].(map[string]any); ok {
		nested = run
	}
	return runMeta{
		status:       firstString(attrs, nested, "status"),
		repo:         firstString(attrs, nested, "repo", "repository", "repo_path"),
		branch:       firstString(attrs, nested, "branch", "git_branch"),
		worktree:     firstString(attrs, nested, "worktree", "git_worktree"),
		model:        firstString(attrs, nested, "model", "model_name"),
		prompt:       firstString(attrs, nested, "prompt", "prompt_summary"),
		failureClass: firstString(attrs, nested, "failure_class", "failure"),
		outcome:      firstString(attrs, nested, "outcome", "outcome_summary"),
		durationMS:   firstNumber(attrs, nested, "duration_ms"),
	}
}

func (s *Store) upsertRunLifecycle(tx *sql.Tx, ev *event.StoredEvent, now time.Time) error {
	meta := runMetaFromEvent(ev)
	status := normalizeRunStatus(meta.status, ev.Name)
	tokens, cost := event.ExtractUsageTotals(ev)
	endedAt := ""
	if isTerminalRunStatus(status) {
		endedAt = ev.TS
	}
	updatedAt := now.UTC().Format(time.RFC3339Nano)

	_, err := tx.Exec(
		`INSERT INTO runs(
			run_id, project_id, source, name, status, repo, branch, worktree, model, trace_id,
			prompt_summary, failure_class, outcome_summary, started_at, ended_at,
			total_tokens, total_cost_usd, step_count, error_count, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?)
		ON CONFLICT(run_id) DO UPDATE SET
			source = CASE WHEN excluded.source <> '' THEN excluded.source ELSE runs.source END,
			name = CASE WHEN runs.name = '' THEN excluded.name ELSE runs.name END,
			status = CASE WHEN excluded.status <> '' THEN excluded.status ELSE runs.status END,
			repo = CASE WHEN excluded.repo <> '' THEN excluded.repo ELSE runs.repo END,
			branch = CASE WHEN excluded.branch <> '' THEN excluded.branch ELSE runs.branch END,
			worktree = CASE WHEN excluded.worktree <> '' THEN excluded.worktree ELSE runs.worktree END,
			model = CASE WHEN excluded.model <> '' THEN excluded.model ELSE runs.model END,
			trace_id = CASE WHEN excluded.trace_id <> '' THEN excluded.trace_id ELSE runs.trace_id END,
			prompt_summary = CASE WHEN excluded.prompt_summary <> '' THEN excluded.prompt_summary ELSE runs.prompt_summary END,
			failure_class = CASE WHEN excluded.failure_class <> '' THEN excluded.failure_class ELSE runs.failure_class END,
			outcome_summary = CASE WHEN excluded.outcome_summary <> '' THEN excluded.outcome_summary ELSE runs.outcome_summary END,
			started_at = CASE
				WHEN runs.started_at = '' THEN excluded.started_at
				WHEN excluded.started_at <> '' AND excluded.started_at < runs.started_at THEN excluded.started_at
				ELSE runs.started_at END,
			ended_at = CASE
				WHEN excluded.ended_at = '' THEN runs.ended_at
				WHEN runs.ended_at = '' OR excluded.ended_at > runs.ended_at THEN excluded.ended_at
				ELSE runs.ended_at END,
			total_tokens = CASE WHEN excluded.total_tokens > 0 AND runs.step_count = 0 THEN excluded.total_tokens ELSE runs.total_tokens END,
			total_cost_usd = CASE WHEN excluded.total_cost_usd > 0 AND runs.step_count = 0 THEN excluded.total_cost_usd ELSE runs.total_cost_usd END,
			updated_at = excluded.updated_at`,
		ev.RunID,
		ev.ProjectID,
		ev.Source,
		ev.Name,
		status,
		meta.repo,
		meta.branch,
		meta.worktree,
		meta.model,
		ev.TraceID,
		meta.prompt,
		meta.failureClass,
		meta.outcome,
		ev.TS,
		endedAt,
		tokens,
		cost,
		updatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert run lifecycle: %w", err)
	}
	return nil
}

func (s *Store) insertRunStep(tx *sql.Tx, ev *event.StoredEvent, now time.Time) error {
	meta := runMetaFromEvent(ev)
	status := strings.ToLower(strings.TrimSpace(meta.status))
	if status == "" {
		status = strings.ToLower(strings.TrimSpace(ev.Level))
	}
	tokens, cost := event.ExtractUsageTotals(ev)

	attrs := ev.Attrs
	if len(attrs) == 0 {
		attrs = json.RawMessage("{}")
	}
	body := ev.Body
	if len(body) == 0 {
		body = json.RawMessage("null")
	}

	if _, err := tx.Exec(
		`INSERT OR REPLACE INTO run_steps(
			step_id, run_id, project_id, event_id, ts, trace_id, span_id, step_type, name, status,
			duration_ms, tokens, cost_usd, attrs_json, body_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.EventID,
		ev.RunID,
		ev.ProjectID,
		ev.EventID,
		ev.TS,
		ev.TraceID,
		ev.SpanID,
		ev.StepType,
		ev.Name,
		status,
		meta.durationMS,
		tokens,
		cost,
		string(attrs),
		string(body),
	); err != nil {
		return fmt.Errorf("insert run step: %w", err)
	}

	errorCount := 0
	if status == "error" || status == "failed" || status == "failure" {
		errorCount = 1
	}
	if _, err := tx.Exec(
		`INSERT INTO runs(
			run_id, project_id, source, name, trace_id, status, started_at, ended_at,
			total_tokens, total_cost_usd, step_count, error_count, updated_at
		) VALUES (?, ?, ?, ?, ?, 'running', ?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(run_id) DO UPDATE SET
			source = CASE WHEN runs.source = '' THEN excluded.source ELSE runs.source END,
			name = CASE WHEN runs.name = '' THEN excluded.name ELSE runs.name END,
			trace_id = CASE WHEN runs.trace_id = '' THEN excluded.trace_id ELSE runs.trace_id END,
			started_at = CASE
				WHEN runs.started_at = '' THEN excluded.started_at
				WHEN excluded.started_at <> '' AND excluded.started_at < runs.started_at THEN excluded.started_at
				ELSE runs.started_at END,
			ended_at = CASE
				WHEN excluded.ended_at > runs.ended_at THEN excluded.ended_at
				ELSE runs.ended_at END,
			total_tokens = runs.total_tokens + excluded.total_tokens,
			total_cost_usd = runs.total_cost_usd + excluded.total_cost_usd,
			step_count = runs.step_count + 1,
			error_count = runs.error_count + ?,
			updated_at = excluded.updated_at`,
		ev.RunID,
		ev.ProjectID,
		ev.Source,
		ev.Name,
		ev.TraceID,
		ev.TS,
		ev.TS,
		tokens,
		cost,
		errorCount,
		now.UTC().Format(time.RFC3339Nano),
		errorCount,
	); err != nil {
		return fmt.Errorf("upsert run from step: %w", err)
	}
	return nil
}

func (s *Store) ListRuns(filters query.RangeFilters) (*RunList, error) {
	args := []any{filters.From.Format(time.RFC3339Nano), filters.To.Format(time.RFC3339Nano)}
	where := []string{"started_at >= ?", "started_at <= ?"}
	if filters.ProjectID != "" {
		where = append(where, "project_id = ?")
		args = append(args, filters.ProjectID)
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM runs WHERE %s`, clause), args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count runs: %w", err)
	}

	offset := (filters.Page - 1) * filters.Limit
	rows, err := s.db.Query(
		fmt.Sprintf(
			`SELECT run_id, project_id, source, name, status, repo, branch, worktree, model, trace_id,
			        prompt_summary, failure_class, outcome_summary, started_at, ended_at,
			        total_tokens, total_cost_usd, step_count, error_count
			 FROM runs WHERE %s ORDER BY started_at DESC LIMIT ? OFFSET ?`,
			clause,
		),
		append(args, filters.Limit, offset)...,
	)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()

	runs := []RunSummary{}
	for rows.Next() {
		var run RunSummary
		if err := rows.Scan(
			&run.RunID,
			&run.ProjectID,
			&run.Source,
			&run.Name,
			&run.Status,
			&run.Repo,
			&run.Branch,
			&run.Worktree,
			&run.Model,
			&run.TraceID,
			&run.PromptSummary,
			&run.FailureClass,
			&run.OutcomeSummary,
			&run.StartedAt,
			&run.EndedAt,
			&run.TotalTokens,
			&run.TotalCostUSD,
			&run.StepCount,
			&run.ErrorCount,
		); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	status, err := s.GetSyncStatus()
	if err != nil {
		return nil, err
	}
	return &RunList{
		Runs:       runs,
		Page:       filters.Page,
		Limit:      filters.Limit,
		Total:      total,
		SyncStatus: status,
	}, nil
}

func (s *Store) GetRun(runID string) (*RunDetail, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("run_id is required")
	}

	row := s.db.QueryRow(
		`SELECT run_id, project_id, source, name, status, repo, branch, worktree, model, trace_id,
		        prompt_summary, failure_class, outcome_summary, started_at, ended_at,
		        total_tokens, total_cost_usd, step_count, error_count
		 FROM runs WHERE run_id = ?`,
		runID,
	)
	var run RunSummary
	if err := row.Scan(
		&run.RunID,
		&run.ProjectID,
		&run.Source,
		&run.Name,
		&run.Status,
		&run.Repo,
		&run.Branch,
		&run.Worktree,
		&run.Model,
		&run.TraceID,
		&run.PromptSummary,
		&run.FailureClass,
		&run.OutcomeSummary,
		&run.StartedAt,
		&run.EndedAt,
		&run.TotalTokens,
		&run.TotalCostUSD,
		&run.StepCount,
		&run.ErrorCount,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("run not found")
		}
		return nil, fmt.Errorf("load run: %w", err)
	}

	rows, err := s.db.Query(
		`SELECT step_id, run_id, ts, trace_id, span_id, step_type, name, status, duration_ms, tokens, cost_usd, attrs_json, body_json
		 FROM run_steps WHERE run_id = ? ORDER BY ts ASC, rowid ASC`,
		runID,
	)
	if err != nil {
		return nil, fmt.Errorf("list run steps: %w", err)
	}
	defer rows.Close()

	steps := []RunStep{}
	for rows.Next() {
		var step RunStep
		var attrs, body string
		if err := rows.Scan(
			&step.StepID,
			&step.RunID,
			&step.TS,
			&step.TraceID,
			&step.SpanID,
			&step.StepType,
			&step.Name,
			&step.Status,
			&step.DurationMS,
			&step.Tokens,
			&step.CostUSD,
			&attrs,
			&body,
		); err != nil {
			return nil, err
		}
		step.Attrs = json.RawMessage(attrs)
		step.Body = json.RawMessage(body)
		steps = append(steps, step)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &RunDetail{Run: run, Steps: steps}, nil
}

func normalizeRunStatus(raw string, name string) string {
	status := strings.ToLower(strings.TrimSpace(raw))
	if status == "" {
		lower := strings.ToLower(name)
		switch {
		case strings.Contains(lower, "cancel"):
			return "canceled"
		case strings.Contains(lower, "fail"), strings.Contains(lower, "error"):
			return "failed"
		case strings.Contains(lower, "complete"), strings.Contains(lower, "succeed"),
			strings.Contains(lower, "success"), strings.Contains(lower, "finish"), strings.Contains(lower, "end"):
			return "succeeded"
		case strings.Contains(lower, "start"):
			return "running"
		default:
			return "running"
		}
	}

	switch status {
	case "complete", "completed", "success", "succeeded", "ok", "passed":
		return "succeeded"
	case "fail", "failed", "error", "errored":
		return "failed"
	case "cancel", "canceled", "cancelled", "aborted":
		return "canceled"
	case "running", "in_progress", "started":
		return "running"
	default:
		return status
	}
}

func isTerminalRunStatus(status string) bool {
	switch status {
	case "succeeded", "failed", "canceled":
		return true
	default:
		return false
	}
}

func decodeRawMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return map[string]any{}
	}
	return value
}

func firstString(primary, nested map[string]any, keys ...string) string {
	for _, source := range []map[string]any{primary, nested} {
		for _, key := range keys {
			if value, ok := source[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func firstNumber(primary, nested map[string]any, keys ...string) float64 {
	for _, source := range []map[string]any{primary, nested} {
		for _, key := range keys {
			switch value := source[key].(type) {
			case float64:
				return value
			case int:
				return float64(value)
			case int64:
				return float64(value)
			}
		}
	}
	return 0
}
